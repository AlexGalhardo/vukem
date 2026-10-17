package vukem

import (
	"context"
	"fmt"
	"time"

	"github.com/AlexGalhardo/vukem/cache"
	"github.com/AlexGalhardo/vukem/events"
	"github.com/AlexGalhardo/vukem/session"
)

// WithListeners registers event listeners on App.Events (generated apps
// pass listeners.All(); vukem make:listener appends there).
func WithListeners(regs ...events.Registration) Option {
	return func(o *options) error { o.listeners = append(o.listeners, regs...); return nil }
}

// WithCacheStore replaces the store chosen by CACHE_STORE (e.g. Redis:
// vukem.WithCacheStore(redisClient.CacheStore("cache:"))). The last of
// WithCacheStore and WithCacheStoreFunc wins.
func WithCacheStore(s cache.Store) Option {
	return func(o *options) error { o.cacheStore, o.cacheFunc = s, nil; return nil }
}

// WithCacheStoreFunc replaces the store chosen by CACHE_STORE with one
// built by fn on the first app.Cache() call, after configuration loaded,
// so fn can read app.Source (e.g. REDIS_URL from .env or vukemtest). An
// error from fn is returned by app.Cache(). fn may register cleanup with
// app.OnShutdown.
func WithCacheStoreFunc(fn func(app *App) (cache.Store, error)) Option {
	return func(o *options) error { o.cacheFunc, o.cacheStore = fn, nil; return nil }
}

// WithSessionStore replaces the store chosen by SESSION_DRIVER (e.g.
// Redis: vukem.WithSessionStore(redisClient.SessionStore(""))). The last
// of WithSessionStore and WithSessionStoreFunc wins.
func WithSessionStore(s session.Store) Option {
	return func(o *options) error { o.sessionStore, o.sessionFunc = s, nil; return nil }
}

// WithSessionStoreFunc replaces the store chosen by SESSION_DRIVER with one
// built by fn on the first app.Sessions() call (DefaultMiddleware makes it
// at boot), after configuration loaded. An error from fn is returned by
// app.Sessions(). SetSessionStore still takes precedence.
func WithSessionStoreFunc(fn func(app *App) (session.Store, error)) Option {
	return func(o *options) error { o.sessionFunc, o.sessionStore = fn, nil; return nil }
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

		switch {
		case a.cacheStore != nil:
			a.cache = cache.New(a.cacheStore, cfg.Prefix)
		case a.cacheStoreFunc != nil:
			store, err := a.cacheStoreFunc(a)
			if err != nil {
				a.cacheErr = fmt.Errorf("vukem: cache store: %w", err)
				return
			}

			a.cacheStore = store
			a.cache = cache.New(store, cfg.Prefix)
		case cfg.Store == "memory":
			a.cache = cache.New(cache.NewMemory(cfg.MaxEntries), cfg.Prefix)
		case cfg.Store == "database":
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
