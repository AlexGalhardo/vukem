//go:build integration

package turso_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/drivers/turso"
)

// TestSyncBetweenTwoReplicas needs a Turso Cloud database of its own:
// TURSO_TEST_DATABASE_URL and TURSO_TEST_AUTH_TOKEN. It writes on one local
// replica and reads on another.
func TestSyncBetweenTwoReplicas(t *testing.T) {
	remote, token := os.Getenv("TURSO_TEST_DATABASE_URL"), os.Getenv("TURSO_TEST_AUTH_TOKEN")
	if remote == "" || token == "" {
		t.Skip("set TURSO_TEST_DATABASE_URL and TURSO_TEST_AUTH_TOKEN to run against Turso Cloud")
	}

	open := func(name string) (*database.DB, *turso.Database) {
		td := turso.New(turso.Options{RemoteURL: remote, AuthToken: token, SyncInterval: -1})

		path := filepath.ToSlash(filepath.Join(t.TempDir(), name+".db"))

		db, err := database.Open(t.Context(), "turso://"+path, database.WithDriver(td.Driver()))
		if err != nil {
			t.Fatal(err)
		}

		t.Cleanup(func() { _ = db.Close() })

		return db, td
	}

	first, firstSync := open("first")
	second, secondSync := open("second")

	if _, err := first.ExecContext(
		t.Context(),
		"CREATE TABLE IF NOT EXISTS sync_probe (id INTEGER PRIMARY KEY, note TEXT)",
	); err != nil {
		t.Fatal(err)
	}

	if _, err := first.ExecContext(
		t.Context(),
		"INSERT INTO sync_probe (note) VALUES ('from the first replica')",
	); err != nil {
		t.Fatal(err)
	}

	if err := firstSync.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}

	if err := secondSync.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}

	var note string
	if err := second.QueryRowContext(t.Context(), "SELECT note FROM sync_probe ORDER BY id DESC LIMIT 1").
		Scan(&note); err != nil {
		t.Fatalf("the second replica did not receive the row: %v", err)
	}

	if note != "from the first replica" {
		t.Errorf("note = %q", note)
	}
}
