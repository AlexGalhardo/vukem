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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AlexGalhardo/vukem/errs"
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

// ByIP keys requests by client IP (honouring trusted proxies).
func ByIP(r *http.Request) string {
	return "ip:" + web.NewCtx(nil, r, web.ServicesFrom(r.Context())).IP()
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
