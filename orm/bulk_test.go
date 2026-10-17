package orm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/database"
)

func newUsers(n int) []User {
	out := make([]User, n)
	for i := range out {
		out[i] = User{Name: fmt.Sprintf("u%d", i), Email: fmt.Sprintf("u%d@x.io", i), Age: i}
	}

	return out
}

func TestCreateMany(t *testing.T) {
	t.Parallel()

	db, ctx := setup(t)
	users := newUsers(25)

	// 7 columns per row and a 20-parameter limit: two rows per statement.
	ex := &countingEx{Executor: db}
	if err := createMany(ctx, ex, users, 20); err != nil {
		t.Fatal(err)
	}

	if ex.calls != 13 {
		t.Fatalf("statements = %d, want 13 batches of 2 rows", ex.calls)
	}

	for i, u := range users {
		if u.ID != int64(i+1) || u.CreatedAt.IsZero() || u.UpdatedAt.IsZero() {
			t.Fatalf("users[%d] = %+v, want id %d and timestamps", i, u.Model, i+1)
		}
	}

	got, err := QueryOf[User](db).OrderBy("id", Asc).Pluck[string](ctx, "email")
	if err != nil {
		t.Fatal(err)
	}

	want := make([]string, len(users))
	for i, u := range users {
		want[i] = u.Email
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("stored rows mismatch (-want +got):\n%s", diff)
	}

	if err := CreateMany(ctx, db, []User{}); err != nil {
		t.Fatalf("CreateMany(empty) = %v, want nil", err)
	}

	cats := []Category{{Code: "go", Name: "Go"}, {Code: "rs", Name: "Rust"}}
	if err := CreateMany(ctx, db, cats); err != nil {
		t.Fatal(err)
	}

	if n, _ := QueryOf[Category](db).Count(ctx); n != 2 {
		t.Fatalf("categories = %d, want 2", n)
	}
}

func TestCreateManyIsAtomic(t *testing.T) {
	t.Parallel()

	db, ctx := setup(t)
	users := newUsers(6)
	users[5].Email = users[0].Email // fails in the last batch

	err := createMany(ctx, db, users, 14)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("CreateMany(duplicate) = %v, want ErrConflict", err)
	}

	if n, _ := QueryOf[User](db).Count(ctx); n != 0 {
		t.Fatalf("rows after failed CreateMany = %d, want 0 (rolled back)", n)
	}

	for i, u := range users {
		if u.ID != 0 {
			t.Fatalf("users[%d].ID = %d after rollback, want 0", i, u.ID)
		}
	}

	// Inside the caller's transaction, the caller decides.
	err = db.Tx(ctx, func(tx *database.Tx) error {
		if err := CreateMany(ctx, tx, newUsers(3)); err != nil {
			return err
		}

		return errors.New("abort")
	})
	if err == nil {
		t.Fatal("tx error lost")
	}

	if n, _ := QueryOf[User](db).Count(ctx); n != 0 {
		t.Fatalf("rows after rolled-back tx = %d, want 0", n)
	}
}

func TestCreateManyHooksAndErrors(t *testing.T) {
	t.Parallel()

	db, ctx := setup(t)

	var calls []string
	items := []hooked{{Name: "a", calls: &calls}, {Name: "b", calls: &calls}}
	if err := CreateMany(ctx, db, items); err != nil {
		t.Fatal(err)
	}

	want := []string{"before_create", "before_create", "after_create", "after_create"}
	if diff := cmp.Diff(want, calls); diff != "" {
		t.Fatalf("hooks mismatch (-want +got):\n%s", diff)
	}

	bad := []hooked{{Name: "c", calls: &calls}, {Name: "invalid", calls: &calls}}
	if err := CreateMany(ctx, db, bad); err == nil {
		t.Fatal("hook did not abort CreateMany")
	}

	if n, _ := QueryOf[hooked](db).Where("name", "c").Count(ctx); n != 0 {
		t.Fatal("row inserted although a BeforeCreate hook failed")
	}

	mixed := newUsers(2)
	mixed[1].ID = 99
	if err := CreateMany(ctx, db, mixed); err == nil || !strings.Contains(err.Error(), "mix") {
		t.Fatalf("CreateMany(mixed ids) = %v, want an error", err)
	}

	given := newUsers(2)
	given[0].ID, given[1].ID = 10, 20
	if err := CreateMany(ctx, db, given); err != nil {
		t.Fatal(err)
	}

	if u, err := QueryOf[User](db).Find(ctx, 20); err != nil || u.Email != given[1].Email {
		t.Fatalf("Find(20) = %+v, %v", u, err)
	}
}

func TestCreateManySQL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		d    database.Dialect
		want string
	}{
		{"postgres", database.Postgres, `INSERT INTO "cats" ("code", "name") VALUES ($1, $2), ($3, $4)`},
		{"mysql", database.MySQL, "INSERT INTO `cats` (`code`, `name`) VALUES (?, ?), (?, ?)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ex := &recordEx{fakeEx: fakeEx{tt.d}}
			if err := CreateMany(t.Context(), ex, []Category{{"a", "A"}, {"b", "B"}}); err != nil {
				t.Fatal(err)
			}

			if diff := cmp.Diff([]string{tt.want}, ex.queries); diff != "" {
				t.Fatalf("queries mismatch (-want +got):\n%s", diff)
			}
		})
	}

	ex := &recordEx{fakeEx: fakeEx{database.Postgres}}
	_ = CreateMany(t.Context(), ex, newUsers(2))

	if len(ex.queries) != 1 || !strings.HasSuffix(ex.queries[0], `($8, $9, $10, $11, $12, $13, $14) RETURNING "id"`) {
		t.Fatalf("postgres auto-id query = %q, want a RETURNING multi-row insert", ex.queries)
	}

	// MySQL cannot return a batch of generated ids reliably: one row each.
	ex = &recordEx{fakeEx: fakeEx{database.MySQL}}
	if err := CreateMany(t.Context(), ex, newUsers(3)); err != nil {
		t.Fatal(err)
	}

	if len(ex.queries) != 3 {
		t.Fatalf("mysql auto-id statements = %d, want 3", len(ex.queries))
	}
}

func TestMaxParams(t *testing.T) {
	t.Parallel()

	for d, want := range map[database.Dialect]int{
		database.SQLite:   32766,
		database.Postgres: 65535,
		database.MySQL:    65535,
	} {
		if got := maxParams(d); got != want {
			t.Errorf("maxParams(%s) = %d, want %d", d.Name(), got, want)
		}
	}
}

// countingEx counts statements sent to the wrapped executor.
type countingEx struct {
	database.Executor

	calls int
}

func (c *countingEx) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	c.calls++
	return c.Executor.ExecContext(ctx, q, args...)
}

func (c *countingEx) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	c.calls++
	return c.Executor.QueryContext(ctx, q, args...)
}

// recordEx records statements; writes succeed and queries fail.
type recordEx struct {
	fakeEx

	queries []string
}

func (r *recordEx) ExecContext(_ context.Context, q string, _ ...any) (sql.Result, error) {
	r.queries = append(r.queries, q)
	return driverResult(int64(len(r.queries))), nil
}

func (r *recordEx) QueryContext(_ context.Context, q string, _ ...any) (*sql.Rows, error) {
	r.queries = append(r.queries, q)
	return nil, errors.New("fake")
}
