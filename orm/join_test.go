package orm

import (
	"context"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/database"
)

type qaAuthor struct {
	ID   int64 `db:"id"`
	Name string

	Books HasMany[qaBook] `orm:"fk:author_id"`
}

func (qaAuthor) Table() string { return "qa_authors" }

type qaBook struct {
	SoftDeletes

	ID       int64 `db:"id"`
	AuthorID int64
	Title    string
	Views    int64

	Author      BelongsTo[qaAuthor] `orm:"fk:author_id"`
	Reviews     HasMany[qaReview]   `orm:"fk:book_id"`
	Tags        ManyToMany[qaTag]   `orm:"pivot:qa_book_tag,fk:book_id,rk:tag_id"`
	ReviewCount int64               `orm:"count:Reviews"`
	TagCount    int64               `orm:"count:Tags"`
}

func (qaBook) Table() string { return "qa_books" }

type qaReview struct {
	ID         int64 `db:"id"`
	BookID     int64
	ReviewerID int64
	Body       string

	Reviewer BelongsTo[qaAuthor] `orm:"fk:reviewer_id"`
}

func (qaReview) Table() string { return "qa_reviews" }

type qaTag struct {
	ID    int64 `db:"id"`
	Label string
}

func (qaTag) Table() string { return "qa_tags" }

const qaDDL = `
CREATE TABLE qa_authors (id INTEGER PRIMARY KEY, name TEXT NOT NULL);
CREATE TABLE qa_books (id INTEGER PRIMARY KEY, deleted_at TIMESTAMP, author_id INTEGER NOT NULL, title TEXT NOT NULL, views INTEGER NOT NULL);
CREATE TABLE qa_reviews (id INTEGER PRIMARY KEY, book_id INTEGER NOT NULL, reviewer_id INTEGER NOT NULL, body TEXT NOT NULL);
CREATE TABLE qa_tags (id INTEGER PRIMARY KEY, label TEXT NOT NULL);
CREATE TABLE qa_book_tag (book_id INTEGER NOT NULL, tag_id INTEGER NOT NULL);
INSERT INTO qa_authors VALUES (1, 'Ada'), (2, 'Bob'), (3, 'Cy');
INSERT INTO qa_books VALUES (1, NULL, 1, 'Go', 100), (2, NULL, 1, 'SQL', 50), (3, NULL, 2, 'Zig', 10), (4, '2026-01-01 00:00:00', 2, 'Old', 999);
INSERT INTO qa_reviews VALUES (1, 1, 2, 'good'), (2, 1, 3, 'great'), (3, 3, 1, 'fine'), (4, 2, 3, '');
INSERT INTO qa_tags VALUES (1, 'a'), (2, 'b');
INSERT INTO qa_book_tag VALUES (1, 1), (1, 2), (2, 2);
`

func qaSetup(tb testing.TB) (*database.DB, context.Context) {
	tb.Helper()

	db, ctx := setup(tb)

	for stmt := range strings.SplitSeq(qaDDL, ";\n") {
		if strings.TrimSpace(stmt) == "" {
			continue
		}

		if _, err := db.ExecContext(ctx, stmt); err != nil {
			tb.Fatalf("%s: %v", stmt, err)
		}
	}

	return db, ctx
}

func titles(books []qaBook) []string {
	out := make([]string, len(books))
	for i := range books {
		out[i] = books[i].Title
	}

	return out
}

// Queries without a join keep the exact SQL they had before joins existed.
func TestQuerySQLWithoutJoinsIsUnchanged(t *testing.T) {
	t.Parallel()

	pg := fakeEx{database.Postgres}
	tests := []struct {
		name string
		q    interface{ SQL() (string, []any, error) }
		want string
	}{
		{
			"where",
			QueryOf[User](pg).Where("age", ">", 3),
			`SELECT "id", "created_at", "updated_at", "name", "email", "age", "active", "settings" FROM "users" WHERE "age" > $1`,
		},
		{
			"where in",
			QueryOf[User](pg).Select("id").WhereIn("id", 1, 2),
			`SELECT "id" FROM "users" WHERE "id" IN ($1, $2)`,
		},
		{
			"order by",
			QueryOf[User](pg).Select("id").OrderBy("name", Desc),
			`SELECT "id" FROM "users" ORDER BY "name" DESC`,
		},
		{
			"soft delete",
			QueryOf[Post](pg).Select("id").Where("title", "x"),
			`SELECT "id" FROM "posts" WHERE "deleted_at" IS NULL AND "title" = $1`,
		},
		{"offset", QueryOf[User](pg).Select("id").Limit(5).Offset(10), `SELECT "id" FROM "users" LIMIT 5 OFFSET 10`},
		{"lock", QueryOf[User](pg).Select("id").LockForUpdate(), `SELECT "id" FROM "users" FOR UPDATE`},
	}

	for _, tt := range tests {
		got, _, err := tt.q.SQL()
		if err != nil || got != tt.want {
			t.Errorf("%s: SQL() = %q, %v\nwant %q", tt.name, got, err, tt.want)
		}
	}
}

