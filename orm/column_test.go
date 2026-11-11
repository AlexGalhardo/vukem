package orm

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/database"
)

// The generated shape: one variable per table with typed columns.
var userCols = struct {
	ID    Column[User, int64]
	Name  Column[User, string]
	Age   Column[User, int]
	Email Column[User, string]
}{
	ID:    Col[User, int64]("id"),
	Name:  Col[User, string]("name"),
	Age:   Col[User, int]("age"),
	Email: Col[User, string]("email"),
}

func TestColumnMatchesTheStringAPI(t *testing.T) {
	t.Parallel()

	for _, d := range []database.Dialect{database.SQLite, database.Postgres, database.MySQL} {
		base := func() Query[User] { return QueryOf[User](fakeEx{d}).Select("id") }

		tests := []struct {
			name         string
			typed, plain Query[User]
		}{
			{"Eq", base().Filter(userCols.Name.Eq("a")), base().Where("name", "a")},
			{"Ne", base().Filter(userCols.Name.Ne("a")), base().Where("name", "!=", "a")},
			{"Gt", base().Filter(userCols.Age.Gt(1)), base().Where("age", ">", 1)},
			{"Gte", base().Filter(userCols.Age.Gte(1)), base().Where("age", ">=", 1)},
			{"Lt", base().Filter(userCols.Age.Lt(1)), base().Where("age", "<", 1)},
			{"Lte", base().Filter(userCols.Age.Lte(1)), base().Where("age", "<=", 1)},
			{"In", base().Filter(userCols.Age.In(1, 2)), base().WhereIn("age", 1, 2)},
			{"In empty", base().Filter(userCols.Age.In()), base().WhereIn("age")},
			{"NotIn", base().Filter(userCols.Age.NotIn(1, 2)), base().WhereNotIn("age", 1, 2)},
			{"Between", base().Filter(userCols.Age.Between(1, 9)), base().WhereBetween("age", 1, 9)},
			{"IsNull", base().Filter(userCols.Email.IsNull()), base().WhereNull("email")},
			{"IsNotNull", base().Filter(userCols.Email.IsNotNull()), base().WhereNotNull("email")},
			{"Like", base().Filter(userCols.Name.Like("a%")), base().Where("name", "LIKE", "a%")},
			{
				"several predicates", base().Filter(userCols.Name.Eq("a"), userCols.Age.Gte(3)),
				base().Where("name", "a").Where("age", ">=", 3),
			},
			{
				"OrFilter", base().Filter(userCols.Name.Eq("a")).OrFilter(userCols.Name.Eq("b"), userCols.Age.Lt(2)),
				base().Where("name", "a").OrWhere("name", "b").OrWhere("age", "<", 2),
			},
			{
				"Sort", base().Sort(userCols.Age.Desc(), userCols.Name.Asc()),
				base().OrderBy("age", Desc).OrderBy("name", Asc),
			},
		}

		for _, tt := range tests {
			gotSQL, gotArgs, err := tt.typed.SQL()
			if err != nil {
				t.Fatalf("%s/%s typed: %v", d.Name(), tt.name, err)
			}

			wantSQL, wantArgs, err := tt.plain.SQL()
			if err != nil {
				t.Fatalf("%s/%s plain: %v", d.Name(), tt.name, err)
			}

			if gotSQL != wantSQL {
				t.Errorf("%s/%s: SQL\n got %q\nwant %q", d.Name(), tt.name, gotSQL, wantSQL)
			}

			if diff := cmp.Diff(wantArgs, gotArgs); diff != "" {
				t.Errorf("%s/%s: args (-want +got):\n%s", d.Name(), tt.name, diff)
			}
		}
	}
}

func TestColumnFilterJoined(t *testing.T) {
	t.Parallel()

	db, ctx := qaSetup(t)

	authorName := Col[qaAuthor, string]("name")

	books, err := QueryOf[qaBook](db).
		InnerJoin[qaAuthor]("qa_authors.id", "=", "qa_books.author_id").
		FilterJoined(authorName.Eq("Ada")).
		Sort(Col[qaBook, int64]("id").Asc()).Get(ctx)
	if err != nil || !cmp.Equal(titles(books), []string{"Go", "SQL"}) {
		t.Errorf("FilterJoined = %v, %v", titles(books), err)
	}

	// Not joined: the column does not belong to the query.
	_, err = QueryOf[qaBook](db).FilterJoined(authorName.Eq("Ada")).Get(ctx)
	if err == nil || !strings.Contains(err.Error(), "does not belong to") {
		t.Errorf("FilterJoined without the join = %v, want a column error", err)
	}
}

func TestSecurity_A03_ColRejectsUnknownColumn(t *testing.T) {
	t.Parallel()

	db, ctx := setup(t)

	for _, name := range []string{"email; DROP TABLE users", `name" = 1 OR "1`, "nope", "users.email; --"} {
		bad := Col[User, string](name)

		if _, err := QueryOf[User](db).Filter(bad.Eq("x")).Get(ctx); err == nil {
			t.Errorf("Filter accepted the column %q", name)
		}

		if _, err := QueryOf[User](db).Sort(bad.Asc()).Get(ctx); err == nil {
			t.Errorf("Sort accepted the column %q", name)
		}
	}

	if n, err := QueryOf[User](db).Count(ctx); err != nil || n != 0 {
		t.Fatalf("users table after the attempts: %d, %v", n, err)
	}
}

func TestPredicateWithoutAnOperationFails(t *testing.T) {
	t.Parallel()

	var p Predicate[User]

	if err := QueryOf[User](fakeEx{database.SQLite}).Filter(p).err; err == nil {
		t.Error("a zero Predicate was accepted")
	}
}
