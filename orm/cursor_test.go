package orm

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/errs"
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

	for _, bad := range []string{"WzFd", "WyJ4IiwxLDJd"} { // [1]: one key short; ["x",1,2]: a string for a bool
		_, err := QueryOf[User](db).OrderBy("active", Desc).OrderBy("age", Asc).CursorPaginate(ctx, bad, 10, Asc)
		if !errors.Is(err, ErrInvalidCursor) {
			t.Errorf("composite cursor %q: err = %v", bad, err)
		}
	}

	if _, err := QueryOf[User](db).Where("nope", 1).CursorPaginate(ctx, "", 10, Asc); err == nil {
		t.Error("query error swallowed")
	}

	page, err := QueryOf[User](db).CursorPaginate(ctx, "", 0, Asc)
	if err != nil || page.PerPage != 1 || len(page.Items) != 0 {
		t.Fatalf("empty = %+v %v", page, err)
	}
}

// VF-005 (HexaScore): keyset pagination over the query's own OrderBy,
// with the primary key appended as the tie-breaker.
func TestCursorPaginateOrderBy(t *testing.T) {
	t.Parallel()

	db, ctx := setup(t)
	seedUsers(ctx, t, db)

	collect := func(t *testing.T, q Query[User], dir Direction) []string {
		t.Helper()

		var (
			got    []string
			cursor string
		)

		for range 10 {
			page, err := q.CursorPaginate(ctx, cursor, 1, dir)
			if err != nil {
				t.Fatal(err)
			}

			got = append(got, names(page.Items)...)
			if page.NextCursor == "" {
				return got
			}

			cursor = page.NextCursor
		}

		t.Fatalf("no last page after 10 pages: %v", got)

		return nil
	}

	tests := []struct {
		name string
		q    Query[User]
		dir  Direction
		want []string
	}{
		{
			"mixed directions", QueryOf[User](db).OrderBy("active", Desc).OrderBy("age", Asc), Asc,
			[]string{"Ana", "Cid", "Bob", "Dea"},
		},
		{
			"ties broken by the primary key", QueryOf[User](db).OrderBy("active", Asc), Desc,
			[]string{"Dea", "Bob", "Cid", "Ana"},
		},
		{"time column (Latest)", QueryOf[User](db).Latest(), Asc, []string{"Dea", "Cid", "Bob", "Ana"}},
		{
			"with a filter", QueryOf[User](db).Where("age", ">", 18).OrderBy("age", Desc), Asc,
			[]string{"Cid", "Ana", "Dea"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tt.want, collect(t, tt.q, tt.dir)); diff != "" {
				t.Errorf("pages (-want +got):\n%s", diff)
			}
		})
	}
}

// FuzzDecodeCursor: a client-supplied cursor either fails or yields one
// value per key, never panics.
func FuzzDecodeCursor(f *testing.F) {
	for _, s := range []string{"", "Mg", "WzFd", "WyJ4IiwxLDJd", "W3RydWUsMzAsMV0", "%%%", "bnVsbA"} {
		f.Add(s)
	}

	q := QueryOf[User](nil)
	keys := []orderKey{{"active", Desc}, {"age", Asc}, {"id", Asc}}

	f.Fuzz(func(t *testing.T, s string) {
		for _, k := range [][]orderKey{keys[2:], keys} {
			if vals, err := q.decodeCursor(s, k); err == nil && len(vals) != len(k) {
				t.Fatalf("decodeCursor(%q) = %d values, want %d", s, len(vals), len(k))
			}
		}
	})
}
