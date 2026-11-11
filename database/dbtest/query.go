package dbtest

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/schema"
)

// qAuthor, qBook, qReview and qTag exercise the query builder; their tables
// carry the dbt_q prefix so they never meet the relation suite's tables.
type qAuthor struct {
	ID   int64 `db:"id"`
	Name string
}

// Table implements orm.Tabler.
func (qAuthor) Table() string { return "dbt_qauthors" }

type qBook struct {
	orm.SoftDeletes

	ID       int64 `db:"id"`
	AuthorID int64
	Title    string
	Views    int64

	Author      orm.BelongsTo[qAuthor] `orm:"fk:author_id"`
	Reviews     orm.HasMany[qReview]   `orm:"fk:book_id"`
	Tags        orm.ManyToMany[qTag]   `orm:"pivot:dbt_qbook_tag,fk:book_id,rk:tag_id"`
	ReviewCount int64                  `orm:"count:Reviews"`
}

// Table implements orm.Tabler.
func (qBook) Table() string { return "dbt_qbooks" }

type qReview struct {
	ID         int64 `db:"id"`
	BookID     int64
	ReviewerID int64
	Body       string

	Reviewer orm.BelongsTo[qAuthor] `orm:"fk:reviewer_id"`
}

// Table implements orm.Tabler.
func (qReview) Table() string { return "dbt_qreviews" }

type qTag struct {
	ID    int64 `db:"id"`
	Label string
}

// Table implements orm.Tabler.
func (qTag) Table() string { return "dbt_qtags" }

type qTotal struct {
	Name  string
	Total int64
}

func qTitles(books []qBook) []string {
	out := make([]string, len(books))
	for i := range books {
		out[i] = books[i].Title
	}

	return out
}

func mustOK(t *testing.T, what string, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

func expectEqual[V any](t *testing.T, what string, want, got V) {
	t.Helper()

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("%s (-want +got):\n%s", what, diff)
	}
}

