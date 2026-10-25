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

func TestMemoryLRUKeepsHeldLock(t *testing.T) {
	t.Parallel()

	m := cache.NewMemory(4)
	c := cache.New(m, "app:")
	ctx := t.Context()

	held := c.NewLock("job", time.Minute)
	if ok, err := held.TryAcquire(ctx); err != nil || !ok {
		t.Fatalf("TryAcquire() = %v, %v, want true, nil", ok, err)
	}

	for i := range 100 {
		_ = c.Put(ctx, "flood:"+strconv.Itoa(i), i, time.Minute)
	}

	if got := m.Len(); got > 4 {
		t.Errorf("Len() after flood = %d, want <= 4", got)
	}

	if ok, err := c.NewLock("job", time.Minute).TryAcquire(ctx); err != nil || ok {
		t.Fatalf("second owner TryAcquire() = %v, %v, want false, nil: the held lock was evicted", ok, err)
	}

	if err := held.Release(ctx); err != nil {
		t.Fatal(err)
	}

	if ok, err := c.NewLock("job", time.Minute).TryAcquire(ctx); err != nil || !ok {
		t.Errorf("TryAcquire() after Release = %v, %v, want true, nil", ok, err)
	}
}

func TestMemoryLockFloodStaysBounded(t *testing.T) {
	t.Parallel()

	m := cache.NewMemory(4)
	c := cache.New(m, "app:")
	ctx := t.Context()

	// Lock names derived from input must not grow the store past max: once
	// held locks fill half of it, new locks fail instead of evicting one.
	acquired := 0

	for i := range 100 {
		ok, err := c.NewLock("lock-"+strconv.Itoa(i), time.Minute).TryAcquire(ctx)
		if err != nil {
			t.Fatal(err)
		}

		if ok {
			acquired++
		}
	}

	if acquired != 2 {
		t.Errorf("locks acquired in a 4-entry store = %d, want 2", acquired)
	}

	if got := m.Len(); got > 4 {
		t.Errorf("Len() after lock flood = %d, want <= 4", got)
	}

	for _, name := range []string{"lock-0", "lock-1"} {
		if ok, _ := c.NewLock(name, time.Minute).TryAcquire(ctx); ok {
			t.Errorf("TryAcquire(%q) by a second owner = true, want the held lock kept", name)
		}
	}

	// A counter written after the flood survives its own insertion.
	if _, err := c.Increment(ctx, "hits", 1, time.Minute); err != nil {
		t.Fatal(err)
	}

	if n, err := c.Increment(ctx, "hits", 1, time.Minute); err != nil || n != 2 {
		t.Errorf("Increment() after lock flood = %d, %v, want 2, nil", n, err)
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
