package stores_test

import (
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/cache"
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/internal/testutil"
	"github.com/AlexGalhardo/vukem/queue"
	"github.com/AlexGalhardo/vukem/schema"
	"github.com/AlexGalhardo/vukem/schema/stores"
	"github.com/AlexGalhardo/vukem/session"
	"github.com/AlexGalhardo/vukem/webhook"
)

func openSQLite(t *testing.T) *database.DB {
	t.Helper()

	db, err := database.Open(t.Context(), "file:"+filepath.Join(testutil.TempDir(t), "s.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	return db
}

func tableNames(t *testing.T, db *database.DB) []string {
	t.Helper()

	rows, err := db.QueryContext(t.Context(),
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var out []string

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}

		out = append(out, name)
	}

	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	return out
}

// TestMigrationsAreTheStoreMigrations: the one call an app makes carries
// every store's own migration, so no framework table is left behind (A84).
func TestMigrationsAreTheStoreMigrations(t *testing.T) {
	t.Parallel()

	var got []string
	for _, m := range stores.Migrations(stores.Options{}) {
		got = append(got, m.ID)
	}

	want := []string{queue.MigrationID, session.MigrationID, cache.MigrationID, webhook.MigrationID}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Migrations() ids (-want +got):\n%s", diff)
	}
}

// TestTablesNamesEveryOwnedTable: an app can print what the framework
// takes before naming its own tables (A83).
func TestTablesNamesEveryOwnedTable(t *testing.T) {
	t.Parallel()

	want := []string{
		queue.DefaultTable, queue.DefaultFailedTable, session.DefaultTable,
		cache.DefaultTable, webhook.DefaultDeliveryTable,
	}
	if diff := cmp.Diff(want, stores.Tables(stores.Options{})); diff != "" {
		t.Errorf("Tables() (-want +got):\n%s", diff)
	}

	o := stores.Options{
		Jobs: "vukem_jobs", FailedJobs: "vukem_failed_jobs", Sessions: "vukem_sessions",
		Cache: "vukem_cache", WebhookDeliveries: "vukem_webhook_deliveries",
	}
	custom := []string{
		"vukem_jobs", "vukem_failed_jobs", "vukem_sessions", "vukem_cache", "vukem_webhook_deliveries",
	}

	if diff := cmp.Diff(custom, stores.Tables(o)); diff != "" {
		t.Errorf("Tables(prefixed) (-want +got):\n%s", diff)
	}
}

// TestMigrateCreatesEveryTable: vukem migrate creates the framework
// tables, so migrate no longer reports "Nothing to migrate." while five
// tables exist that no migration describes.
func TestMigrateCreatesEveryTable(t *testing.T) {
	t.Parallel()

	db := openSQLite(t)

	m, err := schema.NewMigrator(db, stores.Migrations(stores.Options{}))
	if err != nil {
		t.Fatal(err)
	}

	ran, err := m.Up(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if len(ran) != 4 {
		t.Fatalf("Up() ran %d migrations, want 4", len(ran))
	}

	want := []string{"cache", "failed_jobs", "jobs", "migrations", "sessions", "webhook_deliveries"}
	if diff := cmp.Diff(want, tableNames(t, db)); diff != "" {
		t.Errorf("tables after migrate (-want +got):\n%s", diff)
	}

	if _, err := m.Reset(t.Context()); err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff([]string{"migrations"}, tableNames(t, db)); diff != "" {
		t.Errorf("tables after migrate:reset (-want +got):\n%s", diff)
	}
}

// TestMigrateCreatesRenamedTables: the options move every framework table
// out of the way of an app's own tables.
func TestMigrateCreatesRenamedTables(t *testing.T) {
	t.Parallel()

	db := openSQLite(t)

	o := stores.Options{
		Jobs: "vukem_jobs", FailedJobs: "vukem_failed_jobs", Sessions: "vukem_sessions",
		Cache: "vukem_cache", WebhookDeliveries: "vukem_webhook_deliveries",
	}

	m, err := schema.NewMigrator(db, stores.Migrations(o))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := m.Up(t.Context()); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"migrations", "vukem_cache", "vukem_failed_jobs", "vukem_jobs",
		"vukem_sessions", "vukem_webhook_deliveries",
	}
	if diff := cmp.Diff(want, tableNames(t, db)); diff != "" {
		t.Errorf("tables after migrate (-want +got):\n%s", diff)
	}
}