func TestJoin(t *testing.T) {
	t.Parallel()

	db, ctx := qaSetup(t)

	books, err := QueryOf[qaBook](db).
		InnerJoin[qaAuthor]("qa_authors.id", "=", "qa_books.author_id").
		Where("qa_authors.name", "Ada").
		OrderBy("qa_books.id", Desc).
		Get(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff([]string{"SQL", "Go"}, titles(books)); diff != "" {
		t.Errorf("inner join (-want +got):\n%s", diff)
	}

	// Cy wrote no book: the left join keeps her side, the inner join drops it.
	type pair struct {
		Name  string
		Title *string
	}

	rows, err := QueryOf[qaAuthor](db).
		LeftJoin[qaBook]("qa_books.author_id", "=", "qa_authors.id").
		SelectAs("qa_authors.name", "name").SelectAs("qa_books.title", "title").
		OrderBy("qa_authors.id", Asc).OrderBy("qa_books.id", Asc).
		Scan[pair](ctx)
	if err != nil {
		t.Fatal(err)
	}

	if len(rows) != 4 || rows[3].Name != "Cy" || rows[3].Title != nil {
		t.Errorf(
			"left join rows = %+v, want 4 rows ending with Cy without a title (the trashed book is not joined)",
			rows,
		)
	}

	// Relations name the keys.
	for name, want := range map[string][]string{
		"Author":  {"Go", "SQL", "Zig"},
		"Reviews": {"Go", "Go", "SQL", "Zig"},
		"Tags":    {"Go", "Go", "SQL"},
	} {
		got, err := QueryOf[qaBook](db).JoinRelation(name).OrderBy("qa_books.id", Asc).Get(ctx)
		if err != nil || !cmp.Equal(titles(got), want) {
			t.Errorf("JoinRelation(%s) = %v, %v; want %v", name, titles(got), err, want)
		}
	}

	// Distinct collapses the repeated rows of a "many" relation.
	if n, err := QueryOf[qaBook](db).JoinRelation("Reviews").Distinct().Count(ctx); err != nil || n != 3 {
		t.Errorf("Distinct join Count = %d, %v; want 3", n, err)
	}
}

func TestJoinSQL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		d    database.Dialect
		want string
	}{
		{
			"postgres",
			database.Postgres,
			`SELECT "qa_books"."deleted_at", "qa_books"."id", "qa_books"."author_id", "qa_books"."title", "qa_books"."views" ` +
				`FROM "qa_books" INNER JOIN "qa_authors" ON "qa_authors"."id" = "qa_books"."author_id" ` +
				`WHERE "qa_books"."deleted_at" IS NULL AND "qa_authors"."name" = $1 ORDER BY "qa_books"."id" DESC`,
		},
		{
			"mysql",
			database.MySQL,
			"SELECT `qa_books`.`deleted_at`, `qa_books`.`id`, `qa_books`.`author_id`, `qa_books`.`title`, `qa_books`.`views` " +
				"FROM `qa_books` INNER JOIN `qa_authors` ON `qa_authors`.`id` = `qa_books`.`author_id` " +
				"WHERE `qa_books`.`deleted_at` IS NULL AND `qa_authors`.`name` = ? ORDER BY `qa_books`.`id` DESC",
		},
	}

	for _, tt := range tests {
		got, _, err := QueryOf[qaBook](fakeEx{tt.d}).
			InnerJoin[qaAuthor]("qa_authors.id", "=", "qa_books.author_id").
			Where("qa_authors.name", "Ada").OrderBy("id", Desc).SQL()
		if err != nil || got != tt.want {
			t.Errorf("%s: SQL() = %q, %v\nwant %q", tt.name, got, err, tt.want)
		}
	}
}

func TestJoinErrors(t *testing.T) {
	t.Parallel()

	db, ctx := qaSetup(t)

	tests := []struct {
		name string
		q    Query[qaBook]
		want string
	}{
		{"same table twice", QueryOf[qaBook](db).
			InnerJoin[qaAuthor]("qa_authors.id", "=", "qa_books.author_id").
			LeftJoin[qaAuthor]("qa_authors.id", "=", "qa_books.author_id"), "already joined"},
		{
			"self join",
			QueryOf[qaBook](db).InnerJoin[qaBook]("qa_books.id", "=", "qa_books.id"),
			"self joins need orm.Raw",
		},
		{"unknown relation", QueryOf[qaBook](db).JoinRelation("Nope"), `no relation "Nope"`},
	}

	for _, tt := range tests {
		if _, err := tt.q.Get(ctx); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: Get = %v, want an error containing %q", tt.name, err, tt.want)
		}
	}

	joined := QueryOf[qaBook](db).InnerJoin[qaAuthor](
		"qa_authors.id",
		"=",
		"qa_books.author_id",
	).Where("qa_authors.name", "Ada")
	if _, err := joined.Update(
		ctx,
		Set{"views": 1},
	); err == nil ||
		!strings.Contains(err.Error(), "does not support joins") {
		t.Errorf("Update with a join = %v", err)
	}

	if _, err := joined.Delete(ctx); err == nil || !strings.Contains(err.Error(), "does not support joins") {
		t.Errorf("Delete with a join = %v", err)
	}

	if _, err := joined.CursorPaginate(
		ctx,
		"",
		2,
		Asc,
	); err == nil ||
		!strings.Contains(err.Error(), "CursorPaginate does not support") {
		t.Errorf("CursorPaginate with a join = %v", err)
	}

	if _, err := QueryOf[qaBook](db).InnerJoin[qaAuthor](
		"qa_authors.id",
		"=",
		"qa_books.author_id",
	).First(ctx); err != nil {
		t.Errorf("First with a join = %v", err)
	}
}
