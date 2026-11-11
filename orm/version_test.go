package orm

import (
	"context"
	"errors"
	"testing"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/errs"
)

type article struct {
	ID      int64 `db:"id"`
	Title   string
	Body    string
	Version int64 `        orm:"version"`
}

func (article) Table() string { return "articles" }

type softArticle struct {
	SoftDeletes

	ID      int64 `db:"id"`
	Title   string
	Version int64 `        orm:"version"`
}

func (softArticle) Table() string { return "soft_articles" }

func versionSetup(t *testing.T) (*database.DB, context.Context) {
	t.Helper()

	db, ctx := setup(t)

	for _, stmt := range []string{
		`CREATE TABLE articles (id INTEGER PRIMARY KEY AUTOINCREMENT, title TEXT NOT NULL, body TEXT NOT NULL DEFAULT '', version INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE soft_articles (id INTEGER PRIMARY KEY AUTOINCREMENT, deleted_at TIMESTAMP, title TEXT NOT NULL, version INTEGER NOT NULL DEFAULT 0)`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}

	return db, ctx
}

func TestVersionCreateStartsAtOne(t *testing.T) {
	t.Parallel()

	db, ctx := versionSetup(t)

	a := article{Title: "a"}
	if err := Create(ctx, db, &a); err != nil || a.Version != 1 {
		t.Fatalf("Create: version %d, %v; want 1", a.Version, err)
	}

	many := []article{{Title: "b"}, {Title: "c"}}
	if err := CreateMany(ctx, db, many); err != nil || many[0].Version != 1 || many[1].Version != 1 {
		t.Fatalf("CreateMany: %+v, %v; want version 1 on each", many, err)
	}
}

func TestVersionUpdateDetectsStaleReaders(t *testing.T) {
	t.Parallel()

	db, ctx := versionSetup(t)

	a := article{Title: "first"}
	if err := Create(ctx, db, &a); err != nil {
		t.Fatal(err)
	}

	one, err := QueryOf[article](db).Find(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}

	two := one

	one.Title = "by one"
	if err := Update(ctx, db, &one); err != nil || one.Version != 2 {
		t.Fatalf("first Update: version %d, %v; want 2", one.Version, err)
	}

	two.Title = "by two"

	err = Update(ctx, db, &two)
	if !errors.Is(err, ErrStale) || errs.Status(err) != 409 {
		t.Fatalf("second Update = %v (status %d), want ErrStale with 409", err, errs.Status(err))
	}

	if two.Version != 1 {
		t.Errorf("a failed Update changed the struct version to %d", two.Version)
	}

	got, _ := QueryOf[article](db).Find(ctx, a.ID)
	if got.Title != "by one" || got.Version != 2 {
		t.Errorf("row = %+v, want the first writer's change", got)
	}

	// UpdateOnly compares too.
	two.Body = "body"
	if err := UpdateOnly(ctx, db, &two, "body"); !errors.Is(err, ErrStale) {
		t.Errorf("UpdateOnly with a stale version = %v, want ErrStale", err)
	}

	got.Body = "fresh"
	if err := UpdateOnly(ctx, db, &got, "body"); err != nil || got.Version != 3 {
		t.Errorf("UpdateOnly with the current version = %v (version %d), want 3", err, got.Version)
	}

	// The counter is not a column to write by hand.
	if err := UpdateOnly(ctx, db, &got, "version"); err == nil {
		t.Error("UpdateOnly accepted the version column")
	}
}

func TestVersionMissingRowIsNotFound(t *testing.T) {
	t.Parallel()

	db, ctx := versionSetup(t)

	if err := Update(ctx, db, &article{ID: 99, Title: "x", Version: 1}); !errors.Is(err, ErrNotFound) {
		t.Errorf("Update of a missing row = %v, want ErrNotFound", err)
	}

	s := softArticle{Title: "s"}
	if err := Create(ctx, db, &s); err != nil {
		t.Fatal(err)
	}

	if err := Delete(ctx, db, &s); err != nil {
		t.Fatal(err)
	}

	if err := Update(ctx, db, &s); !errors.Is(err, ErrNotFound) {
		t.Errorf("Update of a trashed row = %v, want ErrNotFound (not ErrStale)", err)
	}
}

func TestVersionQueryUpdateIncrements(t *testing.T) {
	t.Parallel()

	db, ctx := versionSetup(t)

	a := article{Title: "a"}
	if err := Create(ctx, db, &a); err != nil {
		t.Fatal(err)
	}

	if n, err := QueryOf[article](db).Where("id", a.ID).Update(ctx, Set{"title": "b"}); err != nil || n != 1 {
		t.Fatalf("Query.Update = %d, %v", n, err)
	}

	got, _ := QueryOf[article](db).Find(ctx, a.ID)
	if got.Version != 2 {
		t.Errorf("version after Query.Update = %d, want 2", got.Version)
	}

	if _, err := QueryOf[article](db).Where("id", a.ID).Update(ctx, Set{"version": 10}); err != nil {
		t.Fatal(err)
	}

	if got, _ := QueryOf[article](db).Find(ctx, a.ID); got.Version != 10 {
		t.Errorf("an explicit version in the Set = %d, want 10", got.Version)
	}
}

func TestVersionTagMisuse(t *testing.T) {
	t.Parallel()

	type badType struct {
		ID      int64  `db:"id"`
		Version string `        orm:"version"`
	}

	type twoVersions struct {
		ID int64 `db:"id"`
		A  int64 `        orm:"version"`
		B  int64 `        orm:"version"`
	}

	for name, f := range map[string]func(){
		"string version": func() { metaOf[badType]() },
		"two versions":   func() { metaOf[twoVersions]() },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: no panic", name)
				}
			}()

			f()
		}()
	}
}

type verItem struct {
	ID      int64 `db:"id"`
	Code    string
	Note    string
	Version int64 `        orm:"version"`
}

func (verItem) Table() string { return "ver_items" }

func TestVersionUpsertBumpsTheVersion(t *testing.T) {
	t.Parallel()

	db, ctx := setup(t)

	if _, err := db.ExecContext(
		ctx,
		`CREATE TABLE ver_items (id INTEGER PRIMARY KEY AUTOINCREMENT, code TEXT NOT NULL UNIQUE, note TEXT NOT NULL, version INTEGER NOT NULL DEFAULT 0)`,
	); err != nil {
		t.Fatal(err)
	}

	on := OnConflict{Columns: []string{"code"}, Update: []string{"note"}}

	first := verItem{Code: "a", Note: "one"}
	if err := Upsert(ctx, db, &first, on); err != nil || first.Version != 1 {
		t.Fatalf("insert: version %d, %v; want 1", first.Version, err)
	}

	reader, _ := QueryOf[verItem](db).Find(ctx, first.ID)

	again := verItem{Code: "a", Note: "two"}
	if err := Upsert(ctx, db, &again, on); err != nil {
		t.Fatal(err)
	}

	if again.Version != 2 {
		t.Errorf("version written back by an updating Upsert = %d, want 2", again.Version)
	}

	// A reader from before the upsert must not overwrite it silently.
	reader.Note = "stale write"
	if err := Update(ctx, db, &reader); !errors.Is(err, ErrStale) {
		t.Errorf("Update by a reader from before the Upsert = %v, want ErrStale", err)
	}

	if err := UpsertMany(ctx, db, []verItem{{Code: "a", Note: "three"}}, on); err != nil {
		t.Fatal(err)
	}

	if got, _ := QueryOf[verItem](db).Find(ctx, first.ID); got.Version != 3 || got.Note != "three" {
		t.Errorf("row after UpsertMany = %+v, want version 3", got)
	}
}
