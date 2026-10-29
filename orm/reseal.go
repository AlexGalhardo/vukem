package orm

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"

	"github.com/AlexGalhardo/vukem/database"
)

// DefaultResealBatch is the batch size Reseal uses when given 0.
const DefaultResealBatch = 500

// Reseal re-encrypts the Encrypted columns of T's table under the current
// key, after an APP_KEY rotation: values sealed with a previous key
// (APP_PREVIOUS_KEYS) are opened and sealed again, values already on the
// current key and NULLs are left alone. It walks the table by primary key,
// batch rows per query (DefaultResealBatch when batch <= 0), soft-deleted
// rows included, and returns how many values it rewrote.
//
// Each value is rewritten only while it still holds the ciphertext that was
// read (compare-and-set), so a save running at the same time is never
// overwritten: it already sealed with the current key. Running Reseal again
// is harmless and rewrites nothing. A value no key opens stops it with an
// error wrapping crypt.ErrInvalid; values rewritten before stay rewritten.
//
// The encrypter of db (app.Crypt) must report stale values with
// NeedsRotation(token) bool, as crypt.Encrypter does; one that does not
// gets every value re-sealed.
func Reseal[T any](ctx context.Context, db database.Executor, batch int) (int, error) {
	m := metaOf[T]()

	var cols []string

	for _, f := range m.fields {
		if reflect.PointerTo(f.typ).Implements(reflect.TypeFor[opener]()) {
			cols = append(cols, f.column)
		}
	}

	if len(cols) == 0 {
		return 0, fmt.Errorf("orm: reseal %s: the model has no Encrypted columns", m.table)
	}

	enc := encrypterOf(db)
	if enc == nil {
		return 0, fmt.Errorf("orm: reseal %s: %w", m.table, ErrNoEncrypter)
	}

	if batch <= 0 {
		batch = DefaultResealBatch
	}

	stale := func(string) bool { return true }
	if r, ok := enc.(interface{ NeedsRotation(token string) bool }); ok {
		stale = r.NeedsRotation
	}

	rs := resealer{db: db, enc: enc, m: m, cols: cols, stale: stale}

	var (
		total int
		last  any
	)

	for {
		n, next, rows, err := rs.batch(ctx, last, batch)
		total += n

		if err != nil || rows < batch {
			return total, err
		}

		last = next
	}
}

type resealer struct {
	db    database.Executor
	enc   database.Encrypter
	m     *meta
	cols  []string
	stale func(string) bool
}

type sealedRow struct {
	key    any
	tokens []sql.NullString
}

// batch re-seals up to limit rows after key last (nil: from the start); it
// returns the values rewritten, the last key read and the rows read.
func (rs resealer) batch(ctx context.Context, last any, limit int) (n int, next any, count int, err error) {
	rows, err := rs.read(ctx, last, limit)
	if err != nil {
		return 0, nil, 0, err
	}

	for _, row := range rows {
		for i, col := range rs.cols {
			tok := row.tokens[i]
			if !tok.Valid || !rs.stale(tok.String) {
				continue
			}

			done, err := rs.rewrite(ctx, row.key, col, tok.String)
			if err != nil {
				return n, nil, 0, err
			}

			if done {
				n++
			}
		}
	}

	if len(rows) > 0 {
		next = rows[len(rows)-1].key
	}

	return n, next, len(rows), nil
}

func (rs resealer) read(ctx context.Context, last any, limit int) ([]sealedRow, error) {
	d := rs.db.Dialect()

	quoted := make([]string, 0, len(rs.cols)+1)
	quoted = append(quoted, d.Quote(rs.m.pk))

	for _, c := range rs.cols {
		quoted = append(quoted, d.Quote(c))
	}

	q := "SELECT " + strings.Join(quoted, ", ") + " FROM " + d.Quote(rs.m.table)
	args := []any{}

	if last != nil {
		q += " WHERE " + d.Quote(rs.m.pk) + " > " + d.Placeholder(1)
		args = append(args, last)
	}

	q += fmt.Sprintf(" ORDER BY %s LIMIT %d", d.Quote(rs.m.pk), limit)

	res, err := rs.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("orm: reseal %s: %w", rs.m.table, err)
	}
	defer res.Close()

	var out []sealedRow

	for res.Next() {
		row := sealedRow{tokens: make([]sql.NullString, len(rs.cols))}
		dest := []any{&row.key}

		for i := range row.tokens {
			dest = append(dest, &row.tokens[i])
		}

		if err := res.Scan(dest...); err != nil {
			return nil, fmt.Errorf("orm: reseal %s: %w", rs.m.table, err)
		}

		// Text keys may scan as []byte, which would not bind back as text.
		if b, ok := row.key.([]byte); ok {
			row.key = string(b)
		}

		out = append(out, row)
	}

	if err := res.Err(); err != nil {
		return nil, fmt.Errorf("orm: reseal %s: %w", rs.m.table, err)
	}

	return out, nil
}

// rewrite seals col of the row with key again; done is false when the
// value changed since it was read (a concurrent save won).
func (rs resealer) rewrite(ctx context.Context, key any, col, token string) (bool, error) {
	ad := columnAD(rs.m.table, col)

	plain, err := rs.enc.Open(token, ad)
	if err != nil {
		return false, fmt.Errorf("orm: reseal %s.%s (%s %v): %w", rs.m.table, col, rs.m.pk, key, err)
	}

	d := rs.db.Dialect()
	q := "UPDATE " + d.Quote(rs.m.table) + " SET " + d.Quote(col) + " = " + d.Placeholder(1) +
		" WHERE " + d.Quote(rs.m.pk) + " = " + d.Placeholder(2) + " AND " + d.Quote(col) + " = " + d.Placeholder(3)

	res, err := rs.db.ExecContext(ctx, q, rs.enc.Seal(plain, ad), key, token)
	if err != nil {
		return false, fmt.Errorf("orm: reseal %s.%s: %w", rs.m.table, col, err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("orm: reseal %s.%s: %w", rs.m.table, col, err)
	}

	return n > 0, nil
}
