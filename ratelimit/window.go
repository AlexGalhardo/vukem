package ratelimit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"time"
)

// Counter is the store a [Window] counts in: a cache.Store (database,
// Redis), a *cache.Cache (adds its key prefix) or anything whose
// Increment is atomic across the servers sharing it.
type Counter interface {
	Increment(ctx context.Context, key string, delta int64, ttl time.Duration) (int64, error)
}

// Factory builds a limiter of limit requests per window; name separates
// the budgets of limiters sharing one store ("login", "api").
type Factory func(name string, limit int, window time.Duration) Limiter

// Window is a fixed-window limiter over a shared [Counter]: every server
// using the same store shares one budget per key. Windows are aligned to
// the clock, so a client can send up to 2×limit around a window boundary
// (limit at the end of one, limit at the start of the next); use [Memory]
// or the Redis GCRA limiter when bursts must stay within limit.
type Window struct {
	counter Counter
	name    string
	limit   int
	window  time.Duration
	now     func() time.Time
}

// NewWindow allows limit requests per window for each key, counted in c
// under "ratelimit:<name>:<SHA-256 of key>:<window start>". name is 1-64
// letters, digits, '.', '_' or '-'; with any other name every Allow
// fails, so the route denies (503) instead of sharing budgets.
func NewWindow(c Counter, name string, limit int, window time.Duration) *Window {
	return &Window{
		counter: c, name: name, limit: max(limit, 1),
		window: max(window, time.Second), now: time.Now,
	}
}

var nameRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// Allow implements Limiter. Denied requests count too, so a client that
// keeps hammering stays limited until the window ends.
func (w *Window) Allow(ctx context.Context, key string) (Result, error) {
	if !nameRe.MatchString(w.name) {
		return Result{}, fmt.Errorf("ratelimit: invalid limiter name %q (1-64 of A-Z a-z 0-9 . _ -)", w.name)
	}

	now := w.now()
	start := now.Truncate(w.window)
	reset := start.Add(w.window).Sub(now)

	// The TTL spans a whole window, not just its rest: a server whose clock
	// runs a little behind must not see the counter expire and restart.
	n, err := w.counter.Increment(ctx, w.bucket(key, start), 1, w.window)
	if err != nil {
		return Result{}, fmt.Errorf("ratelimit: count %s: %w", w.name, err)
	}

	res := Result{Limit: w.limit, ResetAfter: reset}
	if n > int64(w.limit) {
		res.RetryAfter = reset
		return res, nil
	}

	res.Allowed = true
	res.Remaining = w.limit - int(n)

	return res, nil
}

// bucket hashes every key: keys come from requests, so a raw key could
// carry separators or forge the encoding of another one, and the hash
// keeps every database key within VARBINARY(255) on MySQL.
func (w *Window) bucket(key string, start time.Time) string {
	sum := sha256.Sum256([]byte(key))

	return "ratelimit:" + w.name + ":" + hex.EncodeToString(sum[:16]) + ":" + strconv.FormatInt(start.UnixMilli(), 10)
}
