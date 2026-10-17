package vukem

import (
	"fmt"
	"time"

	"github.com/AlexGalhardo/vukem/ratelimit"
)

// RateLimitConfig is read from RATE_LIMIT_* variables.
type RateLimitConfig struct {
	// Store is "memory" (default: GCRA per process) or "cache" (fixed
	// windows counted in App.Cache, shared by every server when
	// CACHE_STORE=database or a Redis store is set).
	Store string `env:"RATE_LIMIT_STORE" default:"memory"`
}

// RateLimiters returns a factory for the limiters routes use, chosen by
// RATE_LIMIT_STORE, so switching to a shared store needs no code change:
//
//	limiters, err := app.RateLimiters()
//	login := ratelimit.Middleware(limiters("login", 5, time.Minute), account.LoginThrottleKey)
//
// An unknown store is an error, not a silent fallback to memory.
func (a *App) RateLimiters() (ratelimit.Factory, error) {
	cfg, err := configLoad[RateLimitConfig](a)
	if err != nil {
		return nil, err
	}

	switch cfg.Store {
	case "memory":
		return func(_ string, limit int, window time.Duration) ratelimit.Limiter {
			return ratelimit.NewMemory(limit, window)
		}, nil
	case "cache":
		c, err := a.Cache()
		if err != nil {
			return nil, err
		}

		return func(name string, limit int, window time.Duration) ratelimit.Limiter {
			return ratelimit.NewWindow(c, name, limit, window)
		}, nil
	default:
		return nil, fmt.Errorf("vukem: unknown RATE_LIMIT_STORE %q (memory or cache)", cfg.Store)
	}
}
