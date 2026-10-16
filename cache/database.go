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

// Database stores entries in a table (SQLite or Postgres):
// cache(key TEXT PRIMARY KEY, value TEXT NOT NULL, expires_at BIGINT)
// with expires_at in Unix milliseconds (0 = never). CreateTable creates it.
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

// q renders sqlText with the table name and dialect placeholders.
func (d *Database) q(sqlText string) string {
	dl := d.db.Dialect()

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
	_, err := d.db.ExecContext(
		ctx,
		d.q(
			`CREATE TABLE IF NOT EXISTS {t} (key TEXT PRIMARY KEY, value TEXT NOT NULL, expires_at BIGINT NOT NULL DEFAULT 0)`,
		),
	)
	return err
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
	var v string

	err := d.db.QueryRowContext(ctx, d.q(`SELECT value FROM {t} WHERE key = ? AND (expires_at = 0 OR expires_at > ?)`),
		key, d.nowMS()).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}

	return []byte(v), err == nil, err
}

// Set implements Store.
func (d *Database) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	_, err := d.db.ExecContext(ctx, d.q(`INSERT INTO {t} (key, value, expires_at) VALUES (?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, expires_at = excluded.expires_at`),
		key, string(value), d.expiry(ttl))

	return err
}

// Add implements Store: expired rows are replaced, live ones kept.
func (d *Database) Add(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	res, err := d.db.ExecContext(ctx, d.q(`INSERT INTO {t} (key, value, expires_at) VALUES (?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, expires_at = excluded.expires_at
		WHERE {t}.expires_at <> 0 AND {t}.expires_at <= ?`),
		key, string(value), d.expiry(ttl), d.nowMS())
	if err != nil {
		return false, err
	}

	n, err := res.RowsAffected()

	return n == 1, err
}

// Delete implements Store.
func (d *Database) Delete(ctx context.Context, keys ...string) error {
	for _, k := range keys {
		if _, err := d.db.ExecContext(ctx, d.q(`DELETE FROM {t} WHERE key = ?`), k); err != nil {
			return err
		}
	}

	return nil
}

// DeleteIf implements Store.
func (d *Database) DeleteIf(ctx context.Context, key string, value []byte) (bool, error) {
	res, err := d.db.ExecContext(ctx, d.q(`DELETE FROM {t} WHERE key = ? AND value = ?`), key, string(value))
	if err != nil {
		return false, err
	}

	n, err := res.RowsAffected()

	return n == 1, err
}

// Increment implements Store (atomically, in a transaction).
func (d *Database) Increment(ctx context.Context, key string, delta int64, ttl time.Duration) (int64, error) {
	var n int64

	err := d.db.Tx(ctx, func(tx *database.Tx) error {
		var v string

		err := tx.QueryRowContext(ctx, d.q(`SELECT value FROM {t} WHERE key = ? AND (expires_at = 0 OR expires_at > ?)`),
			key, d.nowMS()).
			Scan(&v)

		switch {
		case errors.Is(err, sql.ErrNoRows):
			n = delta
			_, err = tx.ExecContext(ctx, d.q(`INSERT INTO {t} (key, value, expires_at) VALUES (?, ?, ?)
				ON CONFLICT (key) DO UPDATE SET value = excluded.value, expires_at = excluded.expires_at`),
				key, strconv.FormatInt(n, 10), d.expiry(ttl))

			return err
		case err != nil:
			return err
		}

		cur, err := strconv.ParseInt(string(bytes.TrimSpace([]byte(v))), 10, 64)
		if err != nil {
			return fmt.Errorf("cache: %s is not an integer", key)
		}

		n = cur + delta
		_, err = tx.ExecContext(ctx, d.q(`UPDATE {t} SET value = ? WHERE key = ?`), strconv.FormatInt(n, 10), key)

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
