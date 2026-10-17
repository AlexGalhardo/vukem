package cache

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/database"
)

// Database stores entries in a table (SQLite, Postgres or MySQL):
// cache(key TEXT PRIMARY KEY, value TEXT NOT NULL, expires_at BIGINT); value
// is BYTEA on Postgres
// with expires_at in Unix milliseconds (0 = never). CreateTable creates it.
// On MySQL key and value are binary (VARBINARY, LONGBLOB) so lookups stay
// exact and case-sensitive.
type Database struct {
	db    *database.DB
	table string
	now   func() time.Time
}

var tableRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// NewDatabase returns a database store using table (default "cache").
func NewDatabase(db *database.DB, table string) (*Database, error) {
	if table == "" {
		table = "cache"
	}

	if !tableRe.MatchString(table) {
		return nil, fmt.Errorf("cache: invalid table name %q", table)
	}

	return &Database{db: db, table: table, now: time.Now}, nil
}

func (d *Database) mysql() bool { return d.db.Dialect().Name() == "mysql" }

func (d *Database) postgres() bool { return d.db.Dialect().Name() == "postgres" }

// val binds a value: Postgres stores BYTEA (TEXT rejects NUL and invalid
// UTF-8); SQLite keeps binding strings so rows written by older versions
// still compare equal in DeleteIf.
func (d *Database) val(b []byte) any {
	if d.postgres() {
		return b
	}

	return string(b)
}

// q renders sqlText with the table name, the key column ({k}, a reserved
// word on MySQL) and dialect placeholders.
func (d *Database) q(sqlText string) string {
	dl := d.db.Dialect()

	key := "key"
	if d.mysql() {
		key = dl.Quote(key)
	}

	sqlText = strings.ReplaceAll(sqlText, "{k}", key)

	var b strings.Builder

	n := 0

	for _, ch := range strings.ReplaceAll(sqlText, "{t}", dl.Quote(d.table)) {
		if ch == '?' {
			n++
			b.WriteString(dl.Placeholder(n))

			continue
		}

		b.WriteRune(ch)
	}

	return b.String()
}

// CreateTable creates the cache table if missing.
func (d *Database) CreateTable(ctx context.Context) error {
	stmt := `CREATE TABLE IF NOT EXISTS {t} ({k} TEXT PRIMARY KEY, value TEXT NOT NULL, expires_at BIGINT NOT NULL DEFAULT 0)`
	switch {
	case d.mysql():
		stmt = `CREATE TABLE IF NOT EXISTS {t} ({k} VARBINARY(255) NOT NULL PRIMARY KEY, value LONGBLOB NOT NULL,
			expires_at BIGINT NOT NULL DEFAULT 0)`
	case d.postgres():
		stmt = `CREATE TABLE IF NOT EXISTS {t} ({k} TEXT PRIMARY KEY, value BYTEA NOT NULL, expires_at BIGINT NOT NULL DEFAULT 0)`
	}

	if _, err := d.db.ExecContext(ctx, d.q(stmt)); err != nil {
		return err
	}

	if !d.postgres() {
		return nil
	}

	// Tables created before v0.22 have a TEXT value column: convert it once.
	var typ string

	err := d.db.QueryRowContext(ctx, `SELECT data_type FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = $1 AND column_name = 'value'`, d.table).Scan(&typ)
	if err != nil || typ == "bytea" {
		return err
	}

	_, err = d.db.ExecContext(ctx, d.q(`ALTER TABLE {t} ALTER COLUMN value TYPE BYTEA USING convert_to(value, 'UTF8')`))

	return err
}

// upsert inserts or replaces one entry.
func (d *Database) upsert() string {
	if d.mysql() {
		return d.q(`INSERT INTO {t} ({k}, value, expires_at) VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE value = VALUES(value), expires_at = VALUES(expires_at)`)
	}

	return d.q(`INSERT INTO {t} ({k}, value, expires_at) VALUES (?, ?, ?)
		ON CONFLICT ({k}) DO UPDATE SET value = excluded.value, expires_at = excluded.expires_at`)
}

func (d *Database) nowMS() int64 { return d.now().UnixMilli() }

func (d *Database) expiry(ttl time.Duration) int64 {
	if ttl <= 0 {
		return 0
	}

	return d.now().Add(ttl).UnixMilli()
}

// Get implements Store.
func (d *Database) Get(ctx context.Context, key string) ([]byte, bool, error) {
	var v []byte

	err := d.db.QueryRowContext(ctx, d.q(`SELECT value FROM {t} WHERE {k} = ? AND (expires_at = 0 OR expires_at > ?)`),
		key, d.nowMS()).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}

	return v, err == nil, err
}

// Set implements Store.
func (d *Database) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	_, err := d.db.ExecContext(ctx, d.upsert(), key, d.val(value), d.expiry(ttl))

	return err
}

