package dbtest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/schema"
)

// Tag relates to books through the dbt_book_tag pivot table.
type Tag struct {
	orm.Model

	Name string `json:"name"`
}

// Table implements orm.Tabler.
func (Tag) Table() string { return "dbt_tags" }

var tagsMigration = schema.Migration{
	ID: "dbt_3_tags",
	Up: func(s *schema.Schema) error {
		if err := s.Create("dbt_tags", func(t *schema.Table) {
			t.ID()
			t.String("name").Unique()
			t.Timestamps()
		}); err != nil {
			return err
		}

		return s.Create("dbt_book_tag", func(t *schema.Table) {
			t.ForeignID("book_id").Constrained("dbt_books").CascadeOnDelete()
			t.ForeignID("tag_id").Constrained("dbt_tags").CascadeOnDelete()
			t.UniqueIndex("book_id", "tag_id")
		})
	},
	Down: func(s *schema.Schema) error {
		if err := s.DropIfExists("dbt_book_tag"); err != nil {
			return err
		}

		return s.DropIfExists("dbt_tags")
	},
}

// counter counts queries to assert eager-loading costs.
type counter struct {
	*database.DB

	n int
}

func (c *counter) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	c.n++
	return c.DB.QueryContext(ctx, q, args...)
}

func (c *counter) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	c.n++
	return c.DB.QueryRowContext(ctx, q, args...)
}

func create[T any](ctx context.Context, t *testing.T, db *database.DB, items []T) {
	t.Helper()

	for i := range items {
		if err := orm.Create(ctx, db, &items[i]); err != nil {
			t.Fatal(err)
		}
	}
}

func testRelations(ctx context.Context, t *testing.T, db *database.DB) {
	t.Helper()

	authors := []Author{{Name: "Rel A"}, {Name: "Rel B"}, {Name: "Rel C"}}
	create(ctx, t, db, authors)

	books := []Book{
		{AuthorID: authors[0].ID, Title: "A1"},
		{AuthorID: authors[0].ID, Title: "A2"},
		{AuthorID: authors[1].ID, Title: "B1"},
	}
	create(ctx, t, db, books)

	tags := []Tag{{Name: "go"}, {Name: "sql"}, {Name: "web"}}
	create(ctx, t, db, tags)

	// Pivot writes: duplicates are skipped, Sync replaces, Detach clears.
	for _, step := range []error{
		orm.Attach(ctx, db, &books[0], "Tags", tags[0].ID, tags[1].ID, tags[0].ID),
		orm.Sync(ctx, db, &books[1], "Tags", tags[1].ID, tags[2].ID),
		orm.Sync(ctx, db, &books[1], "Tags", tags[2].ID),
		orm.Attach(ctx, db, &books[2], "Tags", tags[0].ID),
		orm.Detach(ctx, db, &books[2], "Tags"),
	} {
		if step != nil {
			t.Fatal(step)
		}
	}

	testHasMany(ctx, t, db)
	testBelongsToAndPivot(ctx, t, db, books)

	// Strict mode: relations are never loaded lazily.
	plain, err := orm.QueryOf[Book](db).Find(ctx, books[0].ID)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := plain.Author.Get(); !errors.Is(err, orm.ErrNotLoaded) {
		t.Fatalf("unloaded relation = %v", err)
	}

	// JSON includes loaded relations only.
	withAuthor, err := orm.QueryOf[Book](db).With("Author").Find(ctx, books[0].ID)
	if err != nil {
		t.Fatal(err)
	}

	keys := func(v any) map[string]any {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}

		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}

		return m
	}

	loaded, bare := keys(withAuthor), keys(plain)
	author, _ := loaded["author"].(map[string]any)

	_, hasTags := loaded["tags"]
	_, bareAuthor := bare["author"]
	_, deletedAt := author["deleted_at"]

	if author == nil || hasTags || bareAuthor || deletedAt {
		t.Fatalf("json = %v / %v", loaded, bare)
	}

	// Soft-deleted related rows are not loaded.
	if err := orm.Delete(ctx, db, &authors[1]); err != nil {
		t.Fatal(err)
	}

	b, err := orm.QueryOf[Book](db).With("Author").Find(ctx, books[2].ID)
	if err != nil {
		t.Fatal(err)
	}

	if a, err := b.Author.Get(); err != nil || a != nil {
		t.Fatalf("soft-deleted owner loaded: %v %v", a, err)
	}

	if _, err := orm.QueryOf[Book](db).With("Nope").Get(ctx); err == nil {
		t.Fatal("unknown relation accepted")
	}

	if err := orm.Attach(ctx, db, &authors[0], "Books", 1); err == nil {
		t.Fatal("Attach on a has-many relation accepted")
	}

	if err := orm.Attach(ctx, db, &Book{}, "Tags", 1); err == nil {
		t.Fatal("Attach on an unsaved model accepted")
	}
}

func titles(bs []Book) []string {
	out := make([]string, len(bs))
	for i := range bs {
		out[i] = bs[i].Title
	}

	return out
}

// testHasMany: authors + 2 relations cost 3 queries.
func testHasMany(ctx context.Context, t *testing.T, db *database.DB) {
	t.Helper()

	c := &counter{DB: db}

	got, err := orm.QueryOf[Author](c).WhereIn("name", "Rel A", "Rel B", "Rel C").OrderBy("id", orm.Asc).
		With("Books", "FirstBook").Get(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if c.n != 3 {
		t.Errorf("queries for authors + 2 relations = %d, want 3", c.n)
	}

	a0, _ := got[0].Books.Get()
	a2, _ := got[2].Books.Get()
	first, _ := got[0].FirstBook.Get()
	none, _ := got[2].FirstBook.Get()

	if !slices.Equal(titles(a0), []string{"A1", "A2"}) || len(a2) != 0 || first == nil || first.Title != "A1" ||
		none != nil {
		t.Fatalf("has-many/has-one = %v %v %v %v", titles(a0), a2, first, none)
	}
}

// testBelongsToAndPivot: books + belongs-to (1) + many-to-many (pivot,
// then tags) cost 4 queries.
func testBelongsToAndPivot(ctx context.Context, t *testing.T, db *database.DB, books []Book) {
	t.Helper()

	c := &counter{DB: db}

	bs, err := orm.QueryOf[Book](c).WhereIn("id", books[0].ID, books[1].ID, books[2].ID).OrderBy("id", orm.Asc).
		With("Author", "Tags").Get(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if c.n != 4 {
		t.Errorf("queries for books + belongs-to + many-to-many = %d, want 4", c.n)
	}

	tagNames := func(b Book) []string {
		ts, err := b.Tags.Get()
		if err != nil {
			t.Fatal(err)
		}

		out := make([]string, len(ts))
		for i := range ts {
			out[i] = ts[i].Name
		}

		slices.Sort(out)

		return out
	}

	owner, _ := bs[2].Author.Get()
	if owner == nil || owner.Name != "Rel B" ||
		!slices.Equal(tagNames(bs[0]), []string{"go", "sql"}) ||
		!slices.Equal(tagNames(bs[1]), []string{"web"}) || len(tagNames(bs[2])) != 0 {
		t.Fatalf("belongs-to/many-to-many = %v %v %v %v", owner, tagNames(bs[0]), tagNames(bs[1]), tagNames(bs[2]))
	}
}
