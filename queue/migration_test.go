package queue_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/internal/testutil"
	"github.com/AlexGalhardo/vukem/queue"
	"github.com/AlexGalhardo/vukem/schema"
)

func migrateQueue(t *testing.T, db *database.DB) {
	t.Helper()

	m, err := schema.NewMigrator(db, queue.Migrations(queue.DatabaseOptions{}))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := m.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// TestMigrationSchemaMatchesCreateTables: the tables the migration creates
// are the tables CreateTables creates, read from SQLite's catalog rather
// than from the statements that built them (A84).
func TestMigrationSchemaMatchesCreateTables(t *testing.T) {
	t.Parallel()

	booted := openSQLite(t)
	newDatabaseDriver(t, booted, queue.DatabaseOptions{})

	migrated := openSQLite(t)
	migrateQueue(t, migrated)

	tables := []string{queue.DefaultTable, queue.DefaultFailedTable}
	want := testutil.SQLiteSchema(t, booted.SQL(), tables...)
	got := testutil.SQLiteSchema(t, migrated.SQL(), tables...)

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("migrated schema differs from CreateTables (-CreateTables +migration):\n%s", diff)
	}
}

// TestMigratedTablesRunTheQueue: a driver pointed at migrated tables works
// without ever creating them, so an app can boot a least-privilege
// database user once the migration ran.
func TestMigratedTablesRunTheQueue(t *testing.T) {
	t.Parallel()

	db := openSQLite(t)
	migrateQueue(t, db)

	d, err := queue.NewDatabase(db, queue.DatabaseOptions{})
	if err != nil {
		t.Fatal(err)
	}

	m := &queue.Message{ID: "m1", Queue: "default", Kind: "k", Payload: []byte(`{}`)}
	if err := d.Push(t.Context(), m); err != nil {
		t.Fatalf("Push() = %v", err)
	}

	got, err := d.Pop(t.Context(), []string{"default"})
	if err != nil || got == nil {
		t.Fatalf("Pop() = %v, %v, want the pushed job", got, err)
	}

	if got.ID != "m1" {
		t.Errorf("Pop().ID = %q, want %q", got.ID, "m1")
	}
}

// TestMigrationAdoptsBootCreatedTables: upgrading an app that already let
// the driver create the tables is not a conflict — migrate records the
// migration and leaves the existing tables alone.
func TestMigrationAdoptsBootCreatedTables(t *testing.T) {
	t.Parallel()

	db := openSQLite(t)
	newDatabaseDriver(t, db, queue.DatabaseOptions{})
	migrateQueue(t, db)

	m, err := schema.NewMigrator(db, queue.Migrations(queue.DatabaseOptions{}))
	if err != nil {
		t.Fatal(err)
	}

	st, err := m.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if len(st) != 1 || !st[0].Ran || st[0].Changed {
		t.Fatalf("Status() = %+v, want one applied, unchanged migration", st)
	}
}

// TestMigrationRollbackDropsTables: the framework tables can be rolled
// back like any other migration.
func TestMigrationRollbackDropsTables(t *testing.T) {
	t.Parallel()

	db := openSQLite(t)
	migrateQueue(t, db)

	m, err := schema.NewMigrator(db, queue.Migrations(queue.DatabaseOptions{}))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := m.Rollback(t.Context(), 0); err != nil {
		t.Fatalf("Rollback() = %v", err)
	}

	for _, table := range []string{queue.DefaultTable, queue.DefaultFailedTable} {
		var n int
		if err := db.QueryRowContext(t.Context(),
			`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&n); err != nil {
			t.Fatal(err)
		}

		if n != 0 {
			t.Errorf("table %s still exists after Rollback()", table)
		}
	}
}

// TestMigrationRejectsInvalidTableName: an injected table name fails the
// migration instead of reaching the database.
func TestMigrationRejectsInvalidTableName(t *testing.T) {
	t.Parallel()

	db := openSQLite(t)

	m, err := schema.NewMigrator(db, queue.Migrations(queue.DatabaseOptions{Table: "jobs; DROP TABLE users"}))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := m.Up(t.Context()); err == nil || !strings.Contains(err.Error(), "invalid table name") {
		t.Fatalf("Up() = %v, want an invalid table name error", err)
	}
}