// testQueryFeatures runs the join, aggregate, subquery, relation filter,
// nested eager loading and raw SQL features on every dialect.
func testQueryFeatures(ctx context.Context, t *testing.T, db *database.DB) {
	t.Helper()

	s := schema.New(ctx, db)

	mustOK(t, "create authors", s.Create("dbt_qauthors", func(t *schema.Table) {
		t.ID()
		t.String("name")
	}))
	mustOK(t, "create books", s.Create("dbt_qbooks", func(t *schema.Table) {
		t.ID()
		t.SoftDeletes()
		t.ForeignID("author_id")
		t.String("title")
		t.BigInteger("views")
	}))
	mustOK(t, "create reviews", s.Create("dbt_qreviews", func(t *schema.Table) {
		t.ID()
		t.ForeignID("book_id")
		t.ForeignID("reviewer_id")
		t.String("body")
	}))
	mustOK(t, "create tags", s.Create("dbt_qtags", func(t *schema.Table) {
		t.ID()
		t.String("label")
	}))
	mustOK(t, "create pivot", s.Create("dbt_qbook_tag", func(t *schema.Table) {
		t.ForeignID("book_id")
		t.ForeignID("tag_id")
	}))

	authors := []qAuthor{{Name: "Ada"}, {Name: "Bob"}, {Name: "Cy"}}
	mustOK(t, "authors", orm.CreateMany(ctx, db, authors))

	books := []qBook{
		{AuthorID: authors[0].ID, Title: "Go", Views: 100},
		{AuthorID: authors[0].ID, Title: "SQL", Views: 50},
		{AuthorID: authors[1].ID, Title: "Zig", Views: 10},
		{AuthorID: authors[1].ID, Title: "Old", Views: 999},
	}
	for i := range books {
		mustOK(t, "book", orm.Create(ctx, db, &books[i]))
	}

	mustOK(t, "trash a book", orm.Delete(ctx, db, &books[3]))

	reviews := []qReview{
		{BookID: books[0].ID, ReviewerID: authors[1].ID, Body: "good"},
		{BookID: books[0].ID, ReviewerID: authors[2].ID, Body: "great"},
		{BookID: books[2].ID, ReviewerID: authors[0].ID, Body: "fine"},
	}
	mustOK(t, "reviews", orm.CreateMany(ctx, db, reviews))

	tags := []qTag{{Label: "a"}, {Label: "b"}}
	mustOK(t, "tags", orm.CreateMany(ctx, db, tags))
	mustOK(t, "tag book 1", orm.Attach(ctx, db, &books[0], "Tags", tags[0].ID, tags[1].ID))
	mustOK(t, "tag book 2", orm.Attach(ctx, db, &books[1], "Tags", tags[1].ID))

	q := func() orm.Query[qBook] { return orm.QueryOf[qBook](db) }

	t.Run("InnerJoin", func(t *testing.T) {
		got, err := q().InnerJoin[qAuthor]("dbt_qauthors.id", "=", "dbt_qbooks.author_id").
			Where("dbt_qauthors.name", "Ada").OrderBy("dbt_qbooks.id", orm.Desc).Get(ctx)
		mustOK(t, "inner join", err)
		expectEqual(t, "titles", []string{"SQL", "Go"}, qTitles(got))
	})

	t.Run("LeftJoin", func(t *testing.T) {
		type pair struct {
			Name  string
			Title *string
		}

		got, err := orm.QueryOf[qAuthor](db).
			LeftJoin[qBook]("dbt_qbooks.author_id", "=", "dbt_qauthors.id").
			SelectAs("dbt_qauthors.name", "name").SelectAs("dbt_qbooks.title", "title").
			OrderBy("dbt_qauthors.id", orm.Asc).OrderBy("dbt_qbooks.id", orm.Asc).
			Scan[pair](ctx)
		mustOK(t, "left join", err)

		if len(got) != 4 || got[3].Name != "Cy" || got[3].Title != nil {
			t.Errorf("left join rows = %+v, want 4 ending with Cy without a title", got)
		}
	})

	t.Run("JoinRelationDistinct", func(t *testing.T) {
		n, err := q().JoinRelation("Reviews").Distinct().Count(ctx)
		mustOK(t, "distinct count", err)

		if n != 2 {
			t.Errorf("Distinct join Count = %d, want 2", n)
		}
	})

	t.Run("GroupByHavingScan", func(t *testing.T) {
		got, err := q().InnerJoin[qAuthor]("dbt_qauthors.id", "=", "dbt_qbooks.author_id").
			SelectAs("dbt_qauthors.name", "name").SelectExpr(orm.Sum("dbt_qbooks.views"), "total").
			GroupBy("dbt_qauthors.name").Having(orm.Sum("dbt_qbooks.views"), ">", 20).
			OrderBy("dbt_qauthors.name", orm.Asc).Scan[qTotal](ctx)
		mustOK(t, "group by", err)
		expectEqual(t, "totals", []qTotal{{"Ada", 150}}, got)
	})

	t.Run("Aggregates", func(t *testing.T) {
		sum, err := q().Sum[int64](ctx, "views")
		mustOK(t, "sum", err)

		avg, err := q().Avg(ctx, "views")
		mustOK(t, "avg", err)

		lo, err := q().Min[int64](ctx, "views")
		mustOK(t, "min", err)

		hi, err := q().Max[int64](ctx, "views")
		mustOK(t, "max", err)

		if sum != 160 || avg < 53.3 || avg > 53.4 || lo != 10 || hi != 100 {
			t.Errorf("sum %d avg %v min %d max %d; want 160, ~53.33, 10, 100", sum, avg, lo, hi)
		}

		if _, err := q().Where("views", ">", 5000).Max[int64](ctx, "views"); !errors.Is(err, orm.ErrNotFound) {
			t.Errorf("Max of nothing = %v, want ErrNotFound", err)
		}
	})

	t.Run("Distinct", func(t *testing.T) {
		n, err := q().Select("author_id").Distinct().Count(ctx)
		mustOK(t, "distinct count", err)

		if n != 2 {
			t.Errorf("Distinct Count = %d, want 2", n)
		}
	})

	t.Run("WhereExists", func(t *testing.T) {
		has := orm.QueryOf[qReview](db).WhereOuter[qBook]("book_id", "=", "id")

		got, err := q().WhereExists(has).OrderBy("id", orm.Asc).Get(ctx)
		mustOK(t, "exists", err)
		expectEqual(t, "with reviews", []string{"Go", "Zig"}, qTitles(got))

		got, err = q().WhereNotExists(has).Get(ctx)
		mustOK(t, "not exists", err)
		expectEqual(t, "without reviews", []string{"SQL"}, qTitles(got))
	})

	t.Run("WhereInQuery", func(t *testing.T) {
		cheap := q().Where("views", "<", 60).Select("author_id")

		got, err := orm.QueryOf[qAuthor](db).WhereInQuery("id", cheap).OrderBy("id", orm.Asc).Get(ctx)
		mustOK(t, "in query", err)

		if len(got) != 2 || got[0].Name != "Ada" || got[1].Name != "Bob" {
			t.Errorf("WhereInQuery = %+v", got)
		}
	})

	t.Run("WhereHas", func(t *testing.T) {
		got, err := q().WhereHas[qTag](
			"Tags",
			func(x orm.Query[qTag]) orm.Query[qTag] { return x.Where("label", "a") },
		).Get(ctx)
		mustOK(t, "has", err)
		expectEqual(t, "tagged a", []string{"Go"}, qTitles(got))

		got, err = q().WhereDoesntHave[qTag]("Tags", nil).Get(ctx)
		mustOK(t, "doesn't have", err)
		expectEqual(t, "untagged", []string{"Zig"}, qTitles(got))
	})

	t.Run("WithCount", func(t *testing.T) {
		got, err := q().WithCount("Reviews").OrderBy("id", orm.Asc).Get(ctx)
		mustOK(t, "with count", err)

		var counts []int64
		for i := range got {
			counts = append(counts, got[i].ReviewCount)
		}

		expectEqual(t, "review counts", []int64{2, 0, 1}, counts)
	})

	t.Run("NestedWith", func(t *testing.T) {
		got, err := q().With("Reviews.Reviewer").OrderBy("id", orm.Asc).Get(ctx)
		mustOK(t, "nested with", err)

		revs, err := got[0].Reviews.Get()
		mustOK(t, "reviews", err)

		who, err := revs[0].Reviewer.Get()
		if err != nil || who == nil || who.Name != "Bob" {
			t.Errorf("first reviewer = %+v, %v; want Bob", who, err)
		}
	})

	t.Run("Raw", func(t *testing.T) {
		type row struct {
			Title string
			Views int64
		}

		got, err := orm.Raw[row](
			ctx,
			db,
			`SELECT title, views FROM dbt_qbooks WHERE views > ? AND deleted_at IS NULL ORDER BY views DESC`,
			20,
		)
		mustOK(t, "raw", err)
		expectEqual(t, "raw rows", []row{{"Go", 100}, {"SQL", 50}}, got)
	})
}

