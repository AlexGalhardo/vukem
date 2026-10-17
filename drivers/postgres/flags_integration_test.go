//go:build integration

package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/cache"
	"github.com/AlexGalhardo/vukem/flags"
	"github.com/AlexGalhardo/vukem/schema"
)

// TestFlagsDatabaseStore runs the feature flag migration, upserts and
// scoped reads on PostgreSQL.
func TestFlagsDatabaseStore(t *testing.T) {
	db := open(t)
	ctx := t.Context()

	drop := func(ctx context.Context) { _, _ = db.ExecContext(ctx, "DROP TABLE IF EXISTS "+flags.Table) }
	drop(ctx)
	// t.Context is cancelled before Cleanup runs, so the drop needs its own context.
	t.Cleanup(func() { drop(context.Background()) })

	if err := flags.Migration("1_create_feature_flags_table").Up(schema.New(ctx, db)); err != nil {
		t.Fatal(err)
	}

	beta := flags.Bool("beta", false)
	limit := flags.Int("upload_limit_mb", 10)
	locked := flags.Bool("admin_two_factor", true).Lock()

	ff, err := flags.New(flags.Config{
		Flags: []flags.AnyFlag{beta, limit, locked},
		Store: flags.NewDatabaseStore(db, func() time.Time { return time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC) }),
		Cache: cache.New(cache.NewMemory(100), "pg:"),
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := ff.Set(ctx, flags.Global, "beta", "true"); err != nil {
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

	tenant := flags.WithScope(ctx, "tenant:1")
	if !ff.Enabled(ctx, beta) || flags.Get(ctx, ff, limit) != 30 || flags.Get(tenant, ff, limit) != 99 {
		t.Errorf("values = %v %d %d, want true 30 99", ff.Enabled(ctx, beta), flags.Get(ctx, ff, limit),
			flags.Get(tenant, ff, limit))
	}

	if _, err := ff.Set(ctx, flags.Global, "admin_two_factor", "false"); err == nil {
		t.Error("Set() on a locked flag succeeded")
	}

	if _, err := ff.Reset(ctx, "tenant:1", "upload_limit_mb"); err != nil {
		t.Fatal(err)
	}

	if got := flags.Get(tenant, ff, limit); got != 30 {
		t.Errorf("Get(tenant:1) after Reset = %d, want the global 30", got)
	}

	drop(ctx)

	// Fail closed: without the table every read is the default.
	if _, err := ff.Set(ctx, flags.Global, "beta", "false"); err == nil {
		t.Error("Set() without the table succeeded")
	}

	cold, err := flags.New(flags.Config{Flags: []flags.AnyFlag{beta}, Store: flags.NewDatabaseStore(db, nil)})
	if err != nil {
		t.Fatal(err)
	}

	if cold.Enabled(ctx, beta) {
		t.Error("a read without the table did not fall back to the default")
	}
}
