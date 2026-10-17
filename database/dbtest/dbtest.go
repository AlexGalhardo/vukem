// Package dbtest is the database conformance suite: the same schema, ORM,
// transaction and migration checks run against every dialect (SQLite in
// core, Postgres and others in their driver modules).
//
//	func TestConformance(t *testing.T) {
//		db := open(t) // *database.DB for the dialect under test
//		dbtest.Run(t, db)
//	}
//
// Run creates and drops its own tables (prefixed "dbt_"), so point it at a
// throwaway database.
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/database.md.
package dbtest

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/schema"
)

// meta is a JSON column payload.
type meta struct {
	Tags []string `json:"tags"`
}

// author is the main conformance model.
type author struct {
	orm.Model
	orm.SoftDeletes

	Name   string
	Email  *string
	Active bool
	Score  float64
	Meta   orm.JSON[meta]

	Books     orm.HasMany[book] `json:"books,omitzero"`
	FirstBook orm.HasOne[book]  `json:"first_book,omitzero" orm:"fk:author_id"`
}

// Table implements orm.Tabler.
func (author) Table() string { return "dbt_authors" }

// book belongs to an author (foreign key with cascade).
type book struct {
	orm.Model

	AuthorID  int64
	Title     string
	Pages     int
	Published *time.Time

	Author orm.BelongsTo[author] `json:"author,omitzero"`
	Tags   orm.ManyToMany[tag]   `json:"tags,omitzero"   orm:"pivot:dbt_book_tag"`
}

// Table implements orm.Tabler.
func (book) Table() string { return "dbt_books" }

var migrations = []schema.Migration{
	{
		ID: "dbt_1_authors",
		Up: func(s *schema.Schema) error {
			return s.Create("dbt_authors", func(t *schema.Table) {
				t.ID()
				t.String("name").Unique()
				t.String("email").Nullable()
				t.Boolean("active").Default(false)
				t.Float("score").Default(0)
				t.JSON("meta").Nullable()
				t.Timestamps()
				t.SoftDeletes()
			})
		},
		Down: func(s *schema.Schema) error { return s.DropIfExists("dbt_authors") },
	},
	{
		ID: "dbt_2_books",
		Up: func(s *schema.Schema) error {
			return s.Create("dbt_books", func(t *schema.Table) {
				t.ID()
				t.ForeignID("author_id").Constrained("dbt_authors").CascadeOnDelete()
				t.String("title")
				t.Integer("pages").Default(0)
				t.Timestamp("published").Nullable()
				t.Timestamps()
				t.Index("title")
			})
		},
		Down: func(s *schema.Schema) error { return s.DropIfExists("dbt_books") },
	},
	tagsMigration,
}

// Run executes the conformance suite against db. Subtests run in order and
// share tables, so they are not parallel.
func Run(t *testing.T, db *database.DB) {
	t.Helper()

	ctx := t.Context()
	clean(ctx, t, db)
	t.Cleanup(func() { clean(context.Background(), t, db) })

	t.Run("Migrations", func(t *testing.T) { testMigrations(ctx, t, db) })
	t.Run("ORM", func(t *testing.T) { testORM(ctx, t, db) })
	t.Run("Transactions", func(t *testing.T) { testTransactions(ctx, t, db) })
	t.Run("Relations", func(t *testing.T) { testRelations(ctx, t, db) })
	t.Run("SchemaAlter", func(t *testing.T) { testAlter(ctx, t, db) })
	t.Run("Queue", func(t *testing.T) { testQueue(ctx, t, db) })
	t.Run("BulkLocksChecks", func(t *testing.T) { testBulkLocksChecks(ctx, t, db) })
	t.Run("PartialIndexRebind", func(t *testing.T) { testPartialIndexRebind(ctx, t, db) })
}

func clean(ctx context.Context, t *testing.T, db *database.DB) {
	t.Helper()

	s := schema.New(ctx, db)
	for _, table := range []string{"dbt_book_tag", "dbt_tags", "dbt_books", "dbt_authors", "dbt_tmp", "dbt_renamed", "dbt_jobs", "dbt_failed_jobs", "dbt_numbers", "dbt_desc"} {
		if err := s.DropIfExists(table); err != nil {
			t.Fatalf("drop %s: %v", table, err)
		}
	}

	// Forget this suite's rows in the shared migrations table.
	if has, err := s.HasTable("migrations"); err == nil && has {
		if _, err := db.ExecContext(ctx, "DELETE FROM migrations WHERE id LIKE 'dbt_%'"); err != nil {
			t.Fatalf("clean migrations: %v", err)
		}
	}
}

