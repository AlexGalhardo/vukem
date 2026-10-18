package queue

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/schema"
)

// TestMigrationMatchesCreateTablesDDL: the migration and the boot-time
// CreateTables run the same statements on every dialect, so an app that
// manages the queue tables with vukem migrate gets the schema the driver
// expects (A84).
func TestMigrationMatchesCreateTablesDDL(t *testing.T) {
	t.Parallel()

	migs := Migrations(DatabaseOptions{})
	if len(migs) != 1 {
		t.Fatalf("Migrations() = %d migrations, want 1", len(migs))
	}

	for _, dl := range []database.Dialect{database.SQLite, database.Postgres, database.MySQL} {
		got, err := schema.Record(dl, migs[0].Up)
		if err != nil {
			t.Fatalf("Record(%s) = %v", dl.Name(), err)
		}

		want := tableDDL(dl, DefaultTable, DefaultFailedTable)
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("%s: migration SQL differs from CreateTables (-CreateTables +migration):\n%s", dl.Name(), diff)
		}
	}
}

// TestMigrationUsesConfiguredTables: QUEUE_TABLE and QUEUE_FAILED_TABLE
// reach the migration, so the managed tables are the ones the driver reads.
func TestMigrationUsesConfiguredTables(t *testing.T) {
	t.Parallel()

	migs := Migrations(DatabaseOptions{Table: "vukem_jobs", FailedTable: "vukem_failed_jobs"})

	got, err := schema.Record(database.SQLite, migs[0].Up)
	if err != nil {
		t.Fatalf("Record() = %v", err)
	}

	want := tableDDL(database.SQLite, "vukem_jobs", "vukem_failed_jobs")
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("migration SQL (-want +got):\n%s", diff)
	}
}
