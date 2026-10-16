// Package database manages SQL connections for Lumek: dialects, SQLite
// defaults (WAL, busy timeout, foreign keys, a single-connection writer pool
// beside a reader pool), PostgreSQL through driver modules, and
// transactions that roll back on error or panic.
package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver ("sqlite"); no cgo, works on Windows
)

// Dialect describes SQL differences between databases.
type Dialect interface {
	// Name is "sqlite" or "postgres".
	Name() string
	// Placeholder returns the n-th (1-based) bind placeholder.
	Placeholder(n int) string
	// Quote quotes an identifier (already validated by callers).
	Quote(ident string) string
}

type sqliteDialect struct{}

func (sqliteDialect) Name() string              { return "sqlite" }
func (sqliteDialect) Placeholder(int) string    { return "?" }
func (sqliteDialect) Quote(ident string) string { return quoteIdent(ident) }

type postgresDialect struct{}

func (postgresDialect) Name() string              { return "postgres" }
func (postgresDialect) Placeholder(n int) string  { return fmt.Sprintf("$%d", n) }
func (postgresDialect) Quote(ident string) string { return quoteIdent(ident) }

// SQLite and Postgres are the built-in dialects.
var (
	SQLite   Dialect = sqliteDialect{}
	Postgres Dialect = postgresDialect{}
)

func quoteIdent(ident string) string {
	parts := strings.Split(ident, ".")
	for i, p := range parts {
		parts[i] = `"` + strings.ReplaceAll(p, `"`, `""`) + `"`
	}

	return strings.Join(parts, ".")
}

// Executor runs SQL; *DB and *Tx implement it, so ORM and schema code works
// inside and outside transactions.
type Executor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	Dialect() Dialect
}

// Driver connects a dialect to a database/sql driver. PostgreSQL's comes
// from the drivers/postgres module: database.Open(ctx, url, postgres.Driver()).
type Driver struct {
	// SQLName is the database/sql driver name ("pgx").
	SQLName string
	// Dialect is the SQL dialect.
	Dialect Dialect
	// Schemes are the URL schemes the driver accepts ("postgres").
	Schemes []string
}

// Config tunes connection pools.
type Config struct {
	MaxOpen     int
	MaxIdle     int
	MaxLifetime time.Duration
	MaxIdleTime time.Duration
	Log         *slog.Logger
}

// DB is a connection with its dialect. For SQLite, writes and transactions
// go to a single-connection writer pool and reads to a reader pool, which
// avoids SQLITE_BUSY under concurrency.
type DB struct {
	dialect Dialect
	writer  *sql.DB
	reader  *sql.DB
	log     *slog.Logger
}

// Open connects to url:
//   - SQLite: "file:storage/app.db", "sqlite:storage/app.db", ":memory:";
//   - other schemes need their Driver (e.g. postgres.Driver()).
func Open(ctx context.Context, rawURL string, opts ...Option) (*DB, error) {
	o := openOptions{cfg: Config{MaxOpen: 10, MaxIdle: 5, MaxLifetime: 30 * time.Minute, MaxIdleTime: 5 * time.Minute}}
	for _, opt := range opts {
		opt(&o)
	}

	if o.cfg.Log == nil {
		o.cfg.Log = slog.Default()
	}

	if isSQLite(rawURL) {
		return openSQLite(ctx, rawURL, o.cfg)
	}

	scheme, _, _ := strings.Cut(rawURL, "://")

	for _, d := range o.drivers {
		if slices.Contains(d.Schemes, scheme) {
			return openGeneric(ctx, d, rawURL, o.cfg)
		}
	}

	return nil, fmt.Errorf("database: no driver for %q URLs (import the driver module and pass its Driver)", scheme)
}

type openOptions struct {
	cfg     Config
	drivers []Driver
}

// Option configures Open.
type Option func(*openOptions)

