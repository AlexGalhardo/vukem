package cache_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/cache"
	"github.com/AlexGalhardo/vukem/cache/cachetest"
	"github.com/AlexGalhardo/vukem/database"
)

func TestMemoryConformance(t *testing.T) {
	t.Parallel()
	cachetest.Run(t, cache.NewMemory(0))
}

func TestDatabaseConformance(t *testing.T) {
	t.Parallel()

	db, err := database.Open(t.Context(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	store, err := cache.NewDatabase(db, "")
	if err != nil {
		t.Fatal(err)
	}

	if err := store.CreateTable(t.Context()); err != nil {
		t.Fatal(err)
	}

	cachetest.Run(t, store)

	_ = store.Set(t.Context(), "old", []byte("1"), time.Millisecond)
	time.Sleep(5 * time.Millisecond) //nolint:forbidigo // real expiry

	if n, err := store.Prune(t.Context()); err != nil || n != 1 {
		t.Fatalf("Prune = %d %v", n, err)
	}

	if _, err := cache.NewDatabase(db, "bad name;"); err == nil {
		t.Fatal("invalid table accepted")
	}
}

func TestMemoryLRU(t *testing.T) {
	t.Parallel()

	m := cache.NewMemory(3)
	ctx := context.Background()

	for i := range 3 {
		_ = m.Set(ctx, strconv.Itoa(i), []byte("v"), 0)
	}

	_, _, _ = m.Get(ctx, "0") // 0 is now the most recently used
	_ = m.Set(ctx, "3", []byte("v"), 0)

	if m.Len() != 3 {
		t.Fatalf("Len = %d", m.Len())
	}

	if _, ok, _ := m.Get(ctx, "1"); ok {
		t.Fatal("least recently used entry kept")
	}

	if _, ok, _ := m.Get(ctx, "0"); !ok {
		t.Fatal("recently used entry evicted")
	}

	// Values are copied in and out.
	buf := []byte("abc")
	_ = m.Set(ctx, "copy", buf, 0)
	buf[0] = 'x'

	if v, _, _ := m.Get(ctx, "copy"); string(v) != "abc" {
		t.Fatalf("stored value aliased the caller's slice: %q", v)
	}
}

func TestRememberCancelledWaiter(t *testing.T) {
	t.Parallel()

	c := cache.New(cache.NewMemory(0), "")
	release := make(chan struct{})
	started := make(chan struct{})

	go func() {
		_, _ = cache.Remember(context.Background(), c, "slow", time.Minute, func(context.Context) (int, error) {
			close(started)
			<-release

			return 1, nil
		})
	}()

	<-started

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := cache.Remember(
		ctx,
		c,
		"slow",
		time.Minute,
		func(context.Context) (int, error) { return 2, nil },
	); err == nil {
		t.Fatal("cancelled waiter did not return")
	}

	close(release)

	if c.Store() == nil {
		t.Fatal("Store")
	}
}

func openSQLiteStore(t *testing.T) (*database.DB, *cache.Database) {
	t.Helper()

	db, err := database.Open(t.Context(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	store, err := cache.NewDatabase(db, "")
	if err != nil {
		t.Fatal(err)
	}

	return db, store
}

// Prune scans expires_at: CreateTable indexes it, and running it again
// (every boot does) must not fail.
func TestDatabaseCreateTableIndexesExpiry(t *testing.T) {
	t.Parallel()

	db, store := openSQLiteStore(t)

	for range 2 {
		if err := store.CreateTable(t.Context()); err != nil {
			t.Fatal(err)
		}
	}

	var n int
	if err := db.QueryRowContext(t.Context(),
		`SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = 'cache_expires_at'`).Scan(&n); err != nil ||
		n != 1 {
		t.Fatalf("cache_expires_at indexes = %d, %v, want 1", n, err)
	}
}

// Prune deletes in batches so a large backlog does not hold one long lock.
func TestDatabasePrunesInBatches(t *testing.T) {
	t.Parallel()

	_, store := openSQLiteStore(t)
	if err := store.CreateTable(t.Context()); err != nil {
		t.Fatal(err)
	}

	cache.SetPruneBatch(store, 2)

	ctx := t.Context()
	for i := range 5 {
		_ = store.Set(ctx, "old"+strconv.Itoa(i), []byte("1"), time.Millisecond)
	}

	_ = store.Set(ctx, "live", []byte("1"), time.Hour)

	time.Sleep(5 * time.Millisecond) //nolint:forbidigo // real expiry

	if n, err := store.Prune(ctx); err != nil || n != 5 {
		t.Fatalf("Prune = %d %v, want 5", n, err)
	}

	if _, ok, _ := store.Get(ctx, "live"); !ok {
		t.Fatal("Prune deleted a live entry")
	}
}
