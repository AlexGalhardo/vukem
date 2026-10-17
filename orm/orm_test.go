package orm

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/internal/naming"
)

type Settings struct {
	Theme string `json:"theme"`
}

type User struct {
	Model
	Name     string
	Email    string `db:"email"`
	Age      int
	Active   bool
	Settings JSON[Settings]
	Secret   string `db:"-"`
}

type Post struct {
	Model
	SoftDeletes
	UserID int64
	Title  string
}

type Category struct {
	Code string `db:"code"`
	Name string
}

func (Category) Table() string      { return "cats" }
func (Category) PrimaryKey() string { return "code" }

type hooked struct {
	Model
	Name  string
	calls *[]string
}

func (h *hooked) BeforeCreate(context.Context) error {
	if h.Name == "invalid" {
		return errors.New("rejected by hook")
	}
	*h.calls = append(*h.calls, "before_create")
	return nil
}

func (h *hooked) AfterCreate(context.Context) error {
	*h.calls = append(*h.calls, "after_create")
	return nil
}

func (h *hooked) BeforeUpdate(context.Context) error {
	*h.calls = append(*h.calls, "before_update")
	return nil
}

func (h *hooked) AfterUpdate(context.Context) error {
	*h.calls = append(*h.calls, "after_update")
	return nil
}

func (h *hooked) BeforeDelete(context.Context) error {
	*h.calls = append(*h.calls, "before_delete")
	return nil
}
func (hooked) Table() string { return "hooked" }

const ddl = `
CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, created_at TIMESTAMP, updated_at TIMESTAMP,
  name TEXT NOT NULL, email TEXT NOT NULL UNIQUE, age INTEGER NOT NULL DEFAULT 0, active BOOLEAN NOT NULL DEFAULT 0, settings TEXT);
CREATE TABLE posts (id INTEGER PRIMARY KEY AUTOINCREMENT, created_at TIMESTAMP, updated_at TIMESTAMP, deleted_at TIMESTAMP,
  user_id INTEGER NOT NULL, title TEXT NOT NULL);
CREATE TABLE cats (code TEXT PRIMARY KEY, name TEXT NOT NULL);
CREATE TABLE hooked (id INTEGER PRIMARY KEY AUTOINCREMENT, created_at TIMESTAMP, updated_at TIMESTAMP, name TEXT);
`

func setup(tb testing.TB) (*database.DB, context.Context) {
	tb.Helper()

	db, err := database.Open(tb.Context(), ":memory:")
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = db.Close() })

	for stmt := range strings.SplitSeq(ddl, ";") {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if _, err := db.ExecContext(tb.Context(), stmt); err != nil {
			tb.Fatal(err)
		}
	}

	fixed := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	return db, WithClock(tb.Context(), func() time.Time { return fixed })
}

func seedUsers(ctx context.Context, t *testing.T, db *database.DB) []User {
	t.Helper()

	users := []User{
		{Name: "Ana", Email: "ana@x.io", Age: 30, Active: true, Settings: JSON[Settings]{V: Settings{Theme: "dark"}}},
		{Name: "Bob", Email: "bob@x.io", Age: 17},
		{Name: "Cid", Email: "cid@x.io", Age: 45, Active: true},
		{Name: "Dea", Email: "dea@x.io", Age: 22},
	}

	for i := range users {
		if err := Create(ctx, db, &users[i]); err != nil {
			t.Fatal(err)
		}
	}

	return users
}