func migrator(t *testing.T, db *database.DB) *schema.Migrator {
	t.Helper()

	m, err := schema.NewMigrator(db, migrations)
	if err != nil {
		t.Fatal(err)
	}

	return m
}

func testMigrations(ctx context.Context, t *testing.T, db *database.DB) {
	t.Helper()

	m := migrator(t, db)

	ran, err := m.Up(ctx)
	if err != nil || len(ran) != len(migrations) {
		t.Fatalf("Up = %v, %v", ran, err)
	}

	if again, err := m.Up(ctx); err != nil || len(again) != 0 {
		t.Fatalf("second Up = %v, %v", again, err)
	}

	st, err := m.Status(ctx)
	last := len(migrations) - 1

	if err != nil || len(st) != len(migrations) || !st[0].Ran || !st[last].Ran || st[last].Changed {
		t.Fatalf("Status = %+v, %v", st, err)
	}

	rolled, err := m.Rollback(ctx, 1)
	if err != nil || !slices.Equal(rolled, []string{migrations[last].ID}) {
		t.Fatalf("Rollback = %v, %v", rolled, err)
	}

	if has, err := schema.New(ctx, db).HasTable("dbt_tags"); err != nil || has {
		t.Fatalf("tags after rollback: has=%v err=%v", has, err)
	}

	if _, err := m.Fresh(ctx); err != nil {
		t.Fatalf("Fresh: %v", err)
	}

	for _, table := range []string{"dbt_authors", "dbt_books", "dbt_tags", "dbt_book_tag"} {
		if has, err := schema.New(ctx, db).HasTable(table); err != nil || !has {
			t.Fatalf("%s after Fresh: has=%v err=%v", table, has, err)
		}
	}
}

