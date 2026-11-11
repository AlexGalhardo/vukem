package orm

import (
	"errors"
	"strings"
	"testing"
)

func TestUpdateSkipsSoftDeleted(t *testing.T) {
	t.Parallel()

	db, ctx := setup(t)

	p := Post{UserID: 1, Title: "keep"}
	if err := Create(ctx, db, &p); err != nil {
		t.Fatal(err)
	}

	if err := Delete(ctx, db, &p); err != nil {
		t.Fatal(err)
	}

	p.Title = "changed"
	if err := Update(ctx, db, &p); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Update of a trashed row = %v, want ErrNotFound", err)
	}

	got, err := QueryOf[Post](db).WithTrashed().Where("id", p.ID).First(ctx)
	if err != nil || got.Title != "keep" {
		t.Fatalf("trashed row = %+v, %v; want title %q untouched", got, err, "keep")
	}

	// The explicit way to change a trashed row.
	n, err := QueryOf[Post](db).OnlyTrashed().Where("id", p.ID).Update(ctx, Set{"title": "edited"})
	if err != nil || n != 1 {
		t.Fatalf("OnlyTrashed().Update = %d, %v; want 1 row", n, err)
	}
}

func TestUpdateOnlyChangesOnlyGivenColumns(t *testing.T) {
	t.Parallel()

	db, ctx := setup(t)

	u := User{Name: "Ana", Email: "ana@x.io", Age: 30}
	if err := Create(ctx, db, &u); err != nil {
		t.Fatal(err)
	}

	// Another writer changes age; this model still holds the old one.
	if _, err := db.ExecContext(ctx, `UPDATE users SET age = 99 WHERE id = ?`, u.ID); err != nil {
		t.Fatal(err)
	}

	u.Name, u.Email = "Anna", "other@x.io"
	if err := UpdateOnly(ctx, db, &u, "name"); err != nil {
		t.Fatal(err)
	}

	got, err := QueryOf[User](db).Where("id", u.ID).First(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if got.Name != "Anna" || got.Email != "ana@x.io" || got.Age != 99 {
		t.Fatalf("after UpdateOnly(name) = %+v; want name changed, email and age untouched", got)
	}

	if !got.UpdatedAt.Equal(u.UpdatedAt) || got.UpdatedAt.IsZero() {
		t.Errorf("updated_at = %v, want the refreshed %v", got.UpdatedAt, u.UpdatedAt)
	}
}

func TestUpdateOnlyErrors(t *testing.T) {
	t.Parallel()

	db, ctx := setup(t)

	u := User{Name: "Ana", Email: "ana@x.io"}
	if err := Create(ctx, db, &u); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		cols []string
		want string
	}{
		{"empty list", nil, "UpdateOnly needs at least one column"},
		{"unknown column", []string{"nope"}, `unknown column "nope"`},
		{"primary key", []string{"id"}, `UpdateOnly cannot write "id"`},
		{"created_at", []string{"created_at"}, `UpdateOnly cannot write "created_at"`},
	}

	for _, tt := range tests {
		if err := UpdateOnly(ctx, db, &u, tt.cols...); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: UpdateOnly = %v, want an error containing %q", tt.name, err, tt.want)
		}
	}
}

func TestSecurity_A03_UpdateOnlyRejectsUnknownColumn(t *testing.T) {
	t.Parallel()

	db, ctx := setup(t)

	u := User{Name: "Ana", Email: "ana@x.io"}
	if err := Create(ctx, db, &u); err != nil {
		t.Fatal(err)
	}

	for _, col := range []string{`name" = 'x', "age`, "name; DROP TABLE users", "name'", "users.name; --"} {
		if err := UpdateOnly(ctx, db, &u, col); err == nil {
			t.Errorf("UpdateOnly(%q) accepted the column", col)
		}
	}

	if n, err := QueryOf[User](db).Count(ctx); err != nil || n != 1 {
		t.Fatalf("users after the attempts = %d, %v; want 1", n, err)
	}
}
