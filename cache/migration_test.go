package cache

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/internal/testutil"
	"github.com/AlexGalhardo/vukem/schema"
)

func openSQLite(t *testing.T) *database.DB {
	t.Helper()

	db, err := database.Open(t.Context(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	return db
}

func migrateCache(t *testing.T, db *database.DB) {
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
// manages the cache table with vukem migrate gets the schema the store
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

		want := tableDDL(dl, DefaultTable)
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("%s: migration SQL differs from CreateTable (-CreateTable +migration):\n%s", dl.Name(), diff)
		}
	}
}

// TestMigrationUsesConfiguredTable: CACHE_TABLE reaches the migration, so
// the managed table is the one the store reads.
func TestMigrationUsesConfiguredTable(t *testing.T) {
	t.Parallel()

	got, err := schema.Record(database.SQLite, Migrations("vukem_cache")[0].Up)
	if err != nil {
		t.Fatalf("Record() = %v", err)
	}

	if diff := cmp.Diff(tableDDL(database.SQLite, "vukem_cache"), got); diff != "" {
		t.Errorf("migration SQL (-want +got):\n%s", diff)
	}
}

// TestMigrationSchemaMatchesCreateTable compares SQLite's catalog after
// each path instead of trusting the statements.
func TestMigrationSchemaMatchesCreateTable(t *testing.T) {
	t.Parallel()

	booted := openSQLite(t)

	store, err := NewDatabase(booted, "")
	if err != nil {
		t.Fatal(err)
	}

	if err := store.CreateTable(t.Context()); err != nil {
		t.Fatal(err)
	}

	migrated := openSQLite(t)
	migrateCache(t, migrated)

	want := testutil.SQLiteSchema(t, booted.SQL(), DefaultTable)
	got := testutil.SQLiteSchema(t, migrated.SQL(), DefaultTable)

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("migrated schema differs from CreateTable (-CreateTable +migration):\n%s", diff)
	}
}

// TestMigratedTableStoresEntries: a store pointed at the migrated table
// works without ever creating it.
func TestMigratedTableStoresEntries(t *testing.T) {
	t.Parallel()

	db := openSQLite(t)
	migrateCache(t, db)

	store, err := NewDatabase(db, "")
	if err != nil {
		t.Fatal(err)
	}

	if err := store.Set(t.Context(), "k", []byte("v"), 0); err != nil {
		t.Fatalf("Set() = %v", err)
	}

	v, found, err := store.Get(t.Context(), "k")
	if err != nil || !found || string(v) != "v" {
		t.Fatalf("Get() = %q, %v, %v, want \"v\", true, nil", v, found, err)
	}
}

// TestMigrationAdoptsBootCreatedTable: upgrading an app that already let
// the store create the table is not a conflict.
func TestMigrationAdoptsBootCreatedTable(t *testing.T) {
	t.Parallel()

	db := openSQLite(t)

	store, err := NewDatabase(db, "")
	if err != nil {
		t.Fatal(err)
	}

	if err := store.CreateTable(t.Context()); err != nil {
		t.Fatal(err)
	}

	migrateCache(t, db)

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

// TestMigrationRollbackDropsTable: the cache table can be rolled back
// like any other migration.
func TestMigrationRollbackDropsTable(t *testing.T) {
	t.Parallel()

	db := openSQLite(t)
	migrateCache(t, db)

	m, err := schema.NewMigrator(db, Migrations(""))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := m.Rollback(t.Context(), 0); err != nil {
		t.Fatalf("Rollback() = %v", err)
	}

	var n int
	if err := db.QueryRowContext(t.Context(),
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, DefaultTable).Scan(&n); err != nil {
		t.Fatal(err)
	}

	if n != 0 {
		t.Errorf("table %s still exists after Rollback()", DefaultTable)
	}
}

// TestMigrationRejectsInvalidTableName: an injected table name fails the
// migration instead of reaching the database.
func TestMigrationRejectsInvalidTableName(t *testing.T) {
	t.Parallel()

	m, err := schema.NewMigrator(openSQLite(t), Migrations("cache; DROP TABLE users"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := m.Up(t.Context()); err == nil || !strings.Contains(err.Error(), "invalid table name") {
		t.Fatalf("Up() = %v, want an invalid table name error", err)
	}
}
