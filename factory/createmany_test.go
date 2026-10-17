package factory

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/internal/testutil"
	"github.com/AlexGalhardo/vukem/orm"
)

func TestCreateMany(t *testing.T) {
	t.Parallel()

	db := setup(t)
	ctx := t.Context()

	var hooked []int64

	withPosts := users.AfterCreate(func(ctx context.Context, ex database.Executor, u *User) error {
		hooked = append(hooked, u.ID)

		_, err := posts.Count(2).State(func(p *Post) { p.UserID = u.ID }).CreateMany(ctx, ex)

		return err
	})

	created, err := withPosts.Count(250).CreateMany(ctx, db)
	if err != nil {
		t.Fatalf("CreateMany() = %v", err)
	}

	if len(created) != 250 || len(hooked) != 250 {
		t.Fatalf("CreateMany() = %d models, %d hooks, want 250", len(created), len(hooked))
	}

	seen := map[int64]bool{}

	for i, u := range created {
		if u.ID == 0 || seen[u.ID] || hooked[i] != u.ID || u.CreatedAt.IsZero() {
			t.Fatalf("created[%d] = %+v (hook saw id %d)", i, u, hooked[i])
		}

		seen[u.ID] = true

		stored, err := orm.QueryOf[User](db).Find(ctx, u.ID)
		if err != nil || stored.Email != u.Email {
			t.Fatalf("Find(%d) = %+v, %v, want email %q", u.ID, stored, err, u.Email)
		}
	}

	if n, _ := orm.QueryOf[Post](db).Count(ctx); n != 500 {
		t.Fatalf("posts = %d, want 500", n)
	}

	if got, err := users.Count(0).CreateMany(ctx, db); err != nil || len(got) != 0 {
		t.Fatalf("Count(0).CreateMany() = %v, %v", got, err)
	}
}

func TestCreateManyRollsBack(t *testing.T) {
	t.Parallel()

	db := setup(t)
	ctx := t.Context()
	boom := errors.New("boom")

	calls := 0
	failing := users.AfterCreate(func(context.Context, database.Executor, *User) error {
		calls++
		if calls == 3 {
			return boom
		}

		return nil
	})

	if got, err := failing.Count(5).CreateMany(ctx, db); !errors.Is(err, boom) || got != nil {
		t.Fatalf("CreateMany() = %v, %v, want nil, boom", got, err)
	}

	dup := users.State(func(u *User) { u.Email = "same@example.com" })
	if _, err := dup.Count(2).CreateMany(ctx, db); !errors.Is(err, orm.ErrConflict) {
		t.Fatalf("duplicate CreateMany() = %v, want ErrConflict", err)
	}

	if n, _ := orm.QueryOf[User](db).Count(ctx); n != 0 {
		t.Fatalf("users after rollback = %d, want 0", n)
	}

	// Inside the caller's transaction the rows commit with it.
	err := db.Tx(ctx, func(tx *database.Tx) error {
		_, err := users.Count(3).CreateMany(ctx, tx)
		return err
	})
	if n, _ := orm.QueryOf[User](db).Count(ctx); err != nil || n != 3 {
		t.Fatalf("CreateMany in Tx = %v, %d users, want 3", err, n)
	}
}

// BenchmarkCreate compares row-by-row Create with batched CreateMany on a
// file database (WAL, like an app's).
func BenchmarkCreate(b *testing.B) {
	for _, n := range []int{100, 1000} {
		for name, create := range map[string]func(Factory[User], context.Context, database.Executor) ([]User, error){
			"Create":     Factory[User].Create,
			"CreateMany": Factory[User].CreateMany,
		} {
			b.Run(fmt.Sprintf("%s/%d", name, n), func(b *testing.B) {
				db, err := database.Open(
					b.Context(),
					"file:"+filepath.ToSlash(filepath.Join(testutil.TempDir(b), "bench.db")),
				)
				if err != nil {
					b.Fatal(err)
				}

				b.Cleanup(func() { _ = db.Close() })

				if _, err := db.ExecContext(b.Context(), `CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT,
					created_at TIMESTAMP, updated_at TIMESTAMP, name TEXT NOT NULL, email TEXT NOT NULL, role TEXT NOT NULL)`); err != nil {
					b.Fatal(err)
				}

				f := users.Count(n)

				for b.Loop() {
					if _, err := create(f, b.Context(), db); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
