//go:build integration

package mysql

import (
	"context"
	"testing"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/flags"
	"github.com/AlexGalhardo/vukem/schema"
)

// TestFlagsDatabaseStore runs the feature flag migration and the
// ON DUPLICATE KEY upsert on MySQL and MariaDB.
func TestFlagsDatabaseStore(t *testing.T) {
	each(t, func(t *testing.T, db *database.DB) {
		t.Helper()

		ctx := t.Context()
		_, _ = db.ExecContext(ctx, "DROP TABLE IF EXISTS "+flags.Table)
		// t.Context is cancelled before Cleanup runs, so the drop needs its own context.
		t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), "DROP TABLE IF EXISTS "+flags.Table) })

		if err := flags.Migration("1_create_feature_flags_table").Up(schema.New(ctx, db)); err != nil {
			t.Fatal(err)
		}

		limit := flags.Int("upload_limit_mb", 10)

		ff, err := flags.New(flags.Config{Flags: []flags.AnyFlag{limit}, Store: flags.NewDatabaseStore(db, nil)})
		if err != nil {
			t.Fatal(err)
		}

		for _, v := range []int{20, 30} { // the second write updates the row
			if _, err := flags.Put(ctx, ff, flags.Global, limit, v); err != nil {
				t.Fatal(err)
			}
		}

		if _, err := flags.Put(ctx, ff, "tenant:1", limit, 99); err != nil {
			t.Fatal(err)
		}

		if got, scoped := flags.Get(
			ctx,
			ff,
			limit,
		), flags.Get(
			flags.WithScope(ctx, "tenant:1"),
			ff,
			limit,
		); got != 30 ||
			scoped != 99 {
			t.Errorf("Get() = %d, %d, want 30 and the scoped 99", got, scoped)
		}
	})
}