func testORM(ctx context.Context, t *testing.T, db *database.DB) {
	t.Helper()

	fixed := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	ctx = orm.WithClock(ctx, func() time.Time { return fixed })

	email := "ana@example.com"
	authors := []author{
		{
			Name:   "Ana",
			Email:  &email,
			Active: true,
			Score:  9.5,
			Meta:   orm.JSON[meta]{V: meta{Tags: []string{"go", "sql"}}},
		},
		{Name: "Bob", Score: 7},
		{Name: "Cid", Active: true, Score: 8.25},
	}

	for i := range authors {
		if err := orm.Create(ctx, db, &authors[i]); err != nil {
			t.Fatalf("Create %s: %v", authors[i].Name, err)
		}
	}

	if authors[0].ID == 0 || authors[2].ID <= authors[1].ID || !authors[0].CreatedAt.Equal(fixed) {
		t.Fatalf("ids/timestamps = %+v", authors)
	}

	// Round trip of every column type.
	got, err := orm.QueryOf[author](db).Find(ctx, authors[0].ID)
	if err != nil {
		t.Fatal(err)
	}

	if got.Name != "Ana" || got.Email == nil || *got.Email != email || !got.Active || got.Score != 9.5 ||
		!slices.Equal(got.Meta.V.Tags, []string{"go", "sql"}) || !got.CreatedAt.Equal(fixed) {
		t.Fatalf("round trip = %+v", got)
	}

	if bob, _ := orm.QueryOf[author](db).Where("name", "Bob").First(ctx); bob.Email != nil || bob.Active {
		t.Fatalf("nullable/default columns = %+v", bob)
	}

	// Queries.
	count := func(q orm.Query[author]) int64 {
		t.Helper()

		n, err := q.Count(ctx)
		if err != nil {
			t.Fatal(err)
		}

		return n
	}

	base := orm.QueryOf[author](db)
	checks := map[string]struct {
		q    orm.Query[author]
		want int64
	}{
		"where bool": {base.Where("active", true), 2},
		"operator":   {base.Where("score", ">=", 8), 2},
		"in":         {base.WhereIn("name", "Ana", "Cid", "Zed"), 2},
		"not in":     {base.WhereNotIn("name", "Ana"), 2},
		"null":       {base.WhereNull("email"), 2},
		"not null":   {base.WhereNotNull("email"), 1},
		"between":    {base.WhereBetween("score", 7.5, 9), 1},
		"like":       {base.Where("name", "LIKE", "%i%"), 1},
		"or group": {
			base.WhereGroup(
				func(g orm.Query[author]) orm.Query[author] { return g.Where("name", "Bob").OrWhere("name", "Cid") },
			),
			2,
		},
		"raw":      {base.WhereRaw("score > ?", 8), 2},
		"no match": {base.Where("name", "Nobody"), 0},
	}

	for name := range checks {
		if n := count(checks[name].q); n != checks[name].want {
			t.Errorf("%s: count = %d, want %d", name, n, checks[name].want)
		}
	}

	ordered, err := base.OrderBy("score", orm.Desc).Get(ctx)
	if err != nil || len(ordered) != 3 || ordered[0].Name != "Ana" || ordered[2].Name != "Bob" {
		t.Fatalf("order = %v %v", ordered, err)
	}

	names, err := base.OrderBy("name", orm.Asc).Pluck[string](ctx, "name")
	if err != nil || !slices.Equal(names, []string{"Ana", "Bob", "Cid"}) {
		t.Fatalf("Pluck = %v %v", names, err)
	}

	page, err := base.OrderBy("id", orm.Asc).Paginate(ctx, 2, 2)
	if err != nil || page.Total != 3 || page.LastPage != 2 || len(page.Items) != 1 || page.Items[0].Name != "Cid" {
		t.Fatalf("Paginate = %+v %v", page, err)
	}

	cp, err := base.CursorPaginate(ctx, "", 2, orm.Asc)
	if err != nil || len(cp.Items) != 2 || cp.NextCursor == "" {
		t.Fatalf("CursorPaginate = %+v %v", cp, err)
	}

	if next, err := base.CursorPaginate(
		ctx,
		cp.NextCursor,
		2,
		orm.Asc,
	); err != nil || len(next.Items) != 1 ||
		next.NextCursor != "" {
		t.Fatalf("CursorPaginate next = %+v %v", next, err)
	}

	// Unique violation → 409.
	dup := author{Name: "Ana"}
	if err := orm.Create(ctx, db, &dup); !errors.Is(err, orm.ErrConflict) || errs.Status(err) != 409 {
		t.Fatalf("duplicate = %v", err)
	}

	// Updates.
	got.Score = 10
	if err := orm.Update(ctx, db, &got); err != nil {
		t.Fatal(err)
	}

	if n, err := base.Where("active", false).Update(ctx, orm.Set{"score": 1}); err != nil || n != 1 {
		t.Fatalf("bulk Update = %d %v", n, err)
	}

	if s, _ := base.Where("name", "Bob").First(ctx); s.Score != 1 {
		t.Fatalf("bulk update not applied: %+v", s)
	}

	// Foreign keys cascade; soft deletes hide rows.
	published := fixed.Add(-time.Hour)
	created := book{AuthorID: authors[1].ID, Title: "Go", Pages: 300, Published: &published}

	if err := orm.Create(ctx, db, &created); err != nil {
		t.Fatal(err)
	}

	if b, err := orm.QueryOf[book](
		db,
	).Find(ctx, created.ID); err != nil || b.Published == nil ||
		!b.Published.Equal(published) {
		t.Fatalf("book = %+v %v", b, err)
	}

	orphan := book{AuthorID: 999_999, Title: "Orphan"}
	if err := orm.Create(ctx, db, &orphan); err == nil {
		t.Fatal("foreign key not enforced")
	}

	bob := authors[1]
	if err := orm.Delete(ctx, db, &bob); err != nil {
		t.Fatal(err)
	}

	if n := count(base); n != 2 {
		t.Fatalf("after soft delete count = %d", n)
	}

	if n := count(base.WithTrashed()); n != 3 {
		t.Fatalf("WithTrashed count = %d", n)
	}

	if err := orm.Restore(ctx, db, &bob); err != nil || count(base) != 3 {
		t.Fatalf("Restore: %v", err)
	}

	if err := orm.ForceDelete(ctx, db, &bob); err != nil {
		t.Fatal(err)
	}

	if n, _ := orm.QueryOf[book](db).Count(ctx); n != 0 {
		t.Fatalf("books after cascade = %d", n)
	}

	if _, err := base.Find(ctx, bob.ID); !errors.Is(err, orm.ErrNotFound) {
		t.Fatalf("Find deleted = %v", err)
	}

	if n, err := base.Where("name", "Cid").Delete(ctx); err != nil || n != 1 || count(base) != 1 {
		t.Fatalf("query Delete = %d %v", n, err)
	}
}