// upsertRow has a unique code the upsert resolves conflicts on.
type upsertRow struct {
	ID    int64 `db:"id"`
	Code  string
	Value string
}

// Table implements orm.Tabler.
func (upsertRow) Table() string { return "dbt_upserts" }

// testUpsert checks Upsert and UpsertMany on every dialect: insert, update
// by unique column keeping the id, DoNothing, and a batch with one existing
// row.
func testUpsert(ctx context.Context, t *testing.T, db *database.DB) {
	t.Helper()

	mustOK(t, "create", schema.New(ctx, db).Create("dbt_upserts", func(t *schema.Table) {
		t.ID()
		t.String("code").Unique()
		t.String("value")
	}))

	on := orm.OnConflict{Columns: []string{"code"}, Update: []string{"value"}}

	first := upsertRow{Code: "a", Value: "one"}
	mustOK(t, "first upsert", orm.Upsert(ctx, db, &first, on))

	if first.ID == 0 {
		t.Fatal("the generated id was not written back")
	}

	again := upsertRow{Code: "a", Value: "two"}
	mustOK(t, "second upsert", orm.Upsert(ctx, db, &again, on))

	if again.ID != first.ID {
		t.Errorf("upsert of an existing row set id %d, want %d", again.ID, first.ID)
	}

	rows, err := orm.QueryOf[upsertRow](db).Get(ctx)
	mustOK(t, "read", err)

	if len(rows) != 1 || rows[0].Value != "two" {
		t.Fatalf("rows = %+v, want one row with the new value", rows)
	}

	keep := upsertRow{Code: "a", Value: "three"}
	mustOK(t, "do nothing", orm.Upsert(ctx, db, &keep, orm.OnConflict{Columns: []string{"code"}, DoNothing: true}))

	if row, _ := orm.QueryOf[upsertRow](db).Find(ctx, first.ID); row.Value != "two" {
		t.Errorf("DoNothing changed the value to %q", row.Value)
	}

	mustOK(t, "upsert many", orm.UpsertMany(ctx, db, []upsertRow{
		{Code: "a", Value: "x"}, {Code: "b", Value: "y"}, {Code: "c", Value: "z"},
	}, on))

	values, err := orm.QueryOf[upsertRow](db).OrderBy("code", orm.Asc).Pluck[string](ctx, "value")
	mustOK(t, "pluck", err)
	expectEqual(t, "values after the batch", []string{"x", "y", "z"}, values)

	testUpsertNeverRevivesTrashedRows(ctx, t, db)
}

