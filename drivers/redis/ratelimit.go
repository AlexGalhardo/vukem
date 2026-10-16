package redis

import (
	"context"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/AlexGalhardo/vukem/ratelimit"
)

// Limiter implements ratelimit.Limiter with GCRA in Redis, so every server
// shares the same budget per key.
type Limiter struct {
	rdb      goredis.UniversalClient
	prefix   string
	limit    int
	interval time.Duration
	window   time.Duration
	now      func() time.Time
}

// RateLimiter allows limit requests per window for each key (keys under
// prefix, default "ratelimit:").
func (c *Client) RateLimiter(prefix string, limit int, window time.Duration) *Limiter {
	if prefix == "" {
		prefix = "ratelimit:"
	}

	limit = max(limit, 1)

	return &Limiter{
		rdb: c.rdb, prefix: prefix, limit: limit, window: window,
		interval: max(window/time.Duration(limit), time.Millisecond), now: time.Now,
	}
}

//nolint:gochecknoglobals // compiled once, read-only
var gcra = goredis.NewScript(`
local now, interval, window = tonumber(ARGV[1]), tonumber(ARGV[2]), tonumber(ARGV[3])
local tat = tonumber(redis.call("GET", KEYS[1])) or now
if tat < now then tat = now end
local nxt = tat + interval
local allow_at = nxt - window
if now < allow_at then return {0, tat - now, allow_at - now} end
redis.call("SET", KEYS[1], nxt, "PX", nxt - now)
return {1, nxt - now, math.floor((now - allow_at) / interval)}`)

// Allow implements ratelimit.Limiter.
func (l *Limiter) Allow(ctx context.Context, key string) (ratelimit.Result, error) {
	res, err := gcra.Run(ctx, l.rdb, []string{l.prefix + key},
		l.now().UnixMilli(), l.interval.Milliseconds(), l.window.Milliseconds()).Int64Slice()
	if err != nil {
		return ratelimit.Result{}, err
	}

	r := ratelimit.Result{Limit: l.limit, ResetAfter: time.Duration(res[1]) * time.Millisecond}
	if res[0] == 0 {
		r.RetryAfter = time.Duration(res[2]) * time.Millisecond
		return r, nil
	}

	r.Allowed = true
	r.Remaining = min(int(res[2]), l.limit-1)

	return r, nil
}
