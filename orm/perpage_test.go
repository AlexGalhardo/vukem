package orm

import (
	"fmt"
	"testing"
)

// Paginate and CursorPaginate cap perPage at DefaultMaxPerPage, so a
// ?per_page=1000000 forwarded from a request cannot load a whole table;
// MaxPerPage changes the bound per query.
func TestSecurity_A04_PaginateCapsPerPage(t *testing.T) {
	t.Parallel()

	db, ctx := setup(t)

	for i := range DefaultMaxPerPage + 5 {
		u := User{Name: "u", Email: fmt.Sprintf("u%d@x.io", i)}
		if err := Create(ctx, db, &u); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name    string
		q       Query[User]
		perPage int
		want    int // items returned
		wantPer int // reported per_page
	}{
		{"default cap", QueryOf[User](db), 1_000_000, DefaultMaxPerPage, DefaultMaxPerPage},
		{"below cap", QueryOf[User](db), 7, 7, 7},
		{"raised", QueryOf[User](db).MaxPerPage(500), 1_000_000, DefaultMaxPerPage + 5, 500},
		{"lowered", QueryOf[User](db).MaxPerPage(3), 50, 3, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p, err := tt.q.Paginate(ctx, 1, tt.perPage)
			if err != nil {
				t.Fatal(err)
			}

			if len(p.Items) != tt.want || p.PerPage != tt.wantPer {
				t.Errorf("Paginate(1, %d) = %d items, per_page %d, want %d, %d",
					tt.perPage, len(p.Items), p.PerPage, tt.want, tt.wantPer)
			}

			cp, err := tt.q.CursorPaginate(ctx, "", tt.perPage, Asc)
			if err != nil {
				t.Fatal(err)
			}

			if len(cp.Items) != tt.want || cp.PerPage != tt.wantPer {
				t.Errorf("CursorPaginate(%d) = %d items, per_page %d, want %d, %d",
					tt.perPage, len(cp.Items), cp.PerPage, tt.want, tt.wantPer)
			}
		})
	}

	if _, err := QueryOf[User](db).MaxPerPage(0).Paginate(ctx, 1, 10); err == nil {
		t.Error("MaxPerPage(0) = nil error, want error")
	}
}
