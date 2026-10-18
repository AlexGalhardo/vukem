package webhook

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/internal/testutil"
	"github.com/AlexGalhardo/vukem/schema"
)

func openLogDB(t *testing.T) *database.DB {
	t.Helper()

	db, err := database.Open(t.Context(), "file:"+filepath.Join(testutil.TempDir(t), "wl.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	return db
}

func migrateLog(t *testing.T, db *database.DB) {
	t.Helper()

	m, err := schema.NewMigrator(db, Migrations(""))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := m.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// TestMigrationMatchesCreateTableDDL: the migration and the boot-time
// CreateTable run the same statements on every dialect, so an app that
// manages the delivery log with vukem migrate gets the schema the log
// expects (A84).
func TestMigrationMatchesCreateTableDDL(t *testing.T) {
	t.Parallel()

	migs := Migrations("")
	if len(migs) != 1 {
		t.Fatalf("Migrations() = %d migrations, want 1", len(migs))
	}

	for _, dl := range []database.Dialect{database.SQLite, database.Postgres, database.MySQL} {
		got, err := schema.Record(dl, migs[0].Up)
		if err != nil {
			t.Fatalf("Record(%s) = %v", dl.Name(), err)
		}

		want := tableDDL(dl, DefaultDeliveryTable)
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("%s: migration SQL differs from CreateTable (-CreateTable +migration):\n%s", dl.Name(), diff)
		}
	}
}

// TestMigrationUsesConfiguredTable: a log on another table migrates that
// table, so the managed table is the one the log writes to.
func TestMigrationUsesConfiguredTable(t *testing.T) {
	t.Parallel()

	got, err := schema.Record(database.SQLite, Migrations("deliveries")[0].Up)
	if err != nil {
		t.Fatalf("Record() = %v", err)
	}

	if diff := cmp.Diff(tableDDL(database.SQLite, "deliveries"), got); diff != "" {
		t.Errorf("migration SQL (-want +got):\n%s", diff)
	}
}

// TestMigrationSchemaMatchesCreateTable compares SQLite's catalog after
// each path instead of trusting the statements.
func TestMigrationSchemaMatchesCreateTable(t *testing.T) {
	t.Parallel()

	booted := openLogDB(t)

	l, err := NewSQLDeliveryLog(booted, "")
	if err != nil {
		t.Fatal(err)
	}

	if err := l.CreateTable(t.Context()); err != nil {
		t.Fatal(err)
	}

	migrated := openLogDB(t)
	migrateLog(t, migrated)

	want := testutil.SQLiteSchema(t, booted.SQL(), DefaultDeliveryTable)
	got := testutil.SQLiteSchema(t, migrated.SQL(), DefaultDeliveryTable)

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("migrated schema differs from CreateTable (-CreateTable +migration):\n%s", diff)
	}
}

// TestMigratedTableRecordsAttempts: a log pointed at the migrated table
// works without ever creating it.
func TestMigratedTableRecordsAttempts(t *testing.T) {
	t.Parallel()

	db := openLogDB(t)
	migrateLog(t, db)

	l, err := NewSQLDeliveryLog(db, "")
	if err != nil {
		t.Fatal(err)
	}

	at := time.UnixMilli(1_800_000_000_000).UTC()
	if err := l.Record(t.Context(), Attempt{Endpoint: "seller-1", EventID: "msg_1", Status: 204, At: at}); err != nil {
		t.Fatalf("Record() = %v", err)
	}

	got, err := l.Recent(t.Context(), "seller-1", 10)
	if err != nil || len(got) != 1 || got[0].EventID != "msg_1" {
		t.Fatalf("Recent() = %+v, %v, want the recorded attempt", got, err)
	}
}

// TestMigrationAdoptsBootCreatedTable: upgrading an app that already let
// the log create the table is not a conflict.
func TestMigrationAdoptsBootCreatedTable(t *testing.T) {
	t.Parallel()

	db := openLogDB(t)

	l, err := NewSQLDeliveryLog(db, "")
	if err != nil {
		t.Fatal(err)
	}

	if err := l.CreateTable(t.Context()); err != nil {
		t.Fatal(err)
	}

	migrateLog(t, db)

	m, err := schema.NewMigrator(db, Migrations(""))
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

// TestMigrationRollbackDropsTable: the delivery log can be rolled back
// like any other migration.
func TestMigrationRollbackDropsTable(t *testing.T) {
	t.Parallel()

	db := openLogDB(t)
	migrateLog(t, db)

	m, err := schema.NewMigrator(db, Migrations(""))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := m.Rollback(t.Context(), 0); err != nil {
		t.Fatalf("Rollback() = %v", err)
	}

	var n int
	if err := db.QueryRowContext(t.Context(),
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`,
		DefaultDeliveryTable).Scan(&n); err != nil {
		t.Fatal(err)
	}

	if n != 0 {
		t.Errorf("table %s still exists after Rollback()", DefaultDeliveryTable)
	}
}

// TestMigrationRejectsInvalidTableName: an injected or over-long table
// name fails the migration instead of reaching the database.
func TestMigrationRejectsInvalidTableName(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"deliveries; DROP TABLE users", strings.Repeat("t", 52)} {
		m, err := schema.NewMigrator(openLogDB(t), Migrations(name))
		if err != nil {
			t.Fatal(err)
		}

		if _, err := m.Up(t.Context()); err == nil || !strings.Contains(err.Error(), "invalid table name") {
			t.Fatalf("Up() with table %q = %v, want an invalid table name error", name, err)
		}
	}
}
