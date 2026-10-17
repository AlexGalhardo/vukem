package vukem

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/cache"
	"github.com/AlexGalhardo/vukem/internal/testutil"
)

func TestRateLimitersFromConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  map[string]string
	}{
		{name: "default is memory", env: nil},
		{name: "memory", env: map[string]string{"RATE_LIMIT_STORE": "memory"}},
		{name: "cache", env: map[string]string{"RATE_LIMIT_STORE": "cache", "CACHE_STORE": "database"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.env["CACHE_STORE"] == "database" {
				tt.env["DATABASE_URL"] = "file:" + filepath.ToSlash(filepath.Join(testutil.TempDir(t), "rl.db"))
			}

			app := newTestApp(t, WithSource(testSource(tt.env)))
			t.Cleanup(func() { _ = app.Shutdown(context.Background()) })

			limiters, err := app.RateLimiters()
			if err != nil {
				t.Fatal(err)
			}

			l := limiters("api", 1, time.Minute)
			if res, err := l.Allow(t.Context(), "ip:192.0.2.1"); err != nil || !res.Allowed {
				t.Fatalf("first Allow() = %+v, %v", res, err)
			}

			if res, _ := l.Allow(t.Context(), "ip:192.0.2.1"); res.Allowed {
				t.Fatal("second Allow() allowed, want limit 1")
			}
		})
	}
}

// A typo in RATE_LIMIT_STORE must not quietly fall back to per-process
// limits, which multiply the budget by the number of servers.
func TestSecurity_A02_UnknownRateLimitStoreFails(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, WithSource(testSource(map[string]string{"RATE_LIMIT_STORE": "databse"})))

	if limiters, err := app.RateLimiters(); err == nil || limiters != nil {
		t.Fatalf("RateLimiters() with RATE_LIMIT_STORE=databse = %v, %v, want an error", limiters != nil, err)
	}
}

// RATE_LIMIT_STORE=cache over the per-process memory cache would look
// shared while every server keeps its own budget: refuse it at boot.
func TestSecurity_A02_RateLimitCacheNeedsSharedStore(t *testing.T) {
	t.Parallel()

	env := map[string]string{"RATE_LIMIT_STORE": "cache", "CACHE_STORE": "memory"}

	app := newTestApp(t, WithSource(testSource(env)))
	if limiters, err := app.RateLimiters(); err == nil || limiters != nil {
		t.Fatalf("RateLimiters() with CACHE_STORE=memory = %v, %v, want an error", limiters != nil, err)
	}

	// A store set in code (Redis) is shared.
	app = newTestApp(t, WithSource(testSource(env)), WithCacheStore(cache.NewMemory(0)))
	if _, err := app.RateLimiters(); err != nil {
		t.Fatalf("RateLimiters() with WithCacheStore = %v", err)
	}
}

// Two app instances on one database (two servers) share the budget with
// RATE_LIMIT_STORE=cache and CACHE_STORE=database.
func TestRateLimitersShareTheDatabase(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"RATE_LIMIT_STORE": "cache",
		"CACHE_STORE":      "database",
		"DATABASE_URL":     "file:" + filepath.ToSlash(filepath.Join(testutil.TempDir(t), "rl.db")),
	}

	var allowed int

	for range 2 {
		app, err := New(WithSource(testSource(env)), WithLogger(discardLogger()))
		if err != nil {
			t.Fatal(err)
		}

		t.Cleanup(func() { _ = app.Shutdown(context.Background()) })

		limiters, err := app.RateLimiters()
		if err != nil {
			t.Fatal(err)
		}

		l := limiters("login", 3, time.Hour)

		for range 2 {
			res, err := l.Allow(t.Context(), "ip:192.0.2.1")
			if err != nil {
				t.Fatal(err)
			}

			if res.Allowed {
				allowed++
			}
		}
	}

	// shortcut: a window boundary between the requests would reset the
	// count; a 1h window makes that unlikely enough for a unit test.
	if allowed != 3 {
		t.Fatalf("4 requests across 2 instances, limit 3: %d allowed, want 3", allowed)
	}
}
