package vukem

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/cache"
	"github.com/AlexGalhardo/vukem/queue"
	"github.com/AlexGalhardo/vukem/session"
)

func TestWithStores(t *testing.T) {
	t.Parallel()

	store := cache.NewMemory(10)
	app := newTestApp(t, WithCacheStore(store), WithSessionStore(session.NewMemoryStore()),
		WithSource(testSource(map[string]string{"CACHE_STORE": "unknown-is-ignored", "SESSION_DRIVER": "nope"})))

	c, err := app.Cache()
	if err != nil {
		t.Fatal(err)
	}

	if err := c.Put(t.Context(), "k", 1, 0); err != nil {
		t.Fatal(err)
	}

	if _, ok, _ := store.Get(t.Context(), "vukem:k"); !ok {
		t.Error("cache did not use the given store")
	}

	if _, err := app.Sessions(); err != nil {
		t.Errorf("sessions with a given store = %v", err)
	}
}

func TestLazyStoreFuncs(t *testing.T) {
	t.Parallel()

	var calls []string

	store := cache.NewMemory(10)
	fake := queue.NewFake()

	app := newTestApp(t,
		WithSource(testSource(map[string]string{"REDIS_URL": "redis://from-config"})),
		WithCacheStoreFunc(func(app *App) (cache.Store, error) {
			calls = append(calls, "cache:"+app.Source.Get("REDIS_URL"))
			return store, nil
		}),
		WithSessionStoreFunc(func(app *App) (session.Store, error) {
			calls = append(calls, "session:"+app.Source.Get("REDIS_URL"))
			return session.NewMemoryStore(), nil
		}),
		WithQueueDriverFunc(func(app *App) (queue.Driver, error) {
			calls = append(calls, "queue:"+app.Source.Get("REDIS_URL"))
			return fake, nil
		}),
	)

	if len(calls) != 0 {
		t.Fatalf("store funcs ran during New: %v, want lazy", calls)
	}

	for range 2 {
		if _, err := app.Cache(); err != nil {
			t.Fatal(err)
		}

		if _, err := app.Sessions(); err != nil {
			t.Fatal(err)
		}

		if _, err := app.Queue(); err != nil {
			t.Fatal(err)
		}
	}

	want := []string{"cache:redis://from-config", "session:redis://from-config", "queue:redis://from-config"}
	if diff := cmp.Diff(want, calls); diff != "" {
		t.Errorf("store func calls (-want +got):\n%s", diff)
	}

	shared, err := app.cacheShared()
	if err != nil || !shared {
		t.Errorf("cacheShared() = %v, %v, want true (a custom store is shared)", shared, err)
	}
}

func TestLazyStoreFuncErrors(t *testing.T) {
	t.Parallel()

	boom := errors.New("redis down")
	app := newTestApp(t,
		WithCacheStoreFunc(func(*App) (cache.Store, error) { return nil, boom }),
		WithSessionStoreFunc(func(*App) (session.Store, error) { return nil, boom }),
		WithQueueDriverFunc(func(*App) (queue.Driver, error) { return nil, boom }),
	)

	if _, err := app.Cache(); !errors.Is(err, boom) {
		t.Errorf("Cache() error = %v, want %v", err, boom)
	}

	if _, err := app.Sessions(); !errors.Is(err, boom) {
		t.Errorf("Sessions() error = %v, want %v", err, boom)
	}

	if _, err := app.Queue(); !errors.Is(err, boom) {
		t.Errorf("Queue() error = %v, want %v", err, boom)
	}
}

func TestStoreOptionsLastWins(t *testing.T) {
	t.Parallel()

	eager := cache.NewMemory(10)
	app := newTestApp(t,
		WithCacheStoreFunc(func(*App) (cache.Store, error) { return nil, errors.New("replaced") }),
		WithCacheStore(eager),
	)

	if _, err := app.Cache(); err != nil {
		t.Fatalf("Cache() = %v, want the later WithCacheStore to win", err)
	}
}