func TestMetaConventions(t *testing.T) {
	t.Parallel()

	if TableOf[User]() != "users" || TableOf[Category]() != "cats" || TableOf[Post]() != "posts" {
		t.Fatal("table names")
	}

	want := []string{"id", "created_at", "updated_at", "name", "email", "age", "active", "settings"}
	if diff := cmp.Diff(want, Columns[User]()); diff != "" {
		t.Fatalf("columns (-want +got):\n%s", diff)
	}

	for in, want := range map[string]string{"UserProfile": "user_profile", "HTTPCode": "http_code", "ID": "id", "APIKeyID": "api_key_id"} {
		if got := naming.Snake(in); got != want {
			t.Errorf("snake(%q) = %q, want %q", in, got, want)
		}
	}

	for in, want := range map[string]string{"user": "users", "category": "categories", "box": "boxes", "day": "days", "class": "classes", "batch": "batches"} {
		if got := naming.Plural(in); got != want {
			t.Errorf("plural(%q) = %q, want %q", in, got, want)
		}
	}

	type noPK struct{ Name string }
	defer func() {
		if recover() == nil {
			t.Fatal("model without primary key accepted")
		}
	}()
	metaOf[noPK]()
}

func TestCreateFindUpdateDelete(t *testing.T) {
	t.Parallel()

	db, ctx := setup(t)
	users := seedUsers(ctx, t, db)

	if users[0].ID != 1 || users[3].ID != 4 || users[0].CreatedAt.Year() != 2026 || users[0].UpdatedAt.IsZero() {
		t.Fatalf("Create did not fill id/timestamps: %+v", users[0].Model)
	}

	got, err := QueryOf[User](db).Find(ctx, 1)
	if err != nil || got.Name != "Ana" || got.Settings.V.Theme != "dark" || !got.Active {
		t.Fatalf("Find = %+v %v", got, err)
	}

	got.Name = "Ana Maria"
	if err := Update(
		WithClock(ctx, func() time.Time { return time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC) }),
		db,
		&got,
	); err != nil {
		t.Fatal(err)
	}

	again, _ := QueryOf[User](db).Find(ctx, 1)
	if again.Name != "Ana Maria" || again.UpdatedAt.Month() != 2 || again.CreatedAt.Month() != 1 {
		t.Fatalf("after Update = %+v", again)
	}

	if err := Delete(ctx, db, &again); err != nil {
		t.Fatal(err)
	}
	if _, err := QueryOf[User](db).Find(ctx, 1); !errors.Is(err, ErrNotFound) || !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("Find after delete = %v", err)
	}
	if err := Update(ctx, db, &again); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Update missing = %v", err)
	}
	if err := Delete(ctx, db, &again); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete missing = %v", err)
	}

	// String primary keys are inserted as given.
	cat := Category{Code: "go", Name: "Go"}
	if err := Create(ctx, db, &cat); err != nil {
		t.Fatal(err)
	}
	if c, err := QueryOf[Category](db).Find(ctx, "go"); err != nil || c.Name != "Go" {
		t.Fatalf("Find string pk = %+v %v", c, err)
	}
}

func TestUniqueViolationIsConflict(t *testing.T) {
	t.Parallel()

	db, ctx := setup(t)
	seedUsers(ctx, t, db)

	dup := User{Name: "X", Email: "ana@x.io"}
	err := Create(ctx, db, &dup)
	if !errors.Is(err, ErrConflict) || errs.Status(err) != 409 {
		t.Fatalf("duplicate email = %v (status %d)", err, errs.Status(err))
	}
}

