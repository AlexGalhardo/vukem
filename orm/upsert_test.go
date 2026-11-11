package orm

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/database"
)

func TestUpsert(t *testing.T) {
	t.Parallel()

	db, ctx := setup(t)

	u := User{Name: "Ana", Email: "ana@x.io", Age: 30}
	if err := Upsert(ctx, db, &u, OnConflict{Columns: []string{"email"}, Update: []string{"name", "age"}}); err != nil {
		t.Fatal(err)
	}

	firstID := u.ID
	if firstID == 0 {
		t.Fatal("Upsert did not write the generated id back")
	}

	again := User{Name: "Anna", Email: "ana@x.io", Age: 31}
	if err := Upsert(
		ctx,
		db,
		&again,
		OnConflict{Columns: []string{"email"}, Update: []string{"name", "age"}},
	); err != nil {
		t.Fatal(err)
	}

	if again.ID != firstID {
		t.Errorf("Upsert of an existing row set id %d, want %d", again.ID, firstID)
	}

	n, err := QueryOf[User](db).Count(ctx)
	if err != nil || n != 1 {
		t.Fatalf("rows = %d, %v; want 1", n, err)
	}

	got, err := QueryOf[User](db).Find(ctx, firstID)
	if err != nil || got.Name != "Anna" || got.Age != 31 {
		t.Errorf("row after upsert = %+v, %v", got, err)
	}

	// DoNothing leaves the row alone and sets no id.
	keep := User{Name: "Nope", Email: "ana@x.io"}
	if err := Upsert(ctx, db, &keep, OnConflict{Columns: []string{"email"}, DoNothing: true}); err != nil {
		t.Fatal(err)
	}

	if got, _ := QueryOf[User](db).Find(ctx, firstID); got.Name != "Anna" || keep.ID != 0 {
		t.Errorf("DoNothing changed the row (%q) or set an id (%d)", got.Name, keep.ID)
	}
}

func TestUpsertMany(t *testing.T) {
	t.Parallel()

	db, ctx := setup(t)

	if err := Create(ctx, db, &User{Name: "Ana", Email: "a@x.io"}); err != nil {
		t.Fatal(err)
	}

	err := UpsertMany(ctx, db, []User{
		{Name: "Ana2", Email: "a@x.io"}, {Name: "Bob", Email: "b@x.io"}, {Name: "Cid", Email: "c@x.io"},
	}, OnConflict{Columns: []string{"email"}, Update: []string{"name"}})
	if err != nil {
		t.Fatal(err)
	}

	names, err := QueryOf[User](db).OrderBy("email", Asc).Pluck[string](ctx, "name")
	if err != nil || strings.Join(names, ",") != "Ana2,Bob,Cid" {
		t.Errorf("names = %v, %v; want Ana2,Bob,Cid", names, err)
	}
}

func TestUpsertRunsNoHooks(t *testing.T) {
	t.Parallel()

	db, ctx := setup(t)

	var calls []string

	h := hooked{Name: "x", calls: &calls}
	if err := Upsert(ctx, db, &h, OnConflict{Columns: []string{"id"}, Update: []string{"name"}}); err != nil {
		t.Fatal(err)
	}

	if len(calls) != 0 {
		t.Errorf("Upsert ran hooks: %v", calls)
	}
}

func TestUpsertOtherUniqueIndexIsConflict(t *testing.T) {
	t.Parallel()

	db, ctx := setup(t)

	if err := Create(ctx, db, &User{Name: "Ana", Email: "a@x.io"}); err != nil {
		t.Fatal(err)
	}

	// The conflict target is the id, so the clash on email is another index.
	err := Upsert(
		ctx,
		db,
		&User{ID: 99, Name: "Eve", Email: "a@x.io"},
		OnConflict{Columns: []string{"id"}, Update: []string{"name"}},
	)
	if !errors.Is(err, ErrConflict) {
		t.Errorf("Upsert clashing on another index = %v, want ErrConflict", err)
	}
}