// trashRow is a soft-deleted model with a unique code.
type trashRow struct {
	orm.SoftDeletes

	ID    int64 `db:"id"`
	Code  string
	Value string
}

// Table implements orm.Tabler.
func (trashRow) Table() string { return "dbt_trash_upserts" }

// testUpsertNeverRevivesTrashedRows checks that Upsert leaves a trashed row
// alone and reports a conflict, on every dialect.
func testUpsertNeverRevivesTrashedRows(ctx context.Context, t *testing.T, db *database.DB) {
	t.Helper()

	mustOK(t, "create", schema.New(ctx, db).Create("dbt_trash_upserts", func(t *schema.Table) {
		t.ID()
		t.SoftDeletes()
		t.String("code").Unique()
		t.String("value")
	}))

	on := orm.OnConflict{Columns: []string{"code"}, Update: []string{"value"}}

	row := trashRow{Code: "a", Value: "one"}
	mustOK(t, "insert", orm.Upsert(ctx, db, &row, on))
	mustOK(t, "trash", orm.Delete(ctx, db, &row))

	for name, c := range map[string]orm.OnConflict{
		"update": on, "default": {Columns: []string{"code"}}, "do nothing": {Columns: []string{"code"}, DoNothing: true},
	} {
		again := trashRow{Code: "a", Value: "revived"}
		if err := orm.Upsert(ctx, db, &again, c); !errors.Is(err, orm.ErrConflict) {
			t.Errorf("%s: Upsert over a trashed row = %v, want ErrConflict", name, err)
		}
	}

	if err := orm.UpsertMany(ctx, db, []trashRow{{Code: "a", Value: "revived"}}, on); !errors.Is(err, orm.ErrConflict) {
		t.Errorf("UpsertMany over a trashed row = %v, want ErrConflict", err)
	}

	got, err := orm.QueryOf[trashRow](db).OnlyTrashed().Where("code", "a").First(ctx)
	if err != nil || got.Value != "one" {
		t.Errorf("trashed row = %+v, %v; want it untouched", got, err)
	}

	mustOK(t, "restore", orm.Restore(ctx, db, &row))

	live := trashRow{Code: "a", Value: "two"}
	mustOK(t, "upsert after restore", orm.Upsert(ctx, db, &live, on))

	if live.ID != row.ID {
		t.Errorf("upsert of the restored row set id %d, want %d", live.ID, row.ID)
	}
}