func TestQueries(t *testing.T) {
	t.Parallel()

	db, ctx := setup(t)
	seedUsers(ctx, t, db)

	names := func(t *testing.T, q Query[User]) []string {
		t.Helper()
		got, err := q.Get(ctx)
		if err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, u := range got {
			out = append(out, u.Name)
		}
		return out
	}

	tests := []struct {
		name string
		q    Query[User]
		want []string
	}{
		{"where eq", QueryOf[User](db).Where("active", true).OrderBy("name", Asc), []string{"Ana", "Cid"}},
		{"where op", QueryOf[User](db).Where("age", ">=", 30).OrderBy("age", Desc), []string{"Cid", "Ana"}},
		{
			"or where",
			QueryOf[User](db).Where("name", "Bob").OrWhere("name", "Dea").OrderBy("id", Asc),
			[]string{"Bob", "Dea"},
		},
		{"in", QueryOf[User](db).WhereIn("id", []int{2, 3}).OrderBy("id", Asc), []string{"Bob", "Cid"}},
		{"in empty", QueryOf[User](db).WhereIn("id"), []string{}},
		{"not in", QueryOf[User](db).WhereNotIn("id", 1, 2).OrderBy("id", Asc), []string{"Cid", "Dea"}},
		{"not in empty", QueryOf[User](db).WhereNotIn("id").OrderBy("id", Asc), []string{"Ana", "Bob", "Cid", "Dea"}},
		{"between", QueryOf[User](db).WhereBetween("age", 20, 31).OrderBy("id", Asc), []string{"Ana", "Dea"}},
		{"null", QueryOf[User](db).WhereNull("settings"), []string{}}, // JSON zero values are stored as JSON, not NULL
		{
			"not null",
			QueryOf[User](db).WhereNotNull("settings").OrderBy("id", Asc),
			[]string{"Ana", "Bob", "Cid", "Dea"},
		},
		{
			"raw",
			QueryOf[User](db).WhereRaw("age > ? AND name LIKE ?", 18, "%a").OrderBy("id", Asc),
			[]string{"Ana", "Dea"},
		},
		{"group", QueryOf[User](db).Where("active", true).WhereGroup(func(g Query[User]) Query[User] {
			return g.Where("age", "<", 35).OrWhere("name", "Cid")
		}).OrderBy("id", Asc), []string{"Ana", "Cid"}},
		{
			"like",
			QueryOf[User](db).Where("email", "like", "%@x.io").Limit(2).Offset(1).OrderBy("id", Asc),
			[]string{"Bob", "Cid"},
		},
		{"offset only", QueryOf[User](db).OrderBy("id", Asc).Offset(3), []string{"Dea"}},
		{"latest", QueryOf[User](db).Latest().Limit(1), []string{"Dea"}},
		{"scope", QueryOf[User](db).Scope(adults, active), []string{"Ana", "Cid"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tt.want, names(t, tt.q)); diff != "" {
				t.Fatalf("(-want +got):\n%s", diff)
			}
		})
	}

	if n, err := QueryOf[User](db).Where("active", true).Count(ctx); err != nil || n != 2 {
		t.Fatalf("Count = %d %v", n, err)
	}
	if ok, _ := QueryOf[User](db).Where("name", "Zed").Exists(ctx); ok {
		t.Fatal("Exists(Zed)")
	}

	emails, err := QueryOf[User](db).OrderBy("id", Asc).Pluck[string](ctx, "email")
	if err != nil || len(emails) != 4 || emails[1] != "bob@x.io" {
		t.Fatalf("Pluck = %v %v", emails, err)
	}

	first, err := QueryOf[User](db).Where("age", "<", 25).First(ctx)
	if err != nil || first.Name != "Bob" {
		t.Fatalf("First = %v %v", first.Name, err)
	}

	sel, err := QueryOf[User](db).Select("id", "name").Find(ctx, 3)
	if err != nil || sel.Name != "Cid" || sel.Email != "" {
		t.Fatalf("Select = %+v %v", sel, err)
	}
}

func adults(q Query[User]) Query[User] { return q.Where("age", ">=", 18) }
func active(q Query[User]) Query[User] { return q.Where("active", true).OrderBy("id", Asc) }

func TestSecurity_A05_QueryBuilderRejectsInjection(t *testing.T) {
	t.Parallel()

	db, ctx := setup(t)
	seedUsers(ctx, t, db)

	bad := []Query[User]{
		QueryOf[User](db).Where("name; DROP TABLE users", "x"),
		QueryOf[User](db).Where("name", "= 1 OR 1=1 --", "x"),
		QueryOf[User](db).OrderBy("name; DROP TABLE users", Asc),
		QueryOf[User](db).OrderBy("name", Direction("ASC; DROP TABLE users")),
		QueryOf[User](db).Select("password"),
		QueryOf[User](db).Where("posts.title", "x"),
		QueryOf[User](db).WhereRaw("name = 'x' OR 1=1 --", "extra"),
		QueryOf[User](db).WhereIn("id; --", 1),
		QueryOf[User](db).WhereBetween("nope", 1, 2),
		QueryOf[User](db).Where("age"),
	}

	for i, q := range bad {
		if _, err := q.Get(ctx); err == nil {
			t.Errorf("query %d accepted malicious input", i)
		}
	}

	if _, err := QueryOf[User](db).Pluck[string](ctx, "x;--"); err == nil {
		t.Error("Pluck accepted bad column")
	}

	// Values are always bound, never interpolated.
	got, err := QueryOf[User](db).Where("name", "' OR '1'='1").Get(ctx)
	if err != nil || len(got) != 0 {
		t.Fatalf("value injection = %v %v", got, err)
	}

	if n, _ := QueryOf[User](db).Count(ctx); n != 4 {
		t.Fatalf("table damaged: %d rows", n)
	}

	if _, _, err := QueryOf[User](db).Where("bad col", 1).SQL(); err == nil {
		t.Fatal("SQL() hid builder error")
	}
}