// WithDriver registers a non-SQLite driver for this Open call.
func WithDriver(d Driver) Option { return func(o *openOptions) { o.drivers = append(o.drivers, d) } }

// WithConfig sets pool limits and the logger.
func WithConfig(c Config) Option {
	return func(o *openOptions) {
		if c.MaxOpen > 0 {
			o.cfg.MaxOpen = c.MaxOpen
		}

		if c.MaxIdle > 0 {
			o.cfg.MaxIdle = c.MaxIdle
		}

		if c.MaxLifetime > 0 {
			o.cfg.MaxLifetime = c.MaxLifetime
		}

		if c.MaxIdleTime > 0 {
			o.cfg.MaxIdleTime = c.MaxIdleTime
		}

		if c.Log != nil {
			o.cfg.Log = c.Log
		}
	}
}

func isSQLite(u string) bool {
	return u == ":memory:" || strings.HasPrefix(u, "file:") || strings.HasPrefix(u, "sqlite:")
}

// sqlitePragmas are applied to every SQLite connection (docs/database.md).
var sqlitePragmas = []string{
	"journal_mode(WAL)",
	"busy_timeout(5000)",
	"foreign_keys(1)",
	"synchronous(NORMAL)",
	"temp_store(MEMORY)",
}

func sqliteDSN(rawURL string, write bool) (dsn string, memory bool) {
	path := strings.TrimPrefix(strings.TrimPrefix(rawURL, "sqlite:"), "file:")
	path, query, _ := strings.Cut(path, "?")

	memory = path == ":memory:" || strings.Contains(query, "mode=memory")

	vals, _ := url.ParseQuery(query)
	for _, p := range sqlitePragmas {
		if memory && strings.HasPrefix(p, "journal_mode") {
			continue // WAL does not apply to in-memory databases
		}

		vals.Add("_pragma", p)
	}

	if write {
		vals.Set("_txlock", "immediate")
	} else if !memory {
		vals.Set("mode", "ro")
	}

	return "file:" + path + "?" + vals.Encode(), memory
}

func openSQLite(ctx context.Context, rawURL string, cfg Config) (*DB, error) {
	wdsn, memory := sqliteDSN(rawURL, true)

	writer, err := sql.Open("sqlite", wdsn)
	if err != nil {
		return nil, fmt.Errorf("database: open sqlite: %w", err)
	}

	writer.SetMaxOpenConns(1)
	writer.SetMaxIdleConns(1)
	writer.SetConnMaxLifetime(0)

	if err := writer.PingContext(ctx); err != nil {
		_ = writer.Close()
		return nil, fmt.Errorf("database: connect sqlite: %w", err)
	}

	db := &DB{dialect: SQLite, writer: writer, reader: writer, log: cfg.Log}

	if memory {
		// An in-memory database lives in one connection; share it.
		return db, nil
	}

	rdsn, _ := sqliteDSN(rawURL, false)

	reader, err := sql.Open("sqlite", rdsn)
	if err != nil {
		_ = writer.Close()
		return nil, fmt.Errorf("database: open sqlite reader: %w", err)
	}

	reader.SetMaxOpenConns(max(cfg.MaxOpen, 1))
	reader.SetMaxIdleConns(max(cfg.MaxIdle, 1))
	reader.SetConnMaxIdleTime(cfg.MaxIdleTime)
	db.reader = reader

	return db, nil
}

func openGeneric(ctx context.Context, d Driver, rawURL string, cfg Config) (*DB, error) {
	conn, err := sql.Open(d.SQLName, rawURL)
	if err != nil {
		return nil, fmt.Errorf("database: open %s: %w", d.Dialect.Name(), err)
	}

	conn.SetMaxOpenConns(cfg.MaxOpen)
	conn.SetMaxIdleConns(cfg.MaxIdle)
	conn.SetConnMaxLifetime(cfg.MaxLifetime)
	conn.SetConnMaxIdleTime(cfg.MaxIdleTime)

	if err := conn.PingContext(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("database: connect %s: %w", d.Dialect.Name(), err)
	}

	return &DB{dialect: d.Dialect, writer: conn, reader: conn, log: cfg.Log}, nil
}

