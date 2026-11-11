package orm

import (
	"errors"
	"testing"

	"github.com/AlexGalhardo/vukem/database"
)

type cfTag struct {
	ID    int64 `db:"id"`
	Label string
}

func (cfTag) Table() string { return "cf_tags" }

type cfPost struct {
	ID   int64 `db:"id"`
	Name string

	Tags ManyToMany[cfTag] `orm:"pivot:cf_post_tag,fk:post_id,rk:tag_id"`
}

func (cfPost) Table() string { return "cf_posts" }

func TestConflictCreateDuplicate(t *testing.T) {
	t.Parallel()

	db, ctx := setup(t)

	if err := Create(ctx, db, &User{Name: "Ana", Email: "ana@x.io"}); err != nil {
		t.Fatal(err)
	}

	err := Create(ctx, db, &User{Name: "Other", Email: "ana@x.io"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("Create(duplicate email) = %v, want ErrConflict", err)
	}
}

func TestConflictAttachDuplicate(t *testing.T) {
	t.Parallel()

	db, ctx := setup(t)

	// A tag can belong to one post only: the pivot is unique on tag_id.
	for _, stmt := range []string{
		`CREATE TABLE cf_tags (id INTEGER PRIMARY KEY, label TEXT NOT NULL)`,
		`CREATE TABLE cf_posts (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`,
		`CREATE TABLE cf_post_tag (post_id INTEGER NOT NULL, tag_id INTEGER NOT NULL UNIQUE)`,
		`INSERT INTO cf_tags (id, label) VALUES (1, 'a')`,
		`INSERT INTO cf_posts (id, name) VALUES (1, 'p1'), (2, 'p2')`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}

	if err := Attach(ctx, db, &cfPost{ID: 1}, "Tags", int64(1)); err != nil {
		t.Fatal(err)
	}

	err := Attach(ctx, db, &cfPost{ID: 2}, "Tags", int64(1))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("Attach(tag already on another post) = %v, want ErrConflict", err)
	}
}

// uniqueEx reports unique violations the way *database.DB does.
type uniqueEx struct {
	fakeEx
	violation bool
}

func (u uniqueEx) UniqueViolation(error) bool { return u.violation }

func TestConflictIgnoresErrorText(t *testing.T) {
	t.Parallel()

	text := errors.New(`pq: duplicate key value violates unique constraint "x"`)

	tests := []struct {
		name string
		ex   interface{ Dialect() database.Dialect }
		want bool
	}{
		{"wrapped executor matches the text", fakeEx{database.SQLite}, true},
		{"executor that knows its driver: not a violation", uniqueEx{fakeEx{database.SQLite}, false}, false},
		{"executor that knows its driver: a violation", uniqueEx{fakeEx{database.SQLite}, true}, true},
	}

	for _, tt := range tests {
		ex, ok := tt.ex.(database.Executor)
		if !ok {
			t.Fatalf("%s: not an Executor", tt.name)
		}

		if got := errors.Is(mapError(ex, text), ErrConflict); got != tt.want {
			t.Errorf("%s: conflict = %v, want %v", tt.name, got, tt.want)
		}
	}
}
