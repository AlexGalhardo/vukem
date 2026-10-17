package vukem

import (
	"errors"
	"fmt"
	"time"

	"github.com/AlexGalhardo/vukem/cache"
	"github.com/AlexGalhardo/vukem/ratelimit"
)

// RateLimitConfig is read from RATE_LIMIT_* variables.
type RateLimitConfig struct {
	// Store is "memory" (default: GCRA per process) or "cache" (fixed
	// windows counted in App.Cache, which must be shared by every server:
	// CACHE_STORE=database or a Redis store set with WithCacheStore).
	Store string `env:"RATE_LIMIT_STORE" default:"memory"`
}

// RateLimiters returns a factory for the limiters routes use, chosen by
// RATE_LIMIT_STORE, so switching to a shared store needs no code change:
//
//	limiters, err := app.RateLimiters()
//	login := ratelimit.Middleware(limiters("login", 5, time.Minute), account.LoginThrottleKey)
//
// An unknown store is an error, not a silent fallback to memory, and so
// is RATE_LIMIT_STORE=cache over a store [cache.Shared] does not report
// as shared (the per-process memory cache, or a custom store or wrapper
// that does not declare cache.Sharer).
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
		shared, err := a.cacheShared()
		if err != nil {
			return nil, err
		}

		if !shared {
			return nil, errors.New("vukem: RATE_LIMIT_STORE=cache needs a cache shared by every server " +
				"(CACHE_STORE=database or vukem.WithCacheStore with Redis; a custom store or wrapper must implement " +
				"cache.Sharer); the memory cache keeps one budget per process")
		}

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

// cacheShared reports whether App.Cache reaches every server
// ([cache.Shared]): CACHE_STORE=database, or a store set with
// WithCacheStore / WithCacheStoreFunc that declares cache.Sharer (Redis).
// It resolves the store through Cache, so a lazy store is read under its
// sync.Once.
func (a *App) cacheShared() (bool, error) {
	c, err := a.Cache()
	if err != nil {
		return false, err
	}

	return cache.Shared(c.Store()), nil
}