func TestUpsertSQL(t *testing.T) {
	t.Parallel()

	m := metaOf[User]()
	conflict, updates, err := upsertPlan(m, OnConflict{Columns: []string{"email"}, Update: []string{"name"}})
	if err != nil {
		t.Fatal(err)
	}

	tests := map[string]string{
		"postgres": ` ON CONFLICT ("email") DO UPDATE SET "name" = excluded."name", "updated_at" = excluded."updated_at"`,
		"mysql":    " ON DUPLICATE KEY UPDATE `name` = VALUES(`name`), `updated_at` = VALUES(`updated_at`)",
	}

	for name, want := range map[string]database.Dialect{"postgres": database.Postgres, "mysql": database.MySQL} {
		if got := conflictSQL(want, m, conflict, updates, false); got != tests[name] {
			t.Errorf("%s: %q, want %q", name, got, tests[name])
		}
	}

	if got := conflictSQL(database.Postgres, m, conflict, nil, false); got != ` ON CONFLICT ("email") DO NOTHING` {
		t.Errorf("DoNothing postgres = %q", got)
	}

	if got := conflictSQL(
		database.MySQL,
		m,
		conflict,
		nil,
		false,
	); got != " ON DUPLICATE KEY UPDATE `email` = `email`" {
		t.Errorf("DoNothing mysql = %q", got)
	}
}

type sdItem struct {
	SoftDeletes

	ID   int64 `db:"id"`
	Code string
	Note string
}

func (sdItem) Table() string { return "sd_items" }

func sdSetup(t *testing.T) (*database.DB, context.Context) {
	t.Helper()

	db, ctx := setup(t)

	if _, err := db.ExecContext(
		ctx,
		`CREATE TABLE sd_items (id INTEGER PRIMARY KEY, deleted_at TIMESTAMP, code TEXT NOT NULL UNIQUE, note TEXT NOT NULL)`,
	); err != nil {
		t.Fatal(err)
	}

	return db, ctx
}

func TestSecurity_A01_UpsertNeverRevivesTrashedRows(t *testing.T) {
	t.Parallel()

	db, ctx := sdSetup(t)
	on := OnConflict{Columns: []string{"code"}, Update: []string{"note"}}

	item := sdItem{Code: "a", Note: "one"}
	if err := Upsert(ctx, db, &item, on); err != nil {
		t.Fatal(err)
	}

	if err := Delete(ctx, db, &item); err != nil {
		t.Fatal(err)
	}

	for name, on := range map[string]OnConflict{
		"update":     on,
		"default":    {Columns: []string{"code"}},
		"do nothing": {Columns: []string{"code"}, DoNothing: true},
	} {
		again := sdItem{Code: "a", Note: "revived"}

		err := Upsert(ctx, db, &again, on)
		if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "Restore it first") {
			t.Errorf("%s: Upsert over a trashed row = %v, want ErrConflict mentioning Restore", name, err)
		}
	}

	if err := UpsertMany(
		ctx,
		db,
		[]sdItem{{Code: "a", Note: "revived"}, {Code: "b", Note: "new"}},
		on,
	); !errors.Is(
		err,
		ErrConflict,
	) {
		t.Errorf("UpsertMany over a trashed row = %v, want ErrConflict", err)
	}

	got, err := QueryOf[sdItem](db).OnlyTrashed().Where("code", "a").First(ctx)
	if err != nil || got.Note != "one" || !got.DeletedAt.Valid {
		t.Errorf("the trashed row = %+v, %v; want it untouched and still trashed", got, err)
	}

	if err := Restore(ctx, db, &item); err != nil {
		t.Fatal(err)
	}

	again := sdItem{Code: "a", Note: "two"}
	if err := Upsert(ctx, db, &again, on); err != nil {
		t.Errorf("Upsert after Restore = %v", err)
	}
}

func TestUpsertRejectsEncryptedConflictColumn(t *testing.T) {
	t.Parallel()

	type secret struct {
		ID    int64 `db:"id"`
		Token Encrypted[string]
	}

	if _, _, err := upsertPlan(metaOf[secret](), OnConflict{Columns: []string{"token"}}); err == nil {
		t.Error("an encrypted conflict column was accepted")
	}
}

func TestUpsertManyRejectsConflictOnGeneratedKey(t *testing.T) {
	t.Parallel()

	db, ctx := setup(t)

	err := UpsertMany(ctx, db, []User{{Name: "A", Email: "a@x.io"}}, OnConflict{Columns: []string{"id"}})
	if err == nil || !strings.Contains(err.Error(), "generated key") {
		t.Errorf("UpsertMany on the generated key = %v, want an error", err)
	}
}
