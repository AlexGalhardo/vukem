package orm

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/AlexGalhardo/vukem/crypt"
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/internal/testutil"
)

func TestSecurity_A04_ResealRotatesEncryptedColumns(t *testing.T) {
	t.Parallel()

	path := filepath.Join(testutil.TempDir(t), "app.db")
	oldKey, newKey := cryptKey(1), cryptKey(2)
	ctx := t.Context()

	before := openCustomers(t, path, database.WithEncrypter(newEncrypter(t, oldKey)))

	for i := range 7 {
		c := Customer{Name: "c", Card: Encrypted[string]{V: "card"}}
		if i%2 == 0 {
			c.Profile = Encrypted[Profile]{V: Profile{Phone: "555"}}
		}

		if err := Create(ctx, before, &c); err != nil {
			t.Fatal(err)
		}
	}

	// Row 7's card is NULL.
	if _, err := before.ExecContext(ctx, "UPDATE customers SET card = NULL WHERE id = 7"); err != nil {
		t.Fatal(err)
	}
	_ = before.Close()

	enc := newEncrypter(t, newKey, oldKey)
	rotated := openCustomers(t, path, database.WithEncrypter(enc))

	// One row already on the new key is left alone.
	cur, err := QueryOf[Customer](rotated).Find(ctx, int64(1))
	if err != nil {
		t.Fatal(err)
	}

	if err := Update(ctx, rotated, &cur); err != nil {
		t.Fatal(err)
	}

	// Cards: rows 2-6 old, row 1 current, row 7 NULL. Profiles (a zero
	// value is sealed too): rows 2-7 old, row 1 current. 5 + 6 rewritten.
	n, err := Reseal[Customer](ctx, rotated, 2)
	if err != nil || n != 11 {
		t.Fatalf("Reseal() = %d, %v, want 11, nil", n, err)
	}

	if n, err := Reseal[Customer](ctx, rotated, 0); err != nil || n != 0 {
		t.Fatalf("second Reseal() = %d, %v, want 0, nil (idempotent)", n, err)
	}
	_ = rotated.Close()

	fresh := openCustomers(t, path, database.WithEncrypter(newEncrypter(t, newKey)))

	all, err := QueryOf[Customer](fresh).OrderBy("id", Asc).Get(ctx)
	if err != nil || len(all) != 7 {
		t.Fatalf("Get() with the new key only = %d rows, %v", len(all), err)
	}

	if all[1].Card.V != "card" || all[2].Profile.V.Phone != "555" || all[6].Card.V != "" {
		t.Errorf("rows after Reseal = %+v", all)
	}
}

func TestResealFailsClosed(t *testing.T) {
	t.Parallel()

	path := filepath.Join(testutil.TempDir(t), "app.db")
	ctx := t.Context()

	db := openCustomers(t, path, database.WithEncrypter(newEncrypter(t, cryptKey(1))))
	if err := Create(ctx, db, &Customer{Name: "c", Card: Encrypted[string]{V: "card"}}); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	// The old key is gone: the value cannot be opened, Reseal stops.
	lost := openCustomers(t, path, database.WithEncrypter(newEncrypter(t, cryptKey(2))))
	if _, err := Reseal[Customer](ctx, lost, 0); !errors.Is(err, crypt.ErrInvalid) {
		t.Errorf("Reseal() without the old key = %v, want crypt.ErrInvalid", err)
	}

	noEnc := openCustomers(t, path)
	if _, err := Reseal[Customer](ctx, noEnc, 0); !errors.Is(err, ErrNoEncrypter) {
		t.Errorf("Reseal() without encrypter = %v, want ErrNoEncrypter", err)
	}

	type Plain struct {
		Model
		Name string
	}

	if _, err := Reseal[Plain](ctx, noEnc, 0); err == nil {
		t.Error("Reseal() on a model without Encrypted columns = nil error, want an error")
	}
}

func TestResealSkipsConcurrentWrites(t *testing.T) {
	t.Parallel()

	path := filepath.Join(testutil.TempDir(t), "app.db")
	ctx := t.Context()

	old := openCustomers(t, path, database.WithEncrypter(newEncrypter(t, cryptKey(1))))
	if err := Create(ctx, old, &Customer{Name: "c", Card: Encrypted[string]{V: "card"}}); err != nil {
		t.Fatal(err)
	}

	stale := rawColumn(t, old, "card", 1)
	_ = old.Close()

	enc := newEncrypter(t, cryptKey(2), cryptKey(1))
	db := openCustomers(t, path, database.WithEncrypter(enc))

	// A save lands between Reseal's read and its write.
	if _, err := QueryOf[Customer](db).Where("id", 1).Update(ctx, map[string]any{
		"card": Encrypted[string]{V: "new card"},
	}); err != nil {
		t.Fatal(err)
	}

	rs := resealer{db: db, enc: enc, m: metaOf[Customer](), cols: []string{"card"}, stale: enc.NeedsRotation}

	done, err := rs.rewrite(ctx, int64(1), "card", stale)
	if err != nil || done {
		t.Fatalf("rewrite(stale token) = %v, %v, want false, nil", done, err)
	}

	got, err := QueryOf[Customer](db).Find(ctx, int64(1))
	if err != nil || got.Card.V != "new card" {
		t.Errorf("card = %q, %v, want the concurrent save kept", got.Card.V, err)
	}
}
