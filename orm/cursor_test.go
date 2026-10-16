package orm

import (
	"errors"
	"testing"

	"github.com/AlexGalhardo/golang-framework/errs"
)

func names(us []User) []string {
	out := make([]string, len(us))
	for i := range us {
		out[i] = us[i].Name
	}

	return out
}

func TestCursorPaginate(t *testing.T) {
	t.Parallel()

	db, ctx := setup(t)
	seedUsers(ctx, t, db)

	var (
		got    []string
		cursor string
		pages  int
	)

	for {
		page, err := QueryOf[User](db).CursorPaginate(ctx, cursor, 3, Asc)
		if err != nil {
			t.Fatal(err)
		}

		pages++
		got = append(got, names(page.Items)...)

		if page.NextCursor == "" {
			break
		}

		cursor = page.NextCursor
	}

	if pages != 2 || len(got) != 4 || got[0] != "Ana" || got[3] != "Dea" {
		t.Fatalf("asc pages=%d names=%v", pages, got)
	}

	// Descending with a filter; the last page has no cursor.
	first, err := QueryOf[User](db).Where("age", ">", 18).CursorPaginate(ctx, "", 2, Desc)
	if err != nil || len(first.Items) != 2 || first.Items[0].Name != "Dea" || first.NextCursor == "" {
		t.Fatalf("desc first = %+v %v", first, err)
	}

	second, err := QueryOf[User](db).Where("age", ">", 18).CursorPaginate(ctx, first.NextCursor, 2, Desc)
	if err != nil || len(names(second.Items)) != 1 || second.Items[0].Name != "Ana" || second.NextCursor != "" {
		t.Fatalf("desc second = %+v %v", second, err)
	}

	// String keys work too.
	for _, c := range []Category{{Code: "a", Name: "A"}, {Code: "b", Name: "B"}} {
		if err := Create(ctx, db, &c); err != nil {
			t.Fatal(err)
		}
	}

	cats, err := QueryOf[Category](db).CursorPaginate(ctx, "", 1, Asc)
	if err != nil || cats.NextCursor == "" {
		t.Fatal(err)
	}

	if next, err := QueryOf[Category](
		db,
	).CursorPaginate(ctx, cats.NextCursor, 1, Asc); err != nil ||
		next.Items[0].Code != "b" {
		t.Fatalf("string cursor = %+v %v", next, err)
	}
}

func TestCursorPaginateErrors(t *testing.T) {
	t.Parallel()

	db, ctx := setup(t)

	for _, bad := range []string{"%%%", "bm90IGpzb24", "W10", "e30"} { // not base64, not JSON, array, object
		_, err := QueryOf[User](db).CursorPaginate(ctx, bad, 10, Asc)
		if !errors.Is(err, ErrInvalidCursor) || errs.Status(err) != 400 {
			t.Errorf("cursor %q: err = %v", bad, err)
		}
	}

	if _, err := QueryOf[User](db).OrderBy("name", Asc).CursorPaginate(ctx, "", 10, Asc); err == nil {
		t.Error("custom order accepted")
	}

	if _, err := QueryOf[User](db).Where("nope", 1).CursorPaginate(ctx, "", 10, Asc); err == nil {
		t.Error("query error swallowed")
	}

	page, err := QueryOf[User](db).CursorPaginate(ctx, "", 0, Asc)
	if err != nil || page.PerPage != 1 || len(page.Items) != 0 {
		t.Fatalf("empty = %+v %v", page, err)
	}
}
