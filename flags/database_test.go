package flags_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/flags"
	"github.com/AlexGalhardo/vukem/schema"
)

func TestDatabaseStoreOnSQLite(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	db, err := database.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	m, err := schema.NewMigrator(db, []schema.Migration{flags.Migration("1_create_feature_flags_table")})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := m.Up(ctx); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	store := flags.NewDatabaseStore(db, func() time.Time { return now })

	f := newFlags(t, flags.Config{Store: store})

	if _, err := f.Set(ctx, flags.Global, "new_checkout", "true"); err != nil {
		t.Fatal(err)
	}

	// A second write updates the row instead of conflicting.
	if _, err := f.Set(ctx, flags.Global, "upload_limit_mb", "20"); err != nil {
		t.Fatal(err)
	}

	if _, err := f.Set(ctx, flags.Global, "upload_limit_mb", "25"); err != nil {
		t.Fatal(err)
	}

	if _, err := flags.Put(ctx, f, "tenant:9", limit, 99); err != nil {
		t.Fatal(err)
	}

	global, err := store.Load(ctx, flags.Global)
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(map[string]string{"new_checkout": "true", "upload_limit_mb": "25"}, global); diff != "" {
		t.Errorf("Load(global) (-want +got):\n%s", diff)
	}

	if got := flags.Get(flags.WithScope(ctx, "tenant:9"), f, limit); got != 99 {
		t.Errorf("Get(tenant:9) = %d, want 99", got)
	}

	if _, err := f.Reset(ctx, "tenant:9", "upload_limit_mb"); err != nil {
		t.Fatal(err)
	}

	if got := flags.Get(flags.WithScope(ctx, "tenant:9"), f, limit); got != 25 {
		t.Errorf("Get(tenant:9) after Reset = %d, want the global 25", got)
	}

	// Deleting a missing row is not an error.
	if err := store.Delete(ctx, "tenant:9", "upload_limit_mb"); err != nil {
		t.Errorf("Delete(missing) = %v", err)
	}

	var at time.Time
	if err := db.QueryRowContext(ctx, "SELECT updated_at FROM feature_flags WHERE name = 'new_checkout'").
		Scan(&at); err != nil || !at.Equal(now) {
		t.Errorf("updated_at = %v, %v, want %v", at, err, now)
	}

	if _, err := m.Rollback(ctx, 1); err != nil {
		t.Fatalf("migration Down = %v", err)
	}

	// Without the table every read falls back to the default.
	if f.Enabled(ctx, checkout) {
		t.Error("a read without the table did not fall back to the default")
	}
}
