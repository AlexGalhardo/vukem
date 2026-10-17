package orm

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/database"
)

func TestLockSQL(t *testing.T) {
	t.Parallel()

	pg, my, lite := fakeEx{database.Postgres}, fakeEx{database.MySQL}, fakeEx{database.SQLite}
	tests := []struct {
		name string
		q    Query[User]
		want string
	}{
		{
			"postgres for update after limit",
			QueryOf[User](pg).Select("id").Where("id", 1).Limit(1).LockForUpdate(),
			`SELECT "id" FROM "users" WHERE "id" = $1 LIMIT 1 FOR UPDATE`,
		},
		{"postgres for share", QueryOf[User](pg).Select("id").ForShare(), `SELECT "id" FROM "users" FOR SHARE`},
		{
			"postgres skip locked",
			QueryOf[User](pg).Select("id").LockForUpdate().SkipLocked(),
			`SELECT "id" FROM "users" FOR UPDATE SKIP LOCKED`,
		},
		{
			"postgres nowait",
			QueryOf[User](pg).Select("id").ForShare().NoWait(),
			`SELECT "id" FROM "users" FOR SHARE NOWAIT`,
		},
		{
			"last lock wins",
			QueryOf[User](pg).Select("id").ForShare().LockForUpdate(),
			`SELECT "id" FROM "users" FOR UPDATE`,
		},
		{"mysql for update", QueryOf[User](my).Select("id").LockForUpdate(), "SELECT `id` FROM `users` FOR UPDATE"},
		{
			"mysql share works on mariadb too",
			QueryOf[User](my).Select("id").ForShare(),
			"SELECT `id` FROM `users` LOCK IN SHARE MODE",
		},
		{
			"mysql share with skip locked",
			QueryOf[User](my).Select("id").ForShare().SkipLocked(),
			"SELECT `id` FROM `users` FOR SHARE SKIP LOCKED",
		},
		{
			"sqlite ignores locks",
			QueryOf[User](lite).Select("id").Where("id", 1).LockForUpdate().SkipLocked(),
			`SELECT "id" FROM "users" WHERE "id" = ?`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, _, err := tt.q.SQL()
			if err != nil {
				t.Fatal(err)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Fatalf("SQL() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestLockModifierNeedsLock(t *testing.T) {
	t.Parallel()

	for name, q := range map[string]Query[User]{
		"skip locked": QueryOf[User](fakeEx{database.Postgres}).SkipLocked(),
		"nowait":      QueryOf[User](fakeEx{database.Postgres}).NoWait(),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, _, err := q.SQL(); err == nil || !strings.Contains(err.Error(), "LockForUpdate or ForShare") {
				t.Fatalf("SQL() err = %v, want a missing-lock error", err)
			}
		})
	}
}

func TestLockOnSQLite(t *testing.T) {
	t.Parallel()

	db, ctx := setup(t)
	users := seedUsers(ctx, t, db)

	// Outside a transaction the lock would be released immediately on
	// Postgres/MySQL, so the query fails on every dialect for portability.
	if _, err := QueryOf[User](db).LockForUpdate().Get(ctx); !errors.Is(err, ErrLockOutsideTx) {
		t.Fatalf("Get on *DB = %v, want ErrLockOutsideTx", err)
	}

	err := db.Tx(ctx, func(tx *database.Tx) error {
		got, err := QueryOf[User](tx).LockForUpdate().SkipLocked().Find(ctx, users[0].ID)
		if err != nil {
			return err
		}

		if got.Name != users[0].Name {
			t.Fatalf("Find = %q, want %q", got.Name, users[0].Name)
		}

		// Count drops the lock: Postgres rejects FOR UPDATE with aggregates.
		n, err := QueryOf[User](tx).ForShare().Count(ctx)
		if err != nil || n != int64(len(users)) {
			t.Fatalf("Count = %d, %v, want %d", n, err, len(users))
		}

		names, err := QueryOf[User](tx).OrderBy("id", Asc).LockForUpdate().Pluck[string](ctx, "name")
		if err != nil || len(names) != len(users) {
			t.Fatalf("Pluck = %v, %v", names, err)
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCountDropsLock(t *testing.T) {
	t.Parallel()

	got, _, _ := QueryOf[User](fakeEx{database.Postgres}).LockForUpdate().SkipLocked().countSQL()
	if want := `SELECT COUNT(*) FROM "users"`; got != want {
		t.Fatalf("countSQL() = %q, want %q", got, want)
	}
}
