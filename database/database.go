// Package database manages SQL connections for Vukem: dialects, SQLite
// defaults (WAL, busy timeout, foreign keys, a single-connection writer pool
// beside a reader pool), PostgreSQL and MySQL through driver modules, and
// transactions that roll back on error or panic.
//
// [Open] turns a DATABASE_URL into a [*DB]; [DB.Tx] and [RunInTx] run a
// function in a [*Tx]. Both satisfy [Executor], the interface that the orm
// and schema packages accept, so the same code runs inside or outside a
// transaction. A [Manager] holds named connections. Apps get the default
// connection, opened from configuration, with app.DB().
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/database.md.
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
	"sync"
	"time"
)

// Dialect describes SQL differences between databases.
type Dialect interface {
	// Name is "sqlite", "postgres" or "mysql".
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

// mysqlDialect covers MySQL 8 and MariaDB 11: ? placeholders and
// backtick-quoted identifiers.
type mysqlDialect struct{}

func (mysqlDialect) Name() string              { return "mysql" }
func (mysqlDialect) Placeholder(int) string    { return "?" }
func (mysqlDialect) Quote(ident string) string { return quoteWith(ident, "`") }

// SQLite, Postgres and MySQL are the built-in dialects; Postgres and MySQL
// connect through their driver modules.
var (
	SQLite   Dialect = sqliteDialect{}
	Postgres Dialect = postgresDialect{}
	MySQL    Dialect = mysqlDialect{}
)

func quoteIdent(ident string) string { return quoteWith(ident, `"`) }

// quoteWith quotes each dot-separated part, doubling embedded quote chars.
func quoteWith(ident, q string) string {
	parts := strings.Split(ident, ".")
	for i, p := range parts {
		parts[i] = q + strings.ReplaceAll(p, q, q+q) + q
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
	// DSN, when set, turns the URL into the database/sql driver's data
	// source name (MySQL's driver does not take URLs). Its errors must not
	// echo the URL, which may hold a password.
	DSN func(rawURL string) (string, error)
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
	enc     Encrypter
	obs     QueryObserver
}

// Encrypter seals and opens column values for orm.Encrypted;
// *crypt.Encrypter implements it. ad (additional data) binds a value to
// its table and column.
type Encrypter interface {
	Seal(plaintext, ad []byte) string
	Open(token string, ad []byte) ([]byte, error)
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

	db, err := openURL(ctx, rawURL, o)
	if err != nil {
		return nil, err
	}

	db.enc = o.enc
	db.obs = o.obs

	return db, nil
}

func openURL(ctx context.Context, rawURL string, o openOptions) (*DB, error) {
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
	enc     Encrypter
	obs     QueryObserver
}

// WithEncrypter makes orm.Encrypted columns readable and writable through
// this connection (apps pass app.Crypt). Without it, writing or reading an
// Encrypted column fails.
func WithEncrypter(e Encrypter) Option { return func(o *openOptions) { o.enc = e } }

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
	if !sqliteLinked {
		return nil, errors.New("database: SQLite is not linked into this binary (built with -tags vukem_nosqlite); " +
			"rebuild without the tag or set DATABASE_URL to another driver")
	}

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
	dsn := rawURL

	if d.DSN != nil {
		var err error
		if dsn, err = d.DSN(rawURL); err != nil {
			return nil, fmt.Errorf("database: %s url: %w", d.Dialect.Name(), err)
		}
	}

	conn, err := sql.Open(d.SQLName, dsn)
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

// Encrypter returns the encrypter set with WithEncrypter, or nil.
func (db *DB) Encrypter() Encrypter { return db.enc }

// SQL returns the writer *sql.DB for libraries such as sqlc.
func (db *DB) SQL() *sql.DB { return db.writer }

// ExecContext runs a statement on the writer pool.
func (db *DB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if db.obs == nil {
		return db.writer.ExecContext(ctx, query, args...)
	}

	start := time.Now()
	res, err := db.writer.ExecContext(ctx, query, args...)
	db.obs.ObserveQuery(ctx, Query{SQL: query, Args: args, Duration: time.Since(start), Err: err})

	return res, err
}

// QueryContext runs a query on the reader pool.
func (db *DB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return observeRows(ctx, db.obs, db.reader.QueryContext, query, args)
}

// QueryRowContext runs a single-row query on the reader pool, except for
// statements with RETURNING, which write and go to the writer pool.
func (db *DB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	pool := db.reader
	if strings.Contains(strings.ToUpper(query), "RETURNING") {
		pool = db.writer
	}

	return observeRow(ctx, db.obs, pool.QueryRowContext, query, args)
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
	enc     Encrypter
	obs     QueryObserver

	// A pointer keeps Tx comparable, as it was before AfterCommit.
	hooks *txHooks
	log   *slog.Logger
}

type txHooks struct {
	mu  sync.Mutex
	fns []func(ctx context.Context)
}

// AfterCommit runs fn once the transaction commits, after DB.Tx's fn
// returned (hooks run in registration order, with DB.Tx's context). A
// rollback or panic discards the hooks; a hook registered after DB.Tx's fn
// returned never runs. A panicking hook is logged and the remaining hooks
// still run (the commit already happened, so DB.Tx returns nil). It panics
// on a Tx not started by DB.Tx (a programmer error). Queues use it to enqueue jobs that
// must not exist for rolled-back work (queue.DispatchTx on drivers without
// transactional storage).
func (tx *Tx) AfterCommit(fn func(ctx context.Context)) {
	if tx.hooks == nil {
		panic("database: AfterCommit on a Tx not started by DB.Tx")
	}

	tx.hooks.mu.Lock()
	defer tx.hooks.mu.Unlock()

	tx.hooks.fns = append(tx.hooks.fns, fn)
}

// Encrypter returns the encrypter of the DB that started the transaction.
func (tx *Tx) Encrypter() Encrypter { return tx.enc }

// Dialect implements Executor.
func (tx *Tx) Dialect() Dialect { return tx.dialect }

// ExecContext implements Executor.
func (tx *Tx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if tx.obs == nil {
		return tx.tx.ExecContext(ctx, query, args...)
	}

	start := time.Now()
	res, err := tx.tx.ExecContext(ctx, query, args...)
	tx.obs.ObserveQuery(ctx, Query{SQL: query, Args: args, Duration: time.Since(start), Err: err})

	return res, err
}

// QueryContext implements Executor.
func (tx *Tx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return observeRows(ctx, tx.obs, tx.tx.QueryContext, query, args)
}

// QueryRowContext implements Executor.
func (tx *Tx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return observeRow(ctx, tx.obs, tx.tx.QueryRowContext, query, args)
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

	tx := &Tx{tx: sqlTx, dialect: db.dialect, enc: db.enc, obs: db.obs, hooks: &txHooks{}, log: db.log}

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

	tx.hooks.mu.Lock()
	hooks := tx.hooks.fns
	tx.hooks.fns = nil
	tx.hooks.mu.Unlock()

	for _, fn := range hooks {
		tx.runHook(ctx, fn)
	}

	return nil
}

func (tx *Tx) runHook(ctx context.Context, fn func(context.Context)) {
	defer func() {
		if p := recover(); p != nil {
			log := tx.log
			if log == nil {
				log = slog.Default()
			}

			log.ErrorContext(ctx, "database: AfterCommit hook panicked", "panic", fmt.Sprint(p))
		}
	}()

	fn(ctx)
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
