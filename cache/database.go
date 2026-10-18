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
	db         *database.DB
	table      string
	now        func() time.Time
	pruneBatch int
}

var tableRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// DefaultTable is the table the database store owns when NewDatabase is
// called with an empty name (CACHE_TABLE). It is listed with every other
// table vukem owns in docs/database.md#tables-the-framework-owns.
const DefaultTable = "cache"

// checkTable rejects a name that is not a plain identifier: it is
// interpolated into SQL, values never are.
func checkTable(table string) error {
	if !tableRe.MatchString(table) {
		return fmt.Errorf("cache: invalid table name %q", table)
	}

	return nil
}

// NewDatabase returns a database store using table (default "cache").
func NewDatabase(db *database.DB, table string) (*Database, error) {
	if table == "" {
		table = DefaultTable
	}

	if err := checkTable(table); err != nil {
		return nil, err
	}

	return &Database{db: db, table: table, now: time.Now, pruneBatch: 1000}, nil
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
func (d *Database) q(sqlText string) string { return render(d.db.Dialect(), d.table, sqlText) }

// render substitutes the table name ({t}), the key column ({k}, a
// reserved word on MySQL) and dialect placeholders.
func render(dl database.Dialect, table, sqlText string) string {
	key := "key"
	if dl.Name() == "mysql" {
		key = dl.Quote(key)
	}

	sqlText = strings.ReplaceAll(sqlText, "{k}", key)

	var b strings.Builder

	n := 0

	for _, ch := range strings.ReplaceAll(sqlText, "{t}", dl.Quote(table)) {
		if ch == '?' {
			n++
			b.WriteString(dl.Placeholder(n))

			continue
		}

		b.WriteRune(ch)
	}

	return b.String()
}

// tableDDL returns the statements that create table and its expires_at
// index on dl. CreateTable runs them at boot and the migration from
// Migrations runs the same list, so the two paths cannot drift
// (TestMigrationMatchesCreateTableDDL). MySQL stores binary keys and
// values so lookups stay exact and case-sensitive, and has no
// CREATE INDEX IF NOT EXISTS; PostgreSQL rejects NUL bytes in TEXT.
func tableDDL(dl database.Dialect, table string) []string {
	create := `CREATE TABLE IF NOT EXISTS {t} ({k} TEXT PRIMARY KEY, value TEXT NOT NULL,
		expires_at BIGINT NOT NULL DEFAULT 0)`
	index := "CREATE INDEX IF NOT EXISTS " + dl.Quote(table+"_expires_at") + " ON {t} (expires_at)"

	switch dl.Name() {
	case "mysql":
		create = `CREATE TABLE IF NOT EXISTS {t} ({k} VARBINARY(255) NOT NULL PRIMARY KEY, value LONGBLOB NOT NULL,
			expires_at BIGINT NOT NULL DEFAULT 0)`
		index = "CREATE INDEX " + dl.Quote(table+"_expires_at") + " ON {t} (expires_at)"
	case "postgres":
		create = `CREATE TABLE IF NOT EXISTS {t} ({k} TEXT PRIMARY KEY, value BYTEA NOT NULL,
			expires_at BIGINT NOT NULL DEFAULT 0)`
	}

	return []string{render(dl, table, create), render(dl, table, index)}
}

// CreateTable creates the cache table if missing. It is the fallback for
// apps that do not register Migrations: the statements are the same, so
// running it after the migration changes nothing.
func (d *Database) CreateTable(ctx context.Context) error {
	ddl := tableDDL(d.db.Dialect(), d.table)

	if _, err := d.db.ExecContext(ctx, ddl[0]); err != nil {
		return err
	}

	if err := d.createExpiryIndex(ctx, ddl[1]); err != nil {
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

// createExpiryIndex indexes expires_at for Prune. MySQL has no CREATE
// INDEX IF NOT EXISTS: it checks first, and tolerates a server that
// booted at the same time and won the race (error 1061).
func (d *Database) createExpiryIndex(ctx context.Context, stmt string) error {
	name := d.table + "_expires_at"

	if !d.mysql() {
		_, err := d.db.ExecContext(ctx, stmt)
		return err
	}

	var n int
	if err := d.db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.statistics
		WHERE table_schema = DATABASE() AND table_name = ? AND index_name = ?`, d.table, name).Scan(&n); err != nil || n > 0 {
		return err
	}

	_, err := d.db.ExecContext(ctx, stmt)
	if err != nil && strings.Contains(err.Error(), "Error 1061") {
		return nil
	}

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

// Increment implements Store with one upsert that adds delta to the row
// (or creates it), so it is atomic across connections and servers: rate
// limits count with it. A live row keeps its expiry; an expired one
// restarts at delta with ttl. A value that is not an integer is left as
// it is and Increment fails. MySQL has no RETURNING: there the upsert and
// a SELECT of the locked row share a transaction.
func (d *Database) Increment(ctx context.Context, key string, delta int64, ttl time.Duration) (int64, error) {
	now, start := d.nowMS(), d.val([]byte(strconv.FormatInt(delta, 10)))

	var v []byte

	err := retryOnConflict(func() error {
		if d.mysql() {
			return d.db.Tx(ctx, func(tx *database.Tx) error {
				if _, err := tx.ExecContext(
					ctx,
					d.incrementSQL(),
					key,
					start,
					d.expiry(ttl),
					now,
					delta,
					now,
				); err != nil {
					return err
				}

				return tx.QueryRowContext(ctx, d.q(`SELECT value FROM {t} WHERE {k} = ?`), key).Scan(&v)
			})
		}

		return d.db.QueryRowContext(ctx, d.incrementSQL(), key, start, d.expiry(ttl), now, delta, now).Scan(&v)
	})
	if err != nil {
		return 0, err
	}

	// The SQL writes canonical integers; anything else is a value it left
	// alone because it was not one.
	s := string(bytes.TrimSpace(v))

	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || strconv.FormatInt(n, 10) != s {
		return 0, fmt.Errorf("cache: %s is not an integer", key)
	}

	return n, nil
}

// incrementSQL binds key, start value, expiry, now, delta, now. Postgres
// casts the bytes (a non-integer fails the statement); SQLite and MySQL
// only add to a value that round-trips as an integer and keep others.
func (d *Database) incrementSQL() string {
	switch {
	case d.postgres():
		return d.q(`INSERT INTO {t} ({k}, value, expires_at) VALUES (?, ?, ?)
		ON CONFLICT ({k}) DO UPDATE SET
			value = CASE WHEN {t}.expires_at <> 0 AND {t}.expires_at <= ? THEN excluded.value
				ELSE convert_to((convert_from({t}.value, 'UTF8')::bigint + ?)::text, 'UTF8') END,
			expires_at = CASE WHEN {t}.expires_at <> 0 AND {t}.expires_at <= ? THEN excluded.expires_at
				ELSE {t}.expires_at END
		RETURNING value`)
	case d.mysql():
		// Assignments run left to right: value reads the old expires_at.
		return d.q(`INSERT INTO {t} ({k}, value, expires_at) VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE
			value = CASE WHEN expires_at <> 0 AND expires_at <= ? THEN VALUES(value)
				WHEN CAST(CAST(TRIM(value) AS SIGNED) AS CHAR) = TRIM(value)
				THEN CAST(CAST(TRIM(value) AS SIGNED) + ? AS CHAR)
				ELSE value END,
			expires_at = CASE WHEN expires_at <> 0 AND expires_at <= ? THEN VALUES(expires_at) ELSE expires_at END`)
	default:
		return d.q(`INSERT INTO {t} ({k}, value, expires_at) VALUES (?, ?, ?)
		ON CONFLICT ({k}) DO UPDATE SET
			value = CASE WHEN {t}.expires_at <> 0 AND {t}.expires_at <= ? THEN excluded.value
				WHEN CAST(CAST(trim({t}.value) AS INTEGER) AS TEXT) = trim({t}.value)
				THEN CAST(CAST(trim({t}.value) AS INTEGER) + ? AS TEXT)
				ELSE {t}.value END,
			expires_at = CASE WHEN {t}.expires_at <> 0 AND {t}.expires_at <= ? THEN excluded.expires_at
				ELSE {t}.expires_at END
		RETURNING value`)
	}
}

// retryOnConflict runs fn again once when the database aborted it to
// break a deadlock or a serialization conflict; a second failure is
// returned (rate limits then deny the request).
func retryOnConflict(fn func() error) error {
	err := fn()
	if err != nil && isConflict(err) {
		err = fn()
	}

	return err
}

// isConflict matches MySQL error 1213 and Postgres SQLSTATE 40001/40P01
// without importing the drivers.
func isConflict(err error) bool {
	msg := err.Error()

	return strings.Contains(msg, "Error 1213") || strings.Contains(msg, "SQLSTATE 40001") ||
		strings.Contains(msg, "SQLSTATE 40P01")
}

// Flush implements Store.
func (d *Database) Flush(ctx context.Context) error {
	_, err := d.db.ExecContext(ctx, d.q(`DELETE FROM {t}`))
	return err
}

// Prune deletes expired rows (scheduled hourly as cache:prune), in
// batches so a large backlog never holds one long lock.
func (d *Database) Prune(ctx context.Context) (int64, error) {
	stmt := d.q(`DELETE FROM {t} WHERE {k} IN
		(SELECT {k} FROM {t} WHERE expires_at <> 0 AND expires_at <= ? LIMIT ?)`)
	if d.mysql() {
		stmt = d.q(`DELETE FROM {t} WHERE expires_at <> 0 AND expires_at <= ? LIMIT ?`)
	}

	var total int64

	for {
		res, err := d.db.ExecContext(ctx, stmt, d.nowMS(), d.pruneBatch)
		if err != nil {
			return total, err
		}

		n, err := res.RowsAffected()
		if err != nil {
			return total, err
		}

		total += n
		if n < int64(d.pruneBatch) {
			return total, nil
		}
	}
}
