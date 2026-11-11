package orm

import (
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/database"
)

const injection = `title" FROM qa_books; DROP TABLE qa_books; --`

func TestSecurity_A03_JoinRejectsUnknownColumn(t *testing.T) {
	t.Parallel()

	db, ctx := qaSetup(t)

	bad := []Query[qaBook]{
		QueryOf[qaBook](db).InnerJoin[qaAuthor]("qa_authors.id; DROP TABLE qa_books", "=", "qa_books.author_id"),
		QueryOf[qaBook](db).InnerJoin[qaAuthor]("qa_authors.id", "= 1 OR 1 =", "qa_books.author_id"),
		QueryOf[qaBook](db).InnerJoin[qaAuthor]("qa_authors.id", "=", "other_table.author_id"),
		QueryOf[qaBook](db).LeftJoin[qaAuthor]("qa_authors.nope", "=", "qa_books.author_id"),
		QueryOf[qaBook](db).JoinRelation(injection),
	}

	for i, q := range bad {
		if _, err := q.Get(ctx); err == nil {
			t.Errorf("join case %d was accepted", i)
		}
	}

	assertBooksIntact(t, db)
}

func TestSecurity_A03_GroupByRejectsUnknownColumn(t *testing.T) {
	t.Parallel()

	db, ctx := qaSetup(t)

	for _, col := range []string{injection, "nope", "other.title"} {
		if _, err := QueryOf[qaBook](db).SelectExpr(Count("*"), "n").GroupBy(col).Scan[struct{ N int64 }](
			ctx,
		); err == nil {
			t.Errorf("GroupBy(%q) was accepted", col)
		}
	}

	assertBooksIntact(t, db)
}

func TestSecurity_A03_SelectAsRejectsUnknownColumnAndAlias(t *testing.T) {
	t.Parallel()

	db, _ := qaSetup(t)

	for _, q := range []Query[qaBook]{
		QueryOf[qaBook](db).SelectAs(injection, "t"),
		QueryOf[qaBook](db).SelectAs("title", injection),
		QueryOf[qaBook](db).SelectAs("title", `a" b`),
		QueryOf[qaBook](db).SelectExpr(Sum(injection), "s"),
		QueryOf[qaBook](db).SelectExpr(Sum("views"), injection),
		QueryOf[qaBook](db).Having(Sum(injection), ">", 1),
	} {
		if q.err == nil {
			t.Error("a projection with an unvalidated identifier was accepted")
		}
	}
}

func TestSecurity_A03_WhereOuterRejectsUnknownColumn(t *testing.T) {
	t.Parallel()

	db, _ := qaSetup(t)

	for _, q := range []Query[qaReview]{
		QueryOf[qaReview](db).WhereOuter[qaBook](injection, "=", "id"),
		QueryOf[qaReview](db).WhereOuter[qaBook]("book_id", "=", injection),
		QueryOf[qaReview](db).WhereOuter[qaBook]("book_id", "= 1 OR 1 =", "id"),
		QueryOf[qaReview](db).WhereOuter[qaBook]("book_id", "=", "qa_authors.id"),
		QueryOf[qaReview](db).WhereOuter[qaReview]("book_id", "=", "id"),
	} {
		if q.err == nil {
			t.Error("WhereOuter accepted an unvalidated identifier")
		}
	}
}

func TestSecurity_A03_WhereHasRejectsUnknownRelation(t *testing.T) {
	t.Parallel()

	db, ctx := qaSetup(t)

	if _, err := QueryOf[qaBook](db).WhereHas[qaReview](injection, nil).Get(ctx); err == nil ||
		!strings.Contains(err.Error(), "no relation") {
		t.Errorf("WhereHas(injection) = %v", err)
	}

	if _, err := QueryOf[qaBook](db).WithCount(injection).Get(ctx); err == nil {
		t.Error("WithCount accepted an unknown relation")
	}

	if err := QueryOf[qaBook](db).With("Reviews." + injection).err; err == nil {
		t.Error("With accepted an unknown nested relation")
	}

	assertBooksIntact(t, db)
}

func TestSecurity_A03_UpsertRejectsUnknownColumns(t *testing.T) {
	t.Parallel()

	db, ctx := setup(t)

	u := User{Name: "Ana", Email: "ana@x.io"}

	for _, on := range []OnConflict{
		{},
		{Columns: []string{injection}},
		{Columns: []string{"email"}, Update: []string{injection}},
		{Columns: []string{"email"}, Update: []string{"id"}},
		{Columns: []string{"email"}, Update: []string{"created_at"}},
		{Columns: []string{"users.email"}},
	} {
		if err := Upsert(ctx, db, &u, on); err == nil {
			t.Errorf("Upsert accepted %+v", on)
		}
	}

	if n, err := QueryOf[User](db).Count(ctx); err != nil || n != 0 {
		t.Errorf("rows after the rejected upserts = %d, %v; want none written", n, err)
	}
}

func TestSecurity_A03_RawFragmentsCannotCarryMarkers(t *testing.T) {
	t.Parallel()

	db, _ := qaSetup(t)

	if err := QueryOf[qaBook](db).HavingRaw("COUNT(*) > \x01views\x02").err; err == nil {
		t.Error("HavingRaw accepted column marker bytes")
	}
}

func assertBooksIntact(t *testing.T, db *database.DB) {
	t.Helper()

	if n, err := QueryOf[qaBook](db).WithTrashed().Count(t.Context()); err != nil || n != 4 {
		t.Fatalf("qa_books has %d rows after the attempts (%v); want 4", n, err)
	}
}

func TestSecurity_A03_WhereRawRejectsColumnMarkers(t *testing.T) {
	t.Parallel()

	db, _ := qaSetup(t)

	frag := "views > \x01title\x02"

	if err := QueryOf[qaBook](db).WhereRaw(frag).err; err == nil {
		t.Error("WhereRaw accepted column marker bytes")
	}

	if err := QueryOf[qaBook](db).OrWhereRaw(frag).err; err == nil {
		t.Error("OrWhereRaw accepted column marker bytes")
	}
}
