package vukem

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/cache"
	"github.com/AlexGalhardo/vukem/events"
	"github.com/AlexGalhardo/vukem/internal/testutil"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/security"
	"github.com/AlexGalhardo/vukem/web"
)

type orderPlaced struct{ ID int }

func TestCacheFromConfig(t *testing.T) {
	t.Parallel()

	dbURL := "file:" + filepath.ToSlash(filepath.Join(testutil.TempDir(t), "c.db"))

	for store, wantErr := range map[string]bool{"memory": false, "database": false, "redis": true} {
		app, err := New(WithSource(testSource(map[string]string{"CACHE_STORE": store, "DATABASE_URL": dbURL})),
			WithLogger(discardLogger()))
		if err != nil {
			t.Fatal(err)
		}

		t.Cleanup(func() { _ = app.Shutdown(context.Background()) })

		c, err := app.Cache()
		if (err != nil) != wantErr {
			t.Fatalf("CACHE_STORE=%s: %v", store, err)
		}

		if err != nil {
			continue
		}

		if err := c.Put(t.Context(), "k", 1, time.Minute); err != nil {
			t.Fatal(err)
		}

		if v, ok, _ := cache.Get[int](t.Context(), c, "k"); !ok || v != 1 {
			t.Fatalf("%s: get = %v %v", store, v, ok)
		}
	}
}

func TestEventsWiring(t *testing.T) {
	t.Parallel()

	var got []int

	app, err := New(WithSource(testSource(nil)), WithLogger(discardLogger()),
		WithListeners(events.On(func(_ context.Context, e orderPlaced) error { got = append(got, e.ID); return nil })))
	if err != nil {
		t.Fatal(err)
	}

	if err := app.Events.Dispatch(t.Context(), orderPlaced{ID: 7}); err != nil || len(got) != 1 || got[0] != 7 {
		t.Fatalf("dispatch = %v %v", got, err)
	}

	if err := app.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}

	if err := app.Events.Dispatch(t.Context(), orderPlaced{}); !errors.Is(err, events.ErrClosed) {
		t.Fatalf("after shutdown = %v", err)
	}
}

func TestSecurityEventsReachTheBus(t *testing.T) {
	t.Parallel()

	var got []security.Event

	app := newTestApp(t, WithListeners(events.On(func(_ context.Context, e security.Event) error {
		got = append(got, e)
		return nil
	})), WithRoutes(func(r *router.Router, _ *App) error {
		r.Get("/deny", func(c *web.Ctx) error { return c.Authorize("anything", nil) })
		return nil
	}))

	if rec := doReq(t, app, "/deny"); rec.Code != 403 {
		t.Fatalf("status = %d", rec.Code)
	}

	if len(got) != 1 || got[0].Name != "authz.denied" || got[0].Attrs["ability"] != "anything" ||
		got[0].Path != "/deny" {
		t.Fatalf("events = %+v", got)
	}
}

// Database cache rows (rate limit counters among them) stay until pruned:
// the app schedules cache:prune hourly when the cache keeps expired rows.
func TestDatabaseCacheIsPrunedOnSchedule(t *testing.T) {
	t.Parallel()

	dbURL := "file:" + filepath.ToSlash(filepath.Join(testutil.TempDir(t), "c.db"))

	newApp := func(store string) *App {
		app, err := New(WithSource(testSource(map[string]string{"CACHE_STORE": store, "DATABASE_URL": dbURL})),
			WithLogger(discardLogger()))
		if err != nil {
			t.Fatal(err)
		}

		t.Cleanup(func() { _ = app.Shutdown(context.Background()) })

		return app
	}

	scheduled := func(app *App) string {
		s, err := app.Schedule()
		if err != nil {
			t.Fatal(err)
		}

		for _, tk := range s.Tasks() {
			if tk.Name() == "cache:prune" {
				return tk.Expression()
			}
		}

		return ""
	}

	app := newApp("database")
	if got := scheduled(app); got != "0 * * * *" {
		t.Fatalf("cache:prune schedule = %q, want hourly", got)
	}

	c, err := app.Cache()
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()
	if err := c.Store().Set(ctx, "old", []byte("1"), time.Millisecond); err != nil {
		t.Fatal(err)
	}

	if err := c.Store().Set(ctx, "live", []byte("1"), time.Hour); err != nil {
		t.Fatal(err)
	}

	time.Sleep(5 * time.Millisecond) //nolint:forbidigo // real expiry in the database

	if err := app.Call(ctx, "cache:prune", nil); err != nil {
		t.Fatalf("cache:prune = %v", err)
	}

	db, err := app.DB()
	if err != nil {
		t.Fatal(err)
	}

	var n int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM cache").Scan(&n); err != nil || n != 1 {
		t.Fatalf("cache rows after prune = %d, %v, want 1", n, err)
	}

	if got := scheduled(newApp("memory")); got != "" {
		t.Fatalf("cache:prune scheduled for the memory cache (%q)", got)
	}
}
