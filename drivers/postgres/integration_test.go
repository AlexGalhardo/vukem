//go:build integration

package postgres

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/cache"
	"github.com/AlexGalhardo/vukem/cache/cachetest"
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/database/dbtest"
	"github.com/AlexGalhardo/vukem/ratelimit"
	"github.com/AlexGalhardo/vukem/session"
)

// open connects to POSTGRES_URL (task services:up starts one on :55432).
func open(t *testing.T) *database.DB {
	t.Helper()

	url := os.Getenv("POSTGRES_URL")
	if url == "" {
		url = "postgres://vukem:vukem@localhost:55432/vukem_test?sslmode=disable"
	}

	db, err := database.Open(t.Context(), url, database.WithDriver(Driver()))
	if err != nil {
		t.Fatalf("connect: %v (run task services:up)", err)
	}

	t.Cleanup(func() { _ = db.Close() })

	return db
}

func TestConformance(t *testing.T) {
	dbtest.Run(t, open(t))
}

func TestSessionStore(t *testing.T) {
	db := open(t)

	store, err := session.NewSQLStore(db.SQL(), session.WithTable("dbt_sessions"), session.WithDollarPlaceholders())
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()
	_, _ = db.ExecContext(ctx, "DROP TABLE IF EXISTS dbt_sessions")
	t.Cleanup(func() { _, _ = db.ExecContext(t.Context(), "DROP TABLE IF EXISTS dbt_sessions") })

	// The pre-v0.24 layout (clear ids, no user_id) is upgraded in place.
	if _, err := db.ExecContext(
		ctx,
		"CREATE TABLE dbt_sessions (id TEXT PRIMARY KEY, data TEXT NOT NULL, expires_at BIGINT NOT NULL)",
	); err != nil {
		t.Fatal(err)
	}

	const legacy = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	if _, err := db.ExecContext(ctx, "INSERT INTO dbt_sessions (id, data, expires_at) VALUES ($1, $2, $3)",
		legacy, `{"l":1}`, time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}

	for range 2 { // idempotent
		if err := store.CreateTable(ctx); err != nil {
			t.Fatalf("CreateTable() on legacy table = %v", err)
		}
	}

	for range 2 { // the first load re-keys the row
		if data, ok, err := store.Load(ctx, legacy); err != nil || !ok || string(data) != `{"l":1}` {
			t.Fatalf("Load(legacy) = %q %v %v", data, ok, err)
		}
	}

	exp := time.Now().Add(time.Hour)
	for id, user := range map[string]string{"u1a": "1", "u1b": "1", "u2": "2"} {
		if err := store.SaveUser(ctx, id, user, []byte("{}"), exp); err != nil {
			t.Fatal(err)
		}
	}

	if err := store.RevokeOthers(ctx, "1", "u1a"); err != nil {
		t.Fatal(err)
	}

	for id, want := range map[string]bool{"u1a": true, "u1b": false, "u2": true} {
		if _, ok, err := store.Load(ctx, id); err != nil || ok != want {
			t.Fatalf("Load(%s) after RevokeOthers = %v %v, want %v", id, ok, err, want)
		}
	}

	if err := store.Save(ctx, "s1", []byte(`{"a":1}`), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	if err := store.Save(ctx, "s1", []byte(`{"a":2}`), time.Now().Add(time.Hour)); err != nil {
		t.Fatal("upsert:", err)
	}

	data, ok, err := store.Load(ctx, "s1")
	if err != nil || !ok || string(data) != `{"a":2}` {
		t.Fatalf("Load = %q %v %v", data, ok, err)
	}

	if err := store.Save(ctx, "old", []byte("{}"), time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}

	if _, ok, _ := store.Load(ctx, "old"); ok {
		t.Fatal("expired session loaded")
	}

	if _, err := store.Prune(ctx); err != nil {
		t.Fatal(err)
	}

	if err := store.Delete(ctx, "s1"); err != nil {
		t.Fatal(err)
	}

	if _, ok, _ := store.Load(ctx, "s1"); ok {
		t.Fatal("deleted session loaded")
	}
}

func TestCacheStore(t *testing.T) {
	db := open(t)

	store, err := cache.NewDatabase(db, "dbt_cache")
	if err != nil {
		t.Fatal(err)
	}

	_, _ = db.ExecContext(t.Context(), "DROP TABLE IF EXISTS dbt_cache")
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), "DROP TABLE IF EXISTS dbt_cache") })

	// Twice: every boot runs it, the expires_at index included.
	for range 2 {
		if err := store.CreateTable(t.Context()); err != nil {
			t.Fatal(err)
		}
	}

	cachetest.Run(t, store)

	if _, err := store.Prune(t.Context()); err != nil {
		t.Fatalf("Prune() = %v", err)
	}
}