func TestEachAndChunkAndPaginate(t *testing.T) {
	t.Parallel()

	db, ctx := setup(t)
	seedUsers(ctx, t, db)

	var seen []string
	for u, err := range QueryOf[User](db).OrderBy("id", Asc).Each(ctx) {
		if err != nil {
			t.Fatal(err)
		}
		seen = append(seen, u.Name)
		if len(seen) == 2 {
			break // must close rows
		}
	}
	if !slices.Equal(seen, []string{"Ana", "Bob"}) {
		t.Fatalf("Each = %v", seen)
	}

	// After an early break the single connection is free again.
	if _, err := QueryOf[User](db).Count(ctx); err != nil {
		t.Fatalf("connection leaked: %v", err)
	}

	var batches [][]string
	err := QueryOf[User](db).Chunk(ctx, 3, func(us []User) error {
		var b []string
		for _, u := range us {
			b = append(b, u.Name)
		}
		batches = append(batches, b)
		return nil
	})
	if err != nil || len(batches) != 2 || len(batches[1]) != 1 {
		t.Fatalf("Chunk = %v %v", batches, err)
	}
	if err := QueryOf[User](db).Chunk(ctx, 0, nil); err == nil {
		t.Fatal("Chunk(0)")
	}
	stop := errors.New("stop")
	if err := QueryOf[User](db).Chunk(ctx, 1, func([]User) error { return stop }); !errors.Is(err, stop) {
		t.Fatal("Chunk stop")
	}

	p, err := QueryOf[User](db).OrderBy("id", Asc).Paginate(ctx, 2, 3)
	if err != nil || p.Total != 4 || p.LastPage != 2 || len(p.Items) != 1 || p.Items[0].Name != "Dea" || p.Page != 2 {
		t.Fatalf("Paginate = %+v %v", p, err)
	}

	empty, _ := QueryOf[User](db).Where("age", ">", 100).Paginate(ctx, 0, 0)
	if empty.LastPage != 1 || empty.Page != 1 || len(empty.Items) != 0 {
		t.Fatalf("empty page = %+v", empty)
	}
}

func TestSoftDeletes(t *testing.T) {
	t.Parallel()

	db, ctx := setup(t)

	posts := []Post{{UserID: 1, Title: "a"}, {UserID: 1, Title: "b"}, {UserID: 2, Title: "c"}}
	for i := range posts {
		if err := Create(ctx, db, &posts[i]); err != nil {
			t.Fatal(err)
		}
	}

	if err := Delete(ctx, db, &posts[0]); err != nil || !posts[0].DeletedAt.Valid {
		t.Fatalf("soft delete = %v %+v", err, posts[0].DeletedAt)
	}

	count := func(q Query[Post]) int64 { n, _ := q.Count(ctx); return n }

	if count(QueryOf[Post](db)) != 2 || count(QueryOf[Post](db).WithTrashed()) != 3 ||
		count(QueryOf[Post](db).OnlyTrashed()) != 1 {
		t.Fatal("trashed scoping")
	}

	// OR conditions cannot leak trashed rows.
	if got := count(QueryOf[Post](db).Where("title", "a").OrWhere("title", "c")); got != 1 {
		t.Fatalf("OR leaked trashed rows: %d", got)
	}

	if err := Restore(ctx, db, &posts[0]); err != nil || posts[0].DeletedAt.Valid || count(QueryOf[Post](db)) != 3 {
		t.Fatal("Restore")
	}

	if n, _ := QueryOf[Post](
		db,
	).Where("user_id", 1).
		Delete(ctx); n != 2 ||
		count(QueryOf[Post](db).OnlyTrashed()) != 2 {
		t.Fatal("bulk soft delete")
	}

	if err := ForceDelete(ctx, db, &posts[2]); err != nil || count(QueryOf[Post](db).WithTrashed()) != 2 {
		t.Fatal("ForceDelete")
	}

	if n, err := QueryOf[Post](db).OnlyTrashed().Delete(ctx); err != nil || n != 2 {
		t.Fatalf("purge = %d %v", n, err)
	}

	if err := Restore(ctx, db, &User{}); err == nil {
		t.Fatal("Restore on non-soft model")
	}
}

