//go:build integration

package postgres

import (
	"os"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/database/dbtest"
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
