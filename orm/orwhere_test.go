package orm

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/database"
)

func TestOrWhere(t *testing.T) {
	t.Parallel()

	lite := fakeEx{database.SQLite}
	base := func() Query[User] { return QueryOf[User](lite).Select("id").Where("age", 1) }

	tests := []struct {
		name string
		q    interface{ SQL() (string, []any, error) }
		want string
	}{
		{"OrWhereIn", base().OrWhereIn("age", 2, 3), `SELECT "id" FROM "users" WHERE "age" = ? OR "age" IN (?, ?)`},
		{
			"OrWhereNotIn",
			base().OrWhereNotIn("age", 2, 3),
			`SELECT "id" FROM "users" WHERE "age" = ? OR "age" NOT IN (?, ?)`,
		},
		{"OrWhereNull", base().OrWhereNull("email"), `SELECT "id" FROM "users" WHERE "age" = ? OR "email" IS NULL`},
		{
			"OrWhereNotNull",
			base().OrWhereNotNull("email"),
			`SELECT "id" FROM "users" WHERE "age" = ? OR "email" IS NOT NULL`,
		},
		{
			"OrWhereBetween",
			base().OrWhereBetween("age", 5, 9),
			`SELECT "id" FROM "users" WHERE "age" = ? OR "age" BETWEEN ? AND ?`,
		},
		{
			"OrWhereRaw",
			base().OrWhereRaw("age % 2 = ?", 0),
			`SELECT "id" FROM "users" WHERE "age" = ? OR (age % 2 = ?)`,
		},
		{
			"OrWhereGroup",
			base().OrWhereGroup(func(g Query[User]) Query[User] { return g.Where("age", 7).Where("active", true) }),
			`SELECT "id" FROM "users" WHERE "age" = ? OR ("age" = ? AND "active" = ?)`,
		},
		{
			"OrWhereIn with no values matches nothing",
			base().OrWhereIn("age"),
			`SELECT "id" FROM "users" WHERE "age" = ? OR 1 = 0`,
		},
		{
			"soft delete keeps the user's OR group inside parentheses",
			QueryOf[Post](lite).Select("id").Where("title", "a").OrWhereIn("title", "b", "c"),
			`SELECT "id" FROM "posts" WHERE "deleted_at" IS NULL AND ("title" = ? OR "title" IN (?, ?))`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, _, err := tt.q.SQL()
			if err != nil || got != tt.want {
				t.Errorf("SQL() = %q, %v\nwant %q", got, err, tt.want)
			}
		})
	}
}

func TestSecurity_A01_OrFilterCannotEscapeKey(t *testing.T) {
	t.Parallel()

	db, ctx := setup(t)
	users := seedUsers(ctx, t, db)
	ana, bob := users[0], users[1]

	// Find(bob) must still match only bob, whatever the OR filter says.
	q := QueryOf[User](db).Where("name", "Ana").OrWhere("name", "Cid")
	if got, err := q.Find(ctx, bob.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Find(bob) under an Ana-or-Cid filter = %+v, %v; want ErrNotFound", got, err)
	}

	if got, err := q.Find(ctx, ana.ID); err != nil || got.ID != ana.ID {
		t.Fatalf("Find(ana) = %+v, %v; want ana", got, err)
	}

	// The key predicate is grouped: pk = ? applies to the whole OR filter.
	sqlText, _, err := QueryOf[User](fakeEx{database.SQLite}).Select("id").Where("name", "a").OrWhere("name", "b").
		grouped().Where("id", 1).SQL()
	if err != nil || !strings.Contains(sqlText, `("name" = ? OR "name" = ?) AND "id" = ?`) {
		t.Fatalf("SQL = %q, %v; want the OR filter grouped before the key", sqlText, err)
	}

	for _, u := range users {
		p := Post{UserID: u.ID, Title: "t"}
		if err := Create(ctx, db, &p); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := QueryOf[Post](db).WithTrashed().Where("title", "t").OrWhere("title", "u").Find(ctx, 1); err != nil {
		t.Fatalf("Find under a OR filter that matches = %v", err)
	}
}

func TestCursorPaginateWithOrWhere(t *testing.T) {
	t.Parallel()

	db, ctx := setup(t)
	seedUsers(ctx, t, db)

	q := QueryOf[User](db).Where("name", "Ana").OrWhere("name", "Bob").OrWhere("name", "Cid")

	var (
		seen   []string
		cursor string
	)

	for range 5 {
		page, err := q.CursorPaginate(ctx, cursor, 1, Asc)
		if err != nil {
			t.Fatal(err)
		}

		for _, u := range page.Items {
			seen = append(seen, u.Name)
		}

		if page.NextCursor == "" {
			break
		}

		cursor = page.NextCursor
	}

	if diff := cmp.Diff([]string{"Ana", "Bob", "Cid"}, seen); diff != "" {
		t.Errorf("rows across cursor pages (-want +got):\n%s", diff)
	}
}