func TestBulkUpdate(t *testing.T) {
	t.Parallel()

	db, ctx := setup(t)
	seedUsers(ctx, t, db)

	n, err := QueryOf[User](db).Where("age", "<", 25).Update(ctx, Set{"active": true})
	if err != nil || n != 2 {
		t.Fatalf("Update = %d %v", n, err)
	}
	if c, _ := QueryOf[User](db).Where("active", true).Count(ctx); c != 4 {
		t.Fatalf("active = %d", c)
	}

	if _, err := QueryOf[User](db).Update(ctx, Set{}); err == nil {
		t.Fatal("empty Set")
	}
	if _, err := QueryOf[User](db).Update(ctx, Set{"evil;--": 1}); err == nil {
		t.Fatal("bad column in Set")
	}
}

func TestHooksAndTransactions(t *testing.T) {
	t.Parallel()

	db, ctx := setup(t)

	var calls []string
	h := hooked{Name: "x", calls: &calls}
	if err := Create(ctx, db, &h); err != nil {
		t.Fatal(err)
	}
	if err := Update(ctx, db, &h); err != nil {
		t.Fatal(err)
	}
	if err := Delete(ctx, db, &h); err != nil {
		t.Fatal(err)
	}

	want := []string{"before_create", "after_create", "before_update", "after_update", "before_delete"}
	if diff := cmp.Diff(want, calls); diff != "" {
		t.Fatal(diff)
	}

	bad := hooked{Name: "invalid", calls: &calls}
	if err := Create(ctx, db, &bad); err == nil || bad.ID != 0 {
		t.Fatal("hook did not abort create")
	}

	// Writes inside a failed transaction are rolled back.
	err := db.Tx(ctx, func(tx *database.Tx) error {
		u := User{Name: "Tx", Email: "tx@x.io"}
		if err := Create(ctx, tx, &u); err != nil {
			return err
		}
		return errors.New("abort")
	})
	if err == nil {
		t.Fatal("tx error lost")
	}
	if ok, _ := QueryOf[User](db).Where("email", "tx@x.io").Exists(ctx); ok {
		t.Fatal("rolled-back row visible")
	}
}

func TestJSONColumn(t *testing.T) {
	t.Parallel()

	var j JSON[Settings]
	if err := j.Scan(nil); err != nil {
		t.Fatal(err)
	}
	if err := j.Scan([]byte(`{"theme":"x"}`)); err != nil || j.V.Theme != "x" {
		t.Fatal("Scan bytes")
	}
	if err := j.Scan(3); err == nil {
		t.Fatal("Scan int")
	}

	b, _ := j.MarshalJSON()
	var k JSON[Settings]
	if err := k.UnmarshalJSON(b); err != nil || k.V.Theme != "x" {
		t.Fatal("JSON roundtrip")
	}

	var n sql.Null[time.Time]
	_ = n
}

