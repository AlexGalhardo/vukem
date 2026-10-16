//go:build integration

package mysql

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/cache"
	"github.com/AlexGalhardo/vukem/cache/cachetest"
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/database/dbtest"
	"github.com/AlexGalhardo/vukem/session"
)

// servers are the databases under test: MYSQL_URL and MARIADB_URL, by
// default the compose services (task services:up) on :53306 and :53307.
func servers() map[string]string {
	urls := map[string]string{
		"MySQL":   os.Getenv("MYSQL_URL"),
		"MariaDB": os.Getenv("MARIADB_URL"),
	}

	if urls["MySQL"] == "" {
		urls["MySQL"] = "mysql://vukem:vukem@localhost:53306/vukem_test"
	}

	if urls["MariaDB"] == "" {
		urls["MariaDB"] = "mariadb://vukem:vukem@localhost:53307/vukem_test"
	}

	return urls
}

// each runs fn as a subtest against every server.
func each(t *testing.T, fn func(t *testing.T, db *database.DB)) {
	t.Helper()

	for name, url := range servers() {
		t.Run(name, func(t *testing.T) {
			db, err := database.Open(t.Context(), url, database.WithDriver(Driver()))
			if err != nil {
				t.Fatalf("connect: %v (run task services:up)", err)
			}

			t.Cleanup(func() { _ = db.Close() })

			fn(t, db)
		})
	}
}

func TestConformance(t *testing.T) {
	each(t, func(t *testing.T, db *database.DB) {
		t.Helper()
		dbtest.Run(t, db)
	})
}

func TestSessionStore(t *testing.T) {
	each(t, testSessionStore)
}

func testSessionStore(t *testing.T, db *database.DB) {
	t.Helper()

	store, err := session.NewSQLStore(db.SQL(), session.WithTable("dbt_sessions"), session.WithMySQL())
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()
	_, _ = db.ExecContext(ctx, "DROP TABLE IF EXISTS dbt_sessions")
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), "DROP TABLE IF EXISTS dbt_sessions") })

	if err := store.CreateTable(ctx); err != nil {
		t.Fatal(err)
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

	// Ids compare exactly, not with MySQL's case-insensitive collations.
	if _, ok, _ := store.Load(ctx, "S1"); ok {
		t.Fatal("session id matched case-insensitively")
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
	each(t, func(t *testing.T, db *database.DB) {
		t.Helper()

		store, err := cache.NewDatabase(db, "dbt_cache")
		if err != nil {
			t.Fatal(err)
		}

		_, _ = db.ExecContext(t.Context(), "DROP TABLE IF EXISTS dbt_cache")
		t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), "DROP TABLE IF EXISTS dbt_cache") })

		if err := store.CreateTable(t.Context()); err != nil {
			t.Fatal(err)
		}

		cachetest.Run(t, store)
	})
}
