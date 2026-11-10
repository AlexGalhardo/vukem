package orm

import (
	"errors"
	"testing"
)

func TestRestore_NotTrashedReportsNotFound(t *testing.T) {
	t.Parallel()

	db, ctx := setup(t)

	p := Post{UserID: 1, Title: "live"}
	if err := Create(ctx, db, &p); err != nil {
		t.Fatal(err)
	}

	if err := Restore(ctx, db, &p); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Restore(not trashed) = %v, want ErrNotFound", err)
	}

	gone := Post{Model: Model{ID: 9999}}
	if err := Restore(ctx, db, &gone); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Restore(missing) = %v, want ErrNotFound", err)
	}
}