// Add implements Store: expired rows are replaced, live ones kept.
func (d *Database) Add(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	if d.mysql() {
		return d.addMySQL(ctx, key, value, ttl)
	}

	res, err := d.db.ExecContext(ctx, d.q(`INSERT INTO {t} ({k}, value, expires_at) VALUES (?, ?, ?)
		ON CONFLICT ({k}) DO UPDATE SET value = excluded.value, expires_at = excluded.expires_at
		WHERE {t}.expires_at <> 0 AND {t}.expires_at <= ?`),
		key, d.val(value), d.expiry(ttl), d.nowMS())
	if err != nil {
		return false, err
	}

	n, err := res.RowsAffected()

	return n == 1, err
}

// addMySQL deletes an expired entry, then inserts: a live entry makes the
// insert fail with a duplicate key, which means "not added". The affected
// row count of ON DUPLICATE KEY UPDATE cannot tell an insert from a no-op
// update once the driver reports found rows.
func (d *Database) addMySQL(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	if _, err := d.db.ExecContext(ctx, d.q(`DELETE FROM {t} WHERE {k} = ? AND expires_at <> 0 AND expires_at <= ?`),
		key, d.nowMS()); err != nil {
		return false, err
	}

	_, err := d.db.ExecContext(ctx, d.q(`INSERT INTO {t} ({k}, value, expires_at) VALUES (?, ?, ?)`),
		key, d.val(value), d.expiry(ttl))
	if err != nil {
		if isDuplicateKey(err) {
			return false, nil
		}

		return false, err
	}

	return true, nil
}

// isDuplicateKey matches MySQL error 1062 without importing the driver.
func isDuplicateKey(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "Error 1062") || strings.Contains(strings.ToLower(msg), "duplicate entry")
}

// Delete implements Store.
func (d *Database) Delete(ctx context.Context, keys ...string) error {
	for _, k := range keys {
		if _, err := d.db.ExecContext(ctx, d.q(`DELETE FROM {t} WHERE {k} = ?`), k); err != nil {
			return err
		}
	}

	return nil
}

// DeleteIf implements Store.
func (d *Database) DeleteIf(ctx context.Context, key string, value []byte) (bool, error) {
	res, err := d.db.ExecContext(ctx, d.q(`DELETE FROM {t} WHERE {k} = ? AND value = ?`), key, d.val(value))
	if err != nil {
		return false, err
	}

	n, err := res.RowsAffected()

	return n == 1, err
}

// Increment implements Store, atomically across connections and servers:
// a no-op upsert first creates the row ("0") or locks the existing one, so
// concurrent increments of a key queue up instead of reading the same
// value (rate limits count with it). A live row keeps its expiry; an
// expired one restarts at delta with ttl.
func (d *Database) Increment(ctx context.Context, key string, delta int64, ttl time.Duration) (int64, error) {
	lock := d.q(`INSERT INTO {t} ({k}, value, expires_at) VALUES (?, ?, ?)
		ON CONFLICT ({k}) DO UPDATE SET expires_at = {t}.expires_at`)
	read := d.q(`SELECT value, expires_at FROM {t} WHERE {k} = ?`)

	if d.mysql() {
		lock = d.q(`INSERT INTO {t} ({k}, value, expires_at) VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE expires_at = expires_at`)
	}

	if d.mysql() || d.postgres() {
		read += " FOR UPDATE" // SQLite has one writer: the upsert already holds the lock
	}

	var n int64

	err := d.db.Tx(ctx, func(tx *database.Tx) error {
		if _, err := tx.ExecContext(ctx, lock, key, d.val([]byte("0")), d.expiry(ttl)); err != nil {
			return err
		}

		var (
			v       []byte
			expires int64
		)

		if err := tx.QueryRowContext(ctx, read, key).Scan(&v, &expires); err != nil {
			return err
		}

		var cur int64

		if expires != 0 && expires <= d.nowMS() {
			expires = d.expiry(ttl)
		} else {
			var err error
			if cur, err = strconv.ParseInt(string(bytes.TrimSpace(v)), 10, 64); err != nil {
				return fmt.Errorf("cache: %s is not an integer", key)
			}
		}

		n = cur + delta
		_, err := tx.ExecContext(ctx, d.q(`UPDATE {t} SET value = ?, expires_at = ? WHERE {k} = ?`),
			d.val([]byte(strconv.FormatInt(n, 10))), expires, key)

		return err
	})

	return n, err
}

// Flush implements Store.
func (d *Database) Flush(ctx context.Context) error {
	_, err := d.db.ExecContext(ctx, d.q(`DELETE FROM {t}`))
	return err
}

// Prune deletes expired rows (schedule it, e.g. hourly).
func (d *Database) Prune(ctx context.Context) (int64, error) {
	res, err := d.db.ExecContext(ctx, d.q(`DELETE FROM {t} WHERE expires_at <> 0 AND expires_at <= ?`), d.nowMS())
	if err != nil {
		return 0, err
	}

	return res.RowsAffected()
}
