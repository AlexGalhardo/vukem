package orm

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/database"
)

func TestWhereExists(t *testing.T) {
	t.Parallel()

	db, ctx := qaSetup(t)

	hasReviews := QueryOf[qaReview](db).WhereOuter[qaBook]("book_id", "=", "id")

	got, err := QueryOf[qaBook](db).WhereExists(hasReviews).OrderBy("id", Asc).Get(ctx)
	if err != nil || !cmp.Equal(titles(got), []string{"Go", "SQL", "Zig"}) {
		t.Errorf("WhereExists = %v, %v", titles(got), err)
	}

	got, err = QueryOf[qaBook](db).WhereNotExists(hasReviews).Get(ctx)
	if err != nil || len(got) != 0 {
		t.Errorf("WhereNotExists = %v, %v; want none (every live book has a review)", titles(got), err)
	}

	bodyBlank := QueryOf[qaReview](db).WhereOuter[qaBook]("book_id", "=", "id").Where("body", "")

	got, err = QueryOf[qaBook](db).WhereExists(bodyBlank).Get(ctx)
	if err != nil || !cmp.Equal(titles(got), []string{"SQL"}) {
		t.Errorf("WhereExists with a filter = %v, %v", titles(got), err)
	}
}

func TestWhereInQuery(t *testing.T) {
	t.Parallel()

	db, ctx := qaSetup(t)

	cheap := QueryOf[qaBook](db).Where("views", "<", 60).Select("author_id")

	got, err := QueryOf[qaAuthor](db).WhereInQuery("id", cheap).OrderBy("id", Asc).Get(ctx)
	if err != nil || len(got) != 2 || got[0].Name != "Ada" || got[1].Name != "Bob" {
		t.Errorf("WhereInQuery = %+v, %v", got, err)
	}

	got, err = QueryOf[qaAuthor](db).WhereNotInQuery("id", cheap).Get(ctx)
	if err != nil || len(got) != 1 || got[0].Name != "Cy" {
		t.Errorf("WhereNotInQuery = %+v, %v", got, err)
	}

	two := QueryOf[qaBook](db).Select("author_id", "views")
	if _, err := QueryOf[qaAuthor](db).WhereInQuery("id", two).Get(ctx); err == nil ||
		!strings.Contains(err.Error(), "exactly one column") {
		t.Errorf("WhereInQuery with two columns = %v", err)
	}
}

func TestSubqueryArgumentOrder(t *testing.T) {
	t.Parallel()

	pg := fakeEx{database.Postgres}

	sub := QueryOf[qaBook](pg).Select("author_id").Where("views", ">", 5).Where("title", "x")

	got, args, err := QueryOf[qaAuthor](pg).Where("name", "a").WhereInQuery("id", sub).Where("id", ">", 0).SQL()
	if err != nil {
		t.Fatal(err)
	}

	want := `SELECT "id", "name" FROM "qa_authors" WHERE "name" = $1 AND "id" IN ` +
		`(SELECT "author_id" FROM "qa_books" WHERE "deleted_at" IS NULL AND "views" > $2 AND "title" = $3) AND "id" > $4`
	if got != want {
		t.Errorf("SQL() = %q\nwant %q", got, want)
	}

	if diff := cmp.Diff([]any{"a", 5, "x", 0}, args); diff != "" {
		t.Errorf("args (-want +got):\n%s", diff)
	}
}

func TestWhereColumn(t *testing.T) {
	t.Parallel()

	db, ctx := qaSetup(t)

	if _, err := db.ExecContext(ctx, `UPDATE qa_books SET views = author_id WHERE id = 1`); err != nil {
		t.Fatal(err)
	}

	got, err := QueryOf[qaBook](db).WhereColumn("views", "=", "author_id").Get(ctx)
	if err != nil || len(got) != 1 || got[0].ID != 1 {
		t.Errorf("WhereColumn = %v, %v", titles(got), err)
	}
}

func TestSubqueryErrorPropagates(t *testing.T) {
	t.Parallel()

	db, ctx := qaSetup(t)

	bad := QueryOf[qaBook](db).Where("nope", 1)
	if _, err := QueryOf[qaAuthor](db).WhereExists(bad).Get(ctx); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("error from the subquery = %v", err)
	}
}
