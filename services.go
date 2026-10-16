package vukem

import (
	"context"
	"fmt"
	"time"

	"github.com/AlexGalhardo/vukem/cache"
	"github.com/AlexGalhardo/vukem/events"
)

// WithListeners registers event listeners on App.Events (generated apps
// pass listeners.All(); vukem make:listener appends there).
func WithListeners(regs ...events.Registration) Option {
	return func(o *options) error { o.listeners = append(o.listeners, regs...); return nil }
}

// CacheConfig is read from CACHE_* variables.
type CacheConfig struct {
	// Store is "memory" (default) or "database" (table CACHE_TABLE).
	Store  string `env:"CACHE_STORE"  default:"memory"`
	Prefix string `env:"CACHE_PREFIX" default:"vukem:"`
	Table  string `env:"CACHE_TABLE"  default:"cache"`
	// MaxEntries bounds the memory store.
	MaxEntries int `env:"CACHE_MAX_ENTRIES" default:"10000"`
}

// Cache returns the application cache, built from CACHE_* on first use.
// The database store creates its table if needed.
func (a *App) Cache() (*cache.Cache, error) {
	a.cacheOnce.Do(func() {
		cfg, err := configLoad[CacheConfig](a)
		if err != nil {
			a.cacheErr = err
			return
		}

		switch cfg.Store {
		case "memory":
			a.cache = cache.New(cache.NewMemory(cfg.MaxEntries), cfg.Prefix)
		case "database":
			db, err := a.DB()
			if err != nil {
				a.cacheErr = err
				return
			}

			store, err := cache.NewDatabase(db, cfg.Table)
			if err != nil {
				a.cacheErr = err
				return
			}

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			if err := store.CreateTable(ctx); err != nil {
				a.cacheErr = fmt.Errorf("vukem: create cache table: %w", err)
				return
			}

			a.cache = cache.New(store, cfg.Prefix)
		default:
			a.cacheErr = fmt.Errorf("vukem: unknown CACHE_STORE %q (memory or database)", cfg.Store)
		}
	})

	return a.cache, a.cacheErr
}
