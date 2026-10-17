package orm

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/crypt"
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/internal/testutil"
)

type Profile struct {
	Phone string `json:"phone"`
}

type Customer struct {
	Model
	Name    string             `json:"name"`
	Card    Encrypted[string]  `json:"card"`
	Profile Encrypted[Profile] `json:"profile"`
}

const customersDDL = `CREATE TABLE customers (id INTEGER PRIMARY KEY AUTOINCREMENT, created_at TIMESTAMP,
  updated_at TIMESTAMP, name TEXT NOT NULL, card TEXT, profile TEXT)`

func cryptKey(b byte) []byte { return bytes.Repeat([]byte{b}, crypt.KeySize) }

func newEncrypter(t *testing.T, current []byte, previous ...[]byte) *crypt.Encrypter {
	t.Helper()

	e, err := crypt.New(current, previous...)
	if err != nil {
		t.Fatal(err)
	}

	return e
}

// openCustomers opens the SQLite file at path (creating the table once)
// with the given options.
func openCustomers(t *testing.T, path string, opts ...database.Option) *database.DB {
	t.Helper()

	db, err := database.Open(t.Context(), "file:"+path, opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() }) // Windows: close before TempDir removal

	if _, err := db.ExecContext(
		t.Context(),
		strings.Replace(customersDDL, "TABLE", "TABLE IF NOT EXISTS", 1),
	); err != nil {
		t.Fatal(err)
	}

	return db
}

func rawColumn(t *testing.T, db *database.DB, col string, id int64) string {
	t.Helper()

	var v string
	if err := db.QueryRowContext(t.Context(), "SELECT "+col+" FROM customers WHERE id = ?", id).Scan(&v); err != nil {
		t.Fatal(err)
	}

	return v
}

func TestSecurity_A04_EncryptedColumnStoresNoPlaintext(t *testing.T) {
	t.Parallel()

	enc := newEncrypter(t, cryptKey(1))
	db := openCustomers(t, filepath.Join(testutil.TempDir(t), "app.db"), database.WithEncrypter(enc))
	ctx := t.Context()

	c := Customer{
		Name:    "Ada",
		Card:    Encrypted[string]{V: "4111111111111111"},
		Profile: Encrypted[Profile]{V: Profile{Phone: "+55 11 5555"}},
	}
	if err := Create(ctx, db, &c); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	for col, plain := range map[string]string{"card": "4111111111111111", "profile": "5555"} {
		if raw := rawColumn(t, db, col, c.ID); strings.Contains(raw, plain) || raw == "" {
			t.Fatalf("column %s = %q, want ciphertext without %q", col, raw, plain)
		}
	}

	got, err := QueryOf[Customer](db).Find(ctx, c.ID)
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}

	if got.Card.V != "4111111111111111" || got.Profile.V.Phone != "+55 11 5555" {
		t.Fatalf("Find() = %+v, want decrypted values", got)
	}

	got.Card.V = "5500000000000004"
	if err := Update(ctx, db, &got); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	if _, err := QueryOf[Customer](
		db,
	).Where("id", c.ID).
		Update(ctx, Set{"profile": Encrypted[Profile]{V: Profile{Phone: "+1"}}}); err != nil {
		t.Fatalf("Query.Update() error = %v", err)
	}

	cards, err := QueryOf[Customer](db).Pluck[Encrypted[string]](ctx, "card")
	if err != nil || len(cards) != 1 || cards[0].V != "5500000000000004" {
		t.Fatalf("Pluck(card) = %v, %v, want the updated card", cards, err)
	}

	all, err := QueryOf[Customer](db).Get(ctx)
	if err != nil || len(all) != 1 || all[0].Profile.V.Phone != "+1" {
		t.Fatalf("Get() = %+v, %v, want the updated profile", all, err)
	}
}

func TestSecurity_A04_EncryptedColumnKeyRotation(t *testing.T) {
	t.Parallel()

	path := filepath.Join(testutil.TempDir(t), "app.db")
	oldKey, newKey := cryptKey(1), cryptKey(2)
	ctx := t.Context()

	before := openCustomers(t, path, database.WithEncrypter(newEncrypter(t, oldKey)))

	c := Customer{Name: "Ada", Card: Encrypted[string]{V: "4111"}}
	if err := Create(ctx, before, &c); err != nil {
		t.Fatal(err)
	}
	_ = before.Close()

	rotated := openCustomers(t, path, database.WithEncrypter(newEncrypter(t, newKey, oldKey)))

	got, err := QueryOf[Customer](rotated).Find(ctx, c.ID)
	if err != nil || got.Card.V != "4111" {
		t.Fatalf("Find() with previous key = %+v, %v, want card 4111", got, err)
	}

	// Saving re-seals every encrypted column with the current key.
	if err := Update(ctx, rotated, &got); err != nil {
		t.Fatal(err)
	}
	_ = rotated.Close()

	fresh := openCustomers(t, path, database.WithEncrypter(newEncrypter(t, newKey)))

	got, err = QueryOf[Customer](fresh).Find(ctx, c.ID)
	if err != nil || got.Card.V != "4111" {
		t.Fatalf("Find() after re-save with the new key only = %+v, %v, want card 4111", got, err)
	}
}

