package orm

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/database"
)

type rating struct {
	UserID int64
	ItemID int64
	Score  int
}

func (rating) PrimaryKeys() []string { return []string{"user_id", "item_id"} }

// looseRating declares one column of a composite key, the VF-035 mistake.
type looseRating struct {
	UserID int64
	ItemID int64 `db:"-"`
	Score  int
}

func (looseRating) Table() string      { return "ratings" }
func (looseRating) PrimaryKey() string { return "user_id" }

type softRating struct {
	SoftDeletes
	UserID int64
	ItemID int64
	Score  int
}

func (softRating) Table() string         { return "soft_ratings" }
func (softRating) PrimaryKeys() []string { return []string{"user_id", "item_id"} }

func setupRatings(t *testing.T) (*database.DB, context.Context) {
	t.Helper()

	db, ctx := setup(t)

	for _, stmt := range []string{
		`CREATE TABLE ratings (user_id INTEGER NOT NULL, item_id INTEGER NOT NULL, score INTEGER NOT NULL,
			PRIMARY KEY (user_id, item_id))`,
		`CREATE TABLE soft_ratings (deleted_at TIMESTAMP, user_id INTEGER NOT NULL, item_id INTEGER NOT NULL,
			score INTEGER NOT NULL, PRIMARY KEY (user_id, item_id))`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}

	for _, r := range []rating{{1, 10, 3}, {1, 11, 4}, {2, 10, 5}} {
		if err := Create(ctx, db, &r); err != nil {
			t.Fatal(err)
		}
	}

	return db, ctx
}

func scores(ctx context.Context, t *testing.T, db *database.DB) map[[2]int64]int {
	t.Helper()

	rows, err := QueryOf[rating](db).Get(ctx)
	if err != nil {
		t.Fatal(err)
	}

	out := map[[2]int64]int{}
	for _, r := range rows {
		out[[2]int64{r.UserID, r.ItemID}] = r.Score
	}

	return out
}

func TestCompositeKeyUpdateDeleteTouchOneRow(t *testing.T) {
	t.Parallel()

	db, ctx := setupRatings(t)

	if got := PrimaryKeysOf[rating](); strings.Join(got, ",") != "user_id,item_id" {
		t.Fatalf("PrimaryKeysOf = %v", got)
	}

	if got := PrimaryKeysOf[User](); strings.Join(got, ",") != "id" {
		t.Fatalf("PrimaryKeysOf[User] = %v", got)
	}

	r := rating{UserID: 1, ItemID: 11, Score: 1}
	if err := Update(ctx, db, &r); err != nil {
		t.Fatal(err)
	}

	got := scores(ctx, t, db)
	if got[[2]int64{1, 10}] != 3 || got[[2]int64{1, 11}] != 1 || got[[2]int64{2, 10}] != 5 {
		t.Fatalf("Update touched other rows: %v", got)
	}

	if err := Delete(ctx, db, &r); err != nil {
		t.Fatal(err)
	}

	if got := scores(ctx, t, db); len(got) != 2 || got[[2]int64{1, 10}] != 3 {
		t.Fatalf("Delete touched other rows: %v", got)
	}

	missing := rating{UserID: 9, ItemID: 9}
	if err := Update(ctx, db, &missing); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Update missing = %v", err)
	}
}

func TestCompositeKeySoftDeleteAndRestore(t *testing.T) {
	t.Parallel()

	db, ctx := setupRatings(t)

	a, b := softRating{UserID: 1, ItemID: 10, Score: 3}, softRating{UserID: 1, ItemID: 11, Score: 4}
	for _, r := range []*softRating{&a, &b} {
		if err := Create(ctx, db, r); err != nil {
			t.Fatal(err)
		}
	}

	if err := Delete(ctx, db, &a); err != nil {
		t.Fatal(err)
	}

	if n, _ := QueryOf[softRating](db).Count(ctx); n != 1 {
		t.Fatalf("live rows after soft delete = %d, want 1", n)
	}

	if err := Restore(ctx, db, &a); err != nil {
		t.Fatal(err)
	}

	if err := ForceDelete(ctx, db, &b); err != nil {
		t.Fatal(err)
	}

	if n, _ := QueryOf[softRating](db).WithTrashed().Count(ctx); n != 1 {
		t.Fatalf("rows after force delete = %d, want 1", n)
	}
}

func TestCompositeKeySingleKeyAPIsFail(t *testing.T) {
	t.Parallel()

	db, ctx := setupRatings(t)

	if _, err := QueryOf[rating](db).Find(ctx, 1); err == nil || !strings.Contains(err.Error(), "composite") {
		t.Errorf("Find = %v, want a composite key error", err)
	}

	if err := QueryOf[rating](db).Chunk(ctx, 10, func([]rating) error { return nil }); err == nil {
		t.Error("Chunk accepted a composite key")
	}

	if _, err := Reseal[rating](ctx, db, 10); err == nil || !strings.Contains(err.Error(), "composite") {
		t.Errorf("Reseal = %v, want a composite key error", err)
	}

	page, err := QueryOf[rating](db).CursorPaginate(ctx, "", 2, Asc)
	if err != nil {
		t.Fatal(err)
	}

	next, err := QueryOf[rating](db).CursorPaginate(ctx, page.NextCursor, 2, Asc)
	if err != nil || len(page.Items)+len(next.Items) != 3 || next.Items[0].UserID != 2 {
		t.Fatalf("cursor pages = %+v / %+v %v", page.Items, next.Items, err)
	}
}

func TestCompositeKeyInvalidDeclarationPanics(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Fatal("unknown key column accepted")
		}
	}()

	metaOf[badKeys]()
}

type badKeys struct {
	A int64
	B int64
}

func (badKeys) PrimaryKeys() []string { return []string{"a", "missing"} }

// TestSecurity_A04_NonUniqueKeyWriteFails covers VF-035: a model that
// declares one column of a composite key must not silently rewrite every
// row sharing that column. In a transaction the error rolls the write back.
func TestSecurity_A04_NonUniqueKeyWriteFails(t *testing.T) {
	t.Parallel()

	db, ctx := setupRatings(t)

	err := db.Tx(ctx, func(tx *database.Tx) error {
		return Update(ctx, tx, &looseRating{UserID: 1, ItemID: 10, Score: 0})
	})
	if !errors.Is(err, ErrKeyNotUnique) {
		t.Fatalf("Update = %v, want ErrKeyNotUnique", err)
	}

	if got := scores(ctx, t, db); got[[2]int64{1, 10}] != 3 || got[[2]int64{1, 11}] != 4 {
		t.Fatalf("rolled-back Update changed rows: %v", got)
	}

	err = db.Tx(ctx, func(tx *database.Tx) error {
		return Delete(ctx, tx, &looseRating{UserID: 1, ItemID: 10})
	})
	if !errors.Is(err, ErrKeyNotUnique) {
		t.Fatalf("Delete = %v, want ErrKeyNotUnique", err)
	}

	if got := scores(ctx, t, db); len(got) != 3 {
		t.Fatalf("rolled-back Delete removed rows: %v", got)
	}
}