func testTransactions(ctx context.Context, t *testing.T, db *database.DB) {
	t.Helper()

	boom := errors.New("boom")

	err := db.Tx(ctx, func(tx *database.Tx) error {
		a := author{Name: "InTx"}
		if err := orm.Create(ctx, tx, &a); err != nil {
			return err
		}

		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Tx error = %v", err)
	}

	if n, _ := orm.QueryOf[author](db).Where("name", "InTx").Count(ctx); n != 0 {
		t.Fatal("rolled back insert is visible")
	}

	err = db.Tx(ctx, func(tx *database.Tx) error {
		return database.RunInTx(ctx, tx, func(inner *database.Tx) error {
			a := author{Name: "Committed"}
			return orm.Create(ctx, inner, &a)
		})
	})
	if err != nil {
		t.Fatal(err)
	}

	if n, _ := orm.QueryOf[author](db).Where("name", "Committed").Count(ctx); n != 1 {
		t.Fatal("committed insert missing")
	}

	func() {
		defer func() { _ = recover() }()

		_ = db.Tx(ctx, func(tx *database.Tx) error {
			a := author{Name: "Panicked"}
			_ = orm.Create(ctx, tx, &a)
			panic("boom")
		})
	}()

	if n, _ := orm.QueryOf[author](db).Where("name", "Panicked").Count(ctx); n != 0 {
		t.Fatal("insert before panic is visible")
	}
}

func testAlter(ctx context.Context, t *testing.T, db *database.DB) {
	t.Helper()

	s := schema.New(ctx, db)

	if err := s.Create("dbt_tmp", func(t *schema.Table) {
		t.ID()
		t.String("code", 20)
		t.UniqueIndex("code")
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.Table("dbt_tmp", func(t *schema.Table) {
		t.Integer("qty").Default(0)
		t.RenameColumn("code", "sku")
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := db.ExecContext(ctx, "INSERT INTO dbt_tmp (sku, qty) VALUES ("+
		db.Dialect().Placeholder(1)+", "+db.Dialect().Placeholder(2)+")", "A1", 3); err != nil {
		t.Fatalf("insert into altered table: %v", err)
	}

	if _, err := db.ExecContext(
		ctx,
		"INSERT INTO dbt_tmp (sku) VALUES ("+db.Dialect().Placeholder(1)+")",
		"A1",
	); err == nil {
		t.Fatal("unique index lost after rename")
	}

	if err := s.Rename("dbt_tmp", "dbt_renamed"); err != nil {
		t.Fatal(err)
	}

	if has, err := s.HasTable("dbt_renamed"); err != nil || !has {
		t.Fatalf("renamed: has=%v err=%v", has, err)
	}

	if err := s.Drop("dbt_renamed"); err != nil {
		t.Fatal(err)
	}
}

// testPartialIndexRebind checks Index(...).Where on the dialects that have
// partial indexes (MySQL must refuse it) and runs the inserts through
// database.Rebind, including a "?" inside a string literal.
func testPartialIndexRebind(ctx context.Context, t *testing.T, db *database.DB) {
	t.Helper()

	s := schema.New(ctx, db)

	err := s.Create("dbt_reservations", func(t *schema.Table) {
		t.ID()
		t.Integer("number_id")
		t.String("status", 20)
		t.UniqueIndex("number_id").Where("status = 'live'")
	})
	if db.Dialect().Name() == "mysql" {
		if err == nil {
			_ = s.DropIfExists("dbt_reservations")
			t.Fatal("mysql accepted a partial index")
		}

		return
	}

	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = s.DropIfExists("dbt_reservations") })

	insert := database.Rebind(db.Dialect(),
		"INSERT INTO dbt_reservations (number_id, status) VALUES (?, CASE WHEN ? THEN 'live' ELSE 'gone?' END)")

	for _, live := range []bool{false, false, true} {
		if _, err := db.ExecContext(ctx, insert, 7, live); err != nil {
			t.Fatalf("insert live=%v: %v", live, err)
		}
	}

	if _, err := db.ExecContext(ctx, insert, 7, true); err == nil {
		t.Fatal("second live reservation accepted; partial unique index missing")
	}

	var n int
	if err := db.QueryRowContext(ctx, database.Rebind(
		db.Dialect(),
		"SELECT COUNT(*) FROM dbt_reservations WHERE status = 'gone?' AND number_id = ?",
	), 7).Scan(&n); err != nil || n != 2 {
		t.Fatalf("count = %d, %v, want 2", n, err)
	}
}