func TestSecurity_A04_EncryptedColumnFailsClosed(t *testing.T) {
	t.Parallel()

	path := filepath.Join(testutil.TempDir(t), "app.db")
	ctx := t.Context()
	enc := newEncrypter(t, cryptKey(1))
	withKey := openCustomers(t, path, database.WithEncrypter(enc))
	noKey := openCustomers(t, path)

	c := Customer{Name: "Ada", Card: Encrypted[string]{V: "4111"}}
	if err := Create(ctx, noKey, &c); !errors.Is(err, ErrNoEncrypter) {
		t.Fatalf("Create() without encrypter error = %v, want ErrNoEncrypter", err)
	}

	if n, _ := QueryOf[Customer](noKey).Count(ctx); n != 0 {
		t.Fatalf("Create() without encrypter stored %d rows, want 0", n)
	}

	if _, err := QueryOf[Customer](
		noKey,
	).Update(ctx, Set{"card": Encrypted[string]{V: "x"}}); !errors.Is(
		err,
		ErrNoEncrypter,
	) {
		t.Fatalf("Query.Update() without encrypter error = %v, want ErrNoEncrypter", err)
	}

	if err := Create(ctx, withKey, &c); err != nil {
		t.Fatal(err)
	}

	if _, err := QueryOf[Customer](noKey).Find(ctx, c.ID); !errors.Is(err, ErrNoEncrypter) {
		t.Fatalf("Find() without encrypter error = %v, want ErrNoEncrypter", err)
	}

	if _, err := QueryOf[Customer](noKey).Pluck[Encrypted[string]](ctx, "card"); !errors.Is(err, ErrNoEncrypter) {
		t.Fatalf("Pluck() without encrypter error = %v, want ErrNoEncrypter", err)
	}

	// Raw database/sql access never sees plaintext either way.
	if _, err := withKey.ExecContext(ctx, "UPDATE customers SET card = ?", Encrypted[string]{V: "plain"}); err == nil {
		t.Fatal("ExecContext(Encrypted value) error = nil, want an error")
	}

	var direct Encrypted[string]
	if err := withKey.QueryRowContext(ctx, "SELECT card FROM customers").Scan(&direct); err == nil {
		t.Fatal("Scan(&Encrypted) error = nil, want an error")
	}

	// A ciphertext moved to another column (or tampered with) is rejected.
	if _, err := withKey.ExecContext(ctx, "UPDATE customers SET profile = card"); err != nil {
		t.Fatal(err)
	}

	if _, err := QueryOf[Customer](withKey).Find(ctx, c.ID); !errors.Is(err, crypt.ErrInvalid) {
		t.Fatalf("Find() with a ciphertext moved between columns error = %v, want crypt.ErrInvalid", err)
	}
}

func TestEncryptedJSONAndNull(t *testing.T) {
	t.Parallel()

	b, err := json.Marshal(Customer{Name: "Ada", Card: Encrypted[string]{V: "4111"}})
	if err != nil || !strings.Contains(string(b), `"card":"4111"`) {
		t.Fatalf("json.Marshal() = %s, %v, want the plain card", b, err)
	}

	var c Customer
	if err := json.Unmarshal([]byte(`{"card":"5500","profile":{"phone":"1"}}`), &c); err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(Profile{Phone: "1"}, c.Profile.V); diff != "" || c.Card.V != "5500" {
		t.Fatalf("json.Unmarshal() mismatch (-want +got):\n%s card=%q", diff, c.Card.V)
	}

	enc := newEncrypter(t, cryptKey(1))
	db := openCustomers(t, filepath.Join(testutil.TempDir(t), "app.db"), database.WithEncrypter(enc))

	res, err := db.ExecContext(
		t.Context(),
		"INSERT INTO customers (name, created_at, updated_at, card, profile) VALUES ('n', ?, ?, NULL, NULL)",
		time.Now(),
		time.Now(),
	)
	if err != nil {
		t.Fatal(err)
	}

	id, _ := res.LastInsertId()

	got, err := QueryOf[Customer](db).Find(t.Context(), id)
	if err != nil || got.Card.V != "" || got.Profile.V != (Profile{}) {
		t.Fatalf("Find(NULL columns) = %+v, %v, want zero values", got, err)
	}
}

// TestEncryptedManyRows checks that the scan buffers shared across rows
// never leak one row's ciphertext or NULL into another.
func TestEncryptedManyRows(t *testing.T) {
	t.Parallel()

	db := openCustomers(t, filepath.Join(testutil.TempDir(t), "app.db"),
		database.WithEncrypter(newEncrypter(t, cryptKey(1))))
	ctx := t.Context()

	want := []string{"4111", "", "5500"}
	for i, card := range want {
		c := Customer{Name: "c" + strings.Repeat("x", i), Card: Encrypted[string]{V: card}}
		if err := Create(ctx, db, &c); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := db.ExecContext(ctx, "UPDATE customers SET card = NULL WHERE name = 'cx'"); err != nil {
		t.Fatal(err)
	}

	all, err := QueryOf[Customer](db).OrderBy("id", Asc).Limit(10).Get(ctx)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	got := make([]string, 0, len(all))
	for _, c := range all {
		got = append(got, c.Card.V)
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Get() cards mismatch (-want +got):\n%s", diff)
	}
}
