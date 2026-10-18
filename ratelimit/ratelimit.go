// Package ratelimit limits requests per key (token, user, IP). [Memory]
// uses GCRA, a smooth token bucket that needs one timestamp per key, and
// fits one process; [Window] counts fixed windows in a shared store (the
// database cache, so several servers on one SQL database share budgets);
// drivers/redis provides a shared GCRA limiter.
//
// [Middleware] applies a [Limiter] to a route, keyed by a [KeyFunc] such
// as [ByIP] or [ByToken]; it sets the RateLimit-* headers, answers 429
// with Retry-After and denies when the limiter fails. Apps build limiters
// with app.RateLimiters(), configured by RATE_LIMIT_STORE.
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/subsystems.md.
package ratelimit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"math"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/internal/principal"
	"github.com/AlexGalhardo/vukem/web"
)

// Result is the outcome of one Allow call.
type Result struct {
	Allowed    bool
	Limit      int
	Remaining  int
	ResetAfter time.Duration // until the bucket is full again
	RetryAfter time.Duration // when denied: until the next request is allowed
}

// Limiter decides whether a request for key may proceed.
type Limiter interface {
	Allow(ctx context.Context, key string) (Result, error)
}

// Memory is an in-process GCRA limiter: limit requests per window, with
// bursts up to limit.
type Memory struct {
	limit    int
	interval time.Duration // window / limit
	window   time.Duration
	now      func() time.Time

	mu    sync.Mutex
	tat   map[string]time.Time // theoretical arrival time per key
	calls int
}

// NewMemory allows limit requests per window for each key.
func NewMemory(limit int, window time.Duration) *Memory {
	limit = max(limit, 1)

	return &Memory{
		limit:    limit,
		window:   window,
		interval: window / time.Duration(limit),
		now:      time.Now,
		tat:      map[string]time.Time{},
	}
}

// Allow implements Limiter.
func (m *Memory) Allow(_ context.Context, key string) (Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.now()

	m.calls++
	if m.calls%1024 == 0 {
		m.prune(now)
	}

	tat := m.tat[key]
	if tat.Before(now) {
		tat = now
	}

	next := tat.Add(m.interval)
	allowAt := next.Add(-m.window)

	if now.Before(allowAt) {
		return Result{
			Limit:      m.limit,
			ResetAfter: tat.Sub(now),
			RetryAfter: allowAt.Sub(now),
		}, nil
	}

	m.tat[key] = next
	remaining := int(now.Sub(allowAt) / m.interval)

	return Result{
		Allowed:    true,
		Limit:      m.limit,
		Remaining:  min(remaining, m.limit-1),
		ResetAfter: next.Sub(now),
	}, nil
}

// prune drops keys whose bucket is full again, bounding memory.
func (m *Memory) prune(now time.Time) {
	for k, t := range m.tat {
		if !t.After(now) {
			delete(m.tat, k)
		}
	}
}

// KeyFunc picks the bucket for a request; "" skips limiting.
type KeyFunc func(r *http.Request) string

// DefaultIPv6Prefix is the IPv6 prefix length [ByIP] groups clients by
// when web.Services.IPv6Prefix is 0.
const DefaultIPv6Prefix = 64

// ByIP keys requests by client IP (honouring trusted proxies). IPv4 keys
// are per address (IPv4-mapped IPv6 counts as IPv4); IPv6 keys are per
// web.Services.IPv6Prefix network (default /64, RATE_LIMIT_IPV6_PREFIX in
// apps), because a client owning a /64 can pick a new address per request.
func ByIP(r *http.Request) string {
	s := web.ServicesFrom(r.Context())
	return "ip:" + IPKey(web.NewCtx(nil, r, s).IP(), s.IPv6Prefix)
}

// IPKey returns the per-client part of an IP rate-limit key: an IPv4
// address as is (IPv4-mapped IPv6 unmapped), an IPv6 address as its
// network of prefix bits ("2001:db8:1:2::/64"); a prefix outside 1-128
// means [DefaultIPv6Prefix]. A value that is not an IP is returned as is:
// it is still one stable key per peer. Custom KeyFuncs that combine the
// client IP with other parts (email, user) use it, or [ByIP], so IPv6
// clients cannot rotate addresses inside their /64.
func IPKey(ip string, prefix int) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}

	// ParseAddr accepts only the canonical dotted form for IPv4, so ip
	// is already the key.
	if addr.Is4() {
		return ip
	}

	addr = addr.Unmap()
	if addr.Is4() {
		return addr.String()
	}

	bits := prefix
	if bits < 1 || bits > 128 {
		bits = DefaultIPv6Prefix
	}

	p, err := addr.WithZone("").Prefix(bits)
	if err != nil {
		return addr.String()
	}

	return p.String()
}

// ByToken keys requests by bearer token (hashed, never stored in clear),
// falling back to the client IP. Use it after authentication
// (auth.Required), behind an IP limit: before the token is verified, a
// client rotating made-up tokens gets a fresh bucket on every request.
func ByToken(r *http.Request) string {
	if tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && tok != "" {
		sum := sha256.Sum256([]byte(tok))
		return "token:" + hex.EncodeToString(sum[:16])
	}

	return ByIP(r)
}

// ByUser keys requests by the user guard authenticated, falling back to
// the client IP for an unauthenticated one. Use it after authentication
// (auth.Required) for a limit that must count per account rather than per
// address or per token: "10 new API keys an hour" has to survive an
// attacker rotating addresses, and a session request carries no bearer
// token, so [ByToken] would key every session by IP.
//
// An anonymous caller gets its IP key, not a shared one: a constant key
// would let any caller exhaust everybody's budget, and an empty key makes
// [Middleware] pass the request through with no limit at all. Put an IP
// limit in front anyway when the route is reachable unauthenticated, the
// way [ByToken] needs one.
func ByUser(guard string) KeyFunc {
	return func(r *http.Request) string {
		if g, id, ok := principal.From(r.Context()); ok && g == guard {
			return "user:" + guard + ":" + id
		}

		return ByIP(r)
	}
}

// Middleware limits requests with l, keyed by key (ByIP when nil). It
// sets RateLimit-Limit/Remaining/Reset on every response and answers 429
// with Retry-After when the bucket is empty. A failing limiter denies the
// request (fail closed).
func Middleware(l Limiter, key KeyFunc) func(http.Handler) http.Handler {
	if key == nil {
		key = ByIP
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			k := key(r)
			if k == "" {
				next.ServeHTTP(w, r)
				return
			}

			res, err := l.Allow(r.Context(), k)
			if err != nil {
				web.Fail(w, r, errs.Wrap(http.StatusServiceUnavailable, err))
				return
			}

			h := w.Header()
			h.Set("RateLimit-Limit", strconv.Itoa(res.Limit))
			h.Set("RateLimit-Remaining", strconv.Itoa(res.Remaining))
			h.Set("RateLimit-Reset", seconds(res.ResetAfter))

			if !res.Allowed {
				web.SecurityEvent(r, "ratelimit.exceeded", "key_kind", strings.SplitN(k, ":", 2)[0])
				web.Fail(w, r, errs.New(http.StatusTooManyRequests, "Too many requests.").
					WithHeader("Retry-After", seconds(res.RetryAfter)))

				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// seconds rounds up so clients never retry too early.
func seconds(d time.Duration) string {
	return strconv.Itoa(int(math.Ceil(d.Seconds())))
}