// FromSQL wraps an existing *sql.DB (tests, sqlc interop).
func FromSQL(conn *sql.DB, dialect Dialect) *DB {
	return &DB{dialect: dialect, writer: conn, reader: conn, log: slog.Default()}
}

// Dialect implements Executor.
func (db *DB) Dialect() Dialect { return db.dialect }

// SQL returns the writer *sql.DB for libraries such as sqlc.
func (db *DB) SQL() *sql.DB { return db.writer }

// ExecContext runs a statement on the writer pool.
func (db *DB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return db.writer.ExecContext(ctx, query, args...)
}

// QueryContext runs a query on the reader pool.
func (db *DB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return db.reader.QueryContext(ctx, query, args...)
}

// QueryRowContext runs a single-row query on the reader pool, except for
// statements with RETURNING, which write and go to the writer pool.
func (db *DB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	if strings.Contains(strings.ToUpper(query), "RETURNING") {
		return db.writer.QueryRowContext(ctx, query, args...)
	}

	return db.reader.QueryRowContext(ctx, query, args...)
}

// Ping checks connectivity (used by health checks).
func (db *DB) Ping(ctx context.Context) error {
	if err := db.writer.PingContext(ctx); err != nil {
		return err
	}

	return db.reader.PingContext(ctx)
}

// Close closes the pools. Always close databases before deleting their
// files (required on Windows).
func (db *DB) Close() error {
	var errs []error
	if db.reader != db.writer {
		errs = append(errs, db.reader.Close())
	}

	errs = append(errs, db.writer.Close())

	return errors.Join(errs...)
}

// Tx is a transaction; it implements Executor.
type Tx struct {
	tx      *sql.Tx
	dialect Dialect
}

// Dialect implements Executor.
func (tx *Tx) Dialect() Dialect { return tx.dialect }

// ExecContext implements Executor.
func (tx *Tx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return tx.tx.ExecContext(ctx, query, args...)
}

// QueryContext implements Executor.
func (tx *Tx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return tx.tx.QueryContext(ctx, query, args...)
}

// QueryRowContext implements Executor.
func (tx *Tx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return tx.tx.QueryRowContext(ctx, query, args...)
}

// SQL returns the underlying *sql.Tx.
func (tx *Tx) SQL() *sql.Tx { return tx.tx }

// Tx runs fn in a transaction on the writer pool: it commits when fn
// returns nil and rolls back on error or panic (the panic is re-raised).
// SQLite write transactions use BEGIN IMMEDIATE.
func (db *DB) Tx(ctx context.Context, fn func(tx *Tx) error) (err error) {
	sqlTx, err := db.writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("database: begin: %w", err)
	}

	tx := &Tx{tx: sqlTx, dialect: db.dialect}

	defer func() {
		if p := recover(); p != nil {
			_ = sqlTx.Rollback()
			panic(p)
		}
	}()

	if err := fn(tx); err != nil {
		if rbErr := sqlTx.Rollback(); rbErr != nil {
			return errors.Join(err, fmt.Errorf("database: rollback: %w", rbErr))
		}

		return err
	}

	if err := sqlTx.Commit(); err != nil {
		return fmt.Errorf("database: commit: %w", err)
	}

	return nil
}

// RunInTx runs fn inside ex's transaction when ex is already a *Tx, or in
// a new transaction when ex is a *DB.
func RunInTx(ctx context.Context, ex Executor, fn func(tx *Tx) error) error {
	switch e := ex.(type) {
	case *Tx:
		return fn(e)
	case *DB:
		return e.Tx(ctx, fn)
	default:
		return fmt.Errorf("database: cannot start a transaction on %T", ex)
	}
}
