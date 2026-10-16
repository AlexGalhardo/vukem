package orm

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/AlexGalhardo/vukem/database"
)

// ErrNoEncrypter is returned when a model with Encrypted columns is written
// or read through an executor without an encrypter (see
// database.WithEncrypter). Values are never stored in plaintext instead.
var ErrNoEncrypter = errors.New("orm: encrypted columns need an encrypter (database.WithEncrypter)")

// Encrypted stores T as JSON encrypted with the executor's encrypter
// (AES-256-GCM via app.Crypt). The table and column are authenticated with
// the value, so a ciphertext copied to another column is rejected; values
// sealed with a previous key (APP_PREVIOUS_KEYS) still decrypt and are
// re-sealed with the current key on the next save.
//
// Encrypted columns are written and read only through orm (Create, Update,
// Query.Update, queries and Pluck); passing an Encrypted value to
// database/sql directly fails. They cannot be filtered with Where because
// every encryption is randomized. NULL reads as the zero value. JSON
// encoding (API output) uses the plain V: tag the field `json:"-"` to hide
// it.
type Encrypted[T any] struct {
	V T
}

// sealer and opener are implemented by Encrypted; orm uses them to swap
// plain values for ciphertext at the database boundary.
type (
	sealer interface {
		sealColumn(enc database.Encrypter, ad []byte) (string, error)
	}
	opener interface {
		openColumn(enc database.Encrypter, token sql.NullString, ad []byte) error
	}
)

func (e Encrypted[T]) sealColumn(enc database.Encrypter, ad []byte) (string, error) {
	b, err := json.Marshal(e.V)
	if err != nil {
		return "", err
	}

	return enc.Seal(b, ad), nil
}

func (e *Encrypted[T]) openColumn(enc database.Encrypter, token sql.NullString, ad []byte) error {
	var zero T

	e.V = zero

	if !token.Valid {
		return nil
	}

	plain, err := enc.Open(token.String, ad)
	if err != nil {
		return err
	}

	return json.Unmarshal(plain, &e.V)
}

// Value implements driver.Valuer and always fails: Encrypted values are
// sealed by orm writes, never sent to the database as plaintext.
func (Encrypted[T]) Value() (driver.Value, error) {
	return nil, errors.New("orm: Encrypted values are written through orm (Create, Update, Query.Update)")
}

// Scan implements sql.Scanner and always fails: Encrypted columns are
// opened by orm queries.
func (*Encrypted[T]) Scan(any) error {
	return errors.New("orm: Encrypted columns are read through orm queries")
}

// MarshalJSON encodes the plain inner value.
func (e Encrypted[T]) MarshalJSON() ([]byte, error) { return json.Marshal(e.V) }

// UnmarshalJSON decodes into the inner value.
func (e *Encrypted[T]) UnmarshalJSON(b []byte) error { return json.Unmarshal(b, &e.V) }

func encrypterOf(ex database.Executor) database.Encrypter {
	if h, ok := ex.(interface{ Encrypter() database.Encrypter }); ok {
		return h.Encrypter()
	}

	return nil
}

// columnAD is the additional data binding a ciphertext to its column.
func columnAD(table, col string) []byte { return []byte("orm:" + table + "." + col) }

// bindValue seals Encrypted values for table.col; other values pass through.
func bindValue(ex database.Executor, table, col string, v any) (any, error) {
	s, ok := v.(sealer)
	if !ok {
		return v, nil
	}

	enc := encrypterOf(ex)
	if enc == nil {
		return nil, fmt.Errorf("orm: write %s.%s: %w", table, col, ErrNoEncrypter)
	}

	token, err := s.sealColumn(enc, columnAD(table, col))
	if err != nil {
		return nil, fmt.Errorf("orm: encrypt %s.%s: %w", table, col, err)
	}

	return token, nil
}

// openValue decrypts token into dst (an opener) for table.col.
func openValue(enc database.Encrypter, dst opener, token sql.NullString, table, col string) error {
	if enc == nil {
		return fmt.Errorf("orm: read %s.%s: %w", table, col, ErrNoEncrypter)
	}

	if err := dst.openColumn(enc, token, columnAD(table, col)); err != nil {
		return fmt.Errorf("orm: decrypt %s.%s: %w", table, col, err)
	}

	return nil
}

// bareColumn strips a "table." qualifier.
func bareColumn(name string) string {
	if _, col, ok := strings.Cut(name, "."); ok {
		return col
	}

	return name
}