func TestPostgresPlaceholders(t *testing.T) {
	t.Parallel()

	q := QueryOf[User](
		fakeEx{database.Postgres},
	).Where("name", "a").
		WhereIn("id", 1, 2).
		WhereRaw("note = '?'").
		Limit(5).
		Offset(10)
	s, args, err := q.SQL()

	want := `SELECT "id", "created_at", "updated_at", "name", "email", "age", "active", "settings" FROM "users" WHERE "name" = $1 AND "id" IN ($2, $3) AND (note = '?') LIMIT 5 OFFSET 10`
	if err != nil || s != want || len(args) != 3 {
		t.Fatalf("SQL =\n%s\n%v %v", s, args, err)
	}

	s, _, _ = QueryOf[User](fakeEx{database.Postgres}).Offset(3).SQL()
	if strings.Contains(s, "LIMIT -1") {
		t.Fatal("postgres got sqlite LIMIT -1")
	}
}

func TestMySQLSQL(t *testing.T) {
	t.Parallel()

	ex := fakeEx{database.MySQL}
	tests := []struct {
		name string
		q    Query[User]
		want string
	}{
		{
			"placeholders and backticks",
			QueryOf[User](ex).Where("name", "a").WhereIn("id", 1, 2).Limit(5).Offset(10),
			"SELECT `id`, `created_at`, `updated_at`, `name`, `email`, `age`, `active`, `settings` FROM `users` " +
				"WHERE `name` = ? AND `id` IN (?, ?) LIMIT 5 OFFSET 10",
		},
		{
			"offset needs a limit",
			QueryOf[User](ex).Select("id").Offset(3),
			"SELECT `id` FROM `users` LIMIT 18446744073709551615 OFFSET 3",
		},
		{
			"ilike becomes like",
			QueryOf[User](ex).Select("id").Where("name", "ilike", "%a%"),
			"SELECT `id` FROM `users` WHERE `name` LIKE ?",
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

func TestMySQLCreateUsesLastInsertID(t *testing.T) {
	t.Parallel()

	ex := &lastIDEx{id: 42}

	u := User{Name: "Ana"}
	if err := Create(t.Context(), ex, &u); err != nil {
		t.Fatal(err)
	}

	if u.ID != 42 || strings.Contains(ex.query, "RETURNING") {
		t.Fatalf("Create = id %d, query %q, want id 42 without RETURNING", u.ID, ex.query)
	}

	dup := &lastIDEx{err: errors.New("Error 1062 (23000): Duplicate entry 'Ana' for key 'users.name'")}
	if err := Create(t.Context(), dup, &User{Name: "Ana"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate = %v, want ErrConflict", err)
	}
}

// lastIDEx is a MySQL executor whose inserts return a fixed id.
type lastIDEx struct {
	fakeEx

	id    int64
	err   error
	query string
}

func (e *lastIDEx) Dialect() database.Dialect { return database.MySQL }

func (e *lastIDEx) ExecContext(_ context.Context, q string, _ ...any) (sql.Result, error) {
	e.query = q
	if e.err != nil {
		return nil, e.err
	}

	return driverResult(e.id), nil
}

type driverResult int64

func (r driverResult) LastInsertId() (int64, error) { return int64(r), nil }
func (driverResult) RowsAffected() (int64, error)   { return 1, nil }

type fakeEx struct{ d database.Dialect }

func (f fakeEx) Dialect() database.Dialect { return f.d }
func (fakeEx) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, errors.New("fake")
}

func (fakeEx) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, errors.New("fake")
}
func (fakeEx) QueryRowContext(context.Context, string, ...any) *sql.Row { return nil }

func FuzzWhereColumn(f *testing.F) {
	for _, s := range []string{"name", "users.name", "name; DROP TABLE users", "\"name\"", "*", "a.b.c"} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, col string) {
		s, _, err := QueryOf[User](fakeEx{database.SQLite}).Where(col, 1).SQL()
		if err != nil {
			return
		}

		// Accepted columns must be known model columns, never raw input.
		if col != "*" && !slices.Contains(Columns[User](), strings.TrimPrefix(col, "users.")) {
			t.Fatalf("unknown column %q accepted: %s", col, s)
		}
	})
}