// Two connection pools stand for two app servers sharing one Postgres:
// concurrent requests must never exceed the limit between them.
func TestRateLimitWindowSharedAcrossServers(t *testing.T) {
	ctx := t.Context()

	stores := make([]*cache.Database, 2)

	for i := range stores {
		store, err := cache.NewDatabase(open(t), "dbt_ratelimit")
		if err != nil {
			t.Fatal(err)
		}

		stores[i] = store
	}

	db := open(t)
	_, _ = db.ExecContext(ctx, "DROP TABLE IF EXISTS dbt_ratelimit")
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), "DROP TABLE IF EXISTS dbt_ratelimit") })

	if err := stores[0].CreateTable(ctx); err != nil {
		t.Fatal(err)
	}

	const limit, requests = 10, 40

	var (
		wg      sync.WaitGroup
		allowed atomic.Int32
		failed  atomic.Int32
	)

	for i := range requests {
		l := ratelimit.NewWindow(stores[i%2], "login", limit, time.Hour)

		wg.Go(func() {
			res, err := l.Allow(ctx, "ip:192.0.2.1")
			switch {
			case err != nil:
				failed.Add(1)
			case res.Allowed:
				allowed.Add(1)
			}
		})
	}

	wg.Wait()

	if allowed.Load() != limit || failed.Load() != 0 {
		t.Fatalf("%d concurrent requests on 2 servers, limit %d: %d allowed, %d errors",
			requests, limit, allowed.Load(), failed.Load())
	}
}

func TestCacheStoreUpgradesTextValueColumn(t *testing.T) {
	db := open(t)
	ctx := t.Context()

	_, _ = db.ExecContext(ctx, "DROP TABLE IF EXISTS dbt_cache_legacy")
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), "DROP TABLE IF EXISTS dbt_cache_legacy") })

	// The pre-v0.22 layout, with a live row.
	if _, err := db.ExecContext(ctx, `CREATE TABLE dbt_cache_legacy (key TEXT PRIMARY KEY, value TEXT NOT NULL,
		expires_at BIGINT NOT NULL DEFAULT 0)`); err != nil {
		t.Fatal(err)
	}

	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO dbt_cache_legacy (key, value) VALUES ('old', '{"n":1}')`,
	); err != nil {
		t.Fatal(err)
	}

	store, err := cache.NewDatabase(db, "dbt_cache_legacy")
	if err != nil {
		t.Fatal(err)
	}

	for range 2 { // idempotent
		if err := store.CreateTable(ctx); err != nil {
			t.Fatalf("CreateTable() on legacy table = %v", err)
		}
	}

	if got, ok, err := store.Get(ctx, "old"); err != nil || !ok || string(got) != `{"n":1}` {
		t.Fatalf("Get(old) after upgrade = %q %v %v", got, ok, err)
	}

	if err := store.Set(ctx, "bin", []byte{0, 0xff}, time.Minute); err != nil {
		t.Fatalf("Set(binary) after upgrade = %v", err)
	}
}
