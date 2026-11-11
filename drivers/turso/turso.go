// Package turso connects Vukem to Turso: a local database file that can sync
// with Turso Cloud. EXPERIMENTAL: it depends on a pre-1.0 SDK
// (turso.tech/database/tursogo, pinned to an exact version) and its API may
// change in any release.
//
// The SDK carries its native library inside the module and loads it without
// cgo, for linux and darwin (amd64, arm64) and windows/amd64; other
// platforms are not supported.
//
//	opts, err := turso.OptionsFromEnv()
//	if err != nil {
//		return err
//	}
//
//	db := turso.New(opts)
//
//	app, err := vukem.New(
//		vukem.WithDatabaseDrivers(db.Driver()), // DATABASE_URL=turso://storage/database.db
//		vukem.WithRunner("turso-sync", db),
//	)
package turso

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	tursogo "turso.tech/database/tursogo"

	"github.com/AlexGalhardo/vukem/database"
)

// Environment variables read by [OptionsFromEnv].
const (
	EnvRemoteURL    = "TURSO_DATABASE_URL"
	EnvAuthToken    = "TURSO_AUTH_TOKEN" //nolint:gosec // G101: the name of the variable, not a secret
	EnvSyncInterval = "TURSO_SYNC_INTERVAL"
)

// DefaultSyncInterval is the pause between background syncs.
const DefaultSyncInterval = 5 * time.Second

var (
	// ErrInvalidURL is returned for a DATABASE_URL that is not turso://<path>.
	// It never quotes the URL.
	ErrInvalidURL = errors.New("turso: invalid database url")

	errNoToken   = errors.New("turso: " + EnvAuthToken + " is required when " + EnvRemoteURL + " is set")
	errBadRemote = errors.New("turso: " + EnvRemoteURL + " must start with libsql:// or https://")
	errPlaintext = errors.New("turso: refusing plaintext connection to a remote host")
)

// Options configures the driver.
type Options struct {
	// RemoteURL is the Turso Cloud database ("libsql://name.turso.io").
	// Empty means a plain local file with no sync.
	RemoteURL string
	// AuthToken authenticates against RemoteURL. It never appears in an
	// error or a log line.
	AuthToken string
	// SyncInterval is the pause between background syncs. Zero means 5s;
	// a negative value turns background sync off.
	SyncInterval time.Duration
	// Logger receives warnings (a failed background sync). Nil means
	// slog.Default().
	Logger *slog.Logger
}

// OptionsFromEnv reads TURSO_DATABASE_URL, TURSO_AUTH_TOKEN and
// TURSO_SYNC_INTERVAL (a Go duration), and validates them.
func OptionsFromEnv() (Options, error) {
	o := Options{RemoteURL: os.Getenv(EnvRemoteURL), AuthToken: os.Getenv(EnvAuthToken)}

	if v := os.Getenv(EnvSyncInterval); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Options{}, fmt.Errorf("turso: %s is not a duration like 5s", EnvSyncInterval)
		}

		o.SyncInterval = d
	}

	return o, o.validate()
}

// validate checks the remote settings: a token with the URL, and no
// plaintext connection to a host that is not this machine.
func (o Options) validate() error {
	if o.RemoteURL == "" {
		return nil
	}

	if o.AuthToken == "" {
		return errNoToken
	}

	u, err := url.Parse(o.RemoteURL)
	if err != nil || u.Host == "" {
		return errBadRemote
	}

	switch u.Scheme {
	case "libsql", "https":
		return nil
	case "http":
		if isLoopback(u.Hostname()) {
			return nil
		}

		return errPlaintext
	default:
		return errBadRemote
	}
}

// isLoopback reports whether host (as url.Hostname gives it, without
// userinfo or port) is this machine: localhost in any case, or an address in
// 127.0.0.0/8 or ::1.
func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}

	ip := net.ParseIP(host)

	return ip != nil && ip.IsLoopback()
}

// syncer is the part of the SDK's sync handle the driver uses; the real
// handle and the test double both implement it.
type syncer interface {
	Push(ctx context.Context) error
	Pull(ctx context.Context) (changed bool, err error)
	// LastPull is the unix time of the last successful pull, 0 when the
	// local file never synced.
	LastPull(ctx context.Context) (int64, error)
}

// syncHandle is a syncer that can also open the connection pool.
type syncHandle interface {
	syncer
	Connect(ctx context.Context) (*sql.DB, error)
}

// sdkHandle adapts the SDK's sync database to syncHandle.
type sdkHandle struct{ *tursogo.TursoSyncDb }

func (h sdkHandle) LastPull(ctx context.Context) (int64, error) {
	st, err := h.Stats(ctx)

	return st.LastPullUnixTime, err
}

// Database is one Turso connection setup: the database.Driver to register
// and the sync controls.
type Database struct {
	opts Options

	mu sync.Mutex
	// sy is the sync handle shared by Driver().Open (which creates it from
	// the SDK's sync constructor when opts.RemoteURL is set), Sync, Run and
	// Driver().Close (the final push). nil without RemoteURL, before the
	// database is open and after it closed; every read and write goes through
	// mu.
	sy syncer

	// syncMu serializes Push and Pull, and makes close wait for a Sync in
	// flight: the SDK handle is not documented as safe for concurrent use.
	syncMu sync.Mutex

	// newSync creates the SDK handle; tests replace it.
	newSync func(ctx context.Context, path string) (syncHandle, error)
}

// New returns a Database for opts.
func New(opts Options) *Database {
	d := &Database{opts: opts}
	d.newSync = d.newSDKSync

	return d
}

// newSDKSync creates the SDK's sync database for the local file at path.
func (d *Database) newSDKSync(ctx context.Context, path string) (syncHandle, error) {
	bootstrap := true

	sy, err := tursogo.NewTursoSyncDb(ctx, tursogo.TursoSyncDbConfig{
		Path: path, RemoteUrl: d.opts.RemoteURL, AuthToken: d.opts.AuthToken, BootstrapIfEmpty: &bootstrap,
	})
	if err != nil {
		return nil, err
	}

	return sdkHandle{sy}, nil
}

func (d *Database) logger() *slog.Logger {
	if d.opts.Logger != nil {
		return d.opts.Logger
	}

	return slog.Default()
}

func (d *Database) syncer() syncer {
	d.mu.Lock()
	defer d.mu.Unlock()

	return d.sy
}

func (d *Database) setSyncer(sy syncer) {
	d.mu.Lock()
	d.sy = sy
	d.mu.Unlock()
}

// guard runs an SDK call and turns a panic into an error, with the panic
// value redacted: the SDK may put the URL or token in it.
func (d *Database) guard(op string, fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = d.redact(fmt.Errorf("turso: %s: panic: %v", op, r))
		}
	}()

	return fn()
}

func (d *Database) push(ctx context.Context, sy syncer) error {
	return d.guard("push", func() error { return sy.Push(ctx) })
}

func (d *Database) pull(ctx context.Context, sy syncer) error {
	return d.guard("pull", func() error {
		_, err := sy.Pull(ctx)
		return err
	})
}

// Driver returns the driver for database.Open and vukem.WithDatabaseDrivers.
// It accepts turso:// URLs.
//
// The pool has ONE connection. Inside a transaction use only the
// transaction's executor: a query on the database itself waits for the
// transaction's connection and blocks until its context ends. Close the
// database after the runners (Run) have stopped, so the final push does not
// race a background sync.
func (d *Database) Driver() database.Driver {
	return database.Driver{
		SQLName: "turso", Dialect: Dialect, Schemes: []string{"turso"}, Open: d.open, Close: d.close,
		IsUniqueViolation: isUniqueViolation,
	}
}

// isUniqueViolation: the SDK wraps every constraint failure in
// ErrTursoConstraint and keeps SQLite's text, which tells a unique or primary
// key violation from a NOT NULL or foreign key one.
func isUniqueViolation(err error) bool {
	if !errors.Is(err, tursogo.ErrTursoConstraint) {
		return false
	}

	msg := err.Error()

	return strings.Contains(msg, "UNIQUE constraint failed") || strings.Contains(msg, "PRIMARY KEY constraint failed")
}

// winDrive is the "/C:" that turso:///C:/data/app.db leaves before the path.
var winDrive = regexp.MustCompile(`^/[A-Za-z]:`) //nolint:gochecknoglobals // compiled once

// parseURL returns the file path of turso://<path>: relative as turso://a/b.db,
// absolute as turso:///var/a.db or turso:///C:/data/a.db. It never quotes the
// URL, which may name a private path.
func parseURL(raw string) (string, error) {
	rest, ok := strings.CutPrefix(raw, "turso://")
	if !ok || rest == "" || strings.ContainsAny(rest, "?#\x00") {
		return "", ErrInvalidURL
	}

	if winDrive.MatchString(rest) {
		rest = rest[1:]
	}

	return rest, nil
}

// pragmas are those of the built-in SQLite driver; temp_store only warns.
var pragmas = []string{ //nolint:gochecknoglobals // read-only
	"PRAGMA journal_mode = WAL",
	"PRAGMA busy_timeout = 5000",
	"PRAGMA foreign_keys = ON",
	"PRAGMA synchronous = NORMAL",
	"PRAGMA temp_store = MEMORY",
}

func (d *Database) open(ctx context.Context, rawURL string) (conn *sql.DB, err error) {
	// A failed open must not leave a sync handle behind: Sync and the final
	// push would talk to a database that never opened.
	defer func() {
		if err != nil {
			d.setSyncer(nil)
		}
	}()

	path, err := parseURL(rawURL)
	if err != nil {
		return nil, err
	}

	if err := d.opts.validate(); err != nil {
		return nil, err
	}

	conn, err = d.connect(ctx, path)
	if err != nil {
		return nil, err
	}

	// shortcut: one connection for reads and writes, no reader/writer split;
	// it avoids SQLITE_BUSY. Upgrade if read concurrency shows up in practice.
	conn.SetMaxOpenConns(1)
	conn.SetMaxIdleConns(1)
	conn.SetConnMaxLifetime(0)

	if err := d.applyPragmas(ctx, conn); err != nil {
		_ = conn.Close()
		return nil, err
	}

	return conn, nil
}

// connect opens the local file, through the sync engine when a remote is set.
func (d *Database) connect(ctx context.Context, path string) (*sql.DB, error) {
	if d.opts.RemoteURL == "" {
		conn, err := sql.Open("turso", path)
		if err != nil {
			return nil, d.redact(fmt.Errorf("turso: open: %w", err))
		}

		return conn, nil
	}

	var sy syncHandle

	err := d.guard("open synced database", func() (err error) {
		sy, err = d.newSync(ctx, path)
		return err
	})
	if err != nil {
		return nil, d.redact(fmt.Errorf("turso: open synced database: %w", err))
	}

	var conn *sql.DB

	err = d.guard("connect", func() (err error) {
		conn, err = sy.Connect(ctx)
		return err
	})
	if err != nil {
		return nil, d.redact(fmt.Errorf("turso: connect: %w", err))
	}

	d.setSyncer(sy)

	// A failed first pull is tolerated only when this file synced before:
	// then it holds real data and the app can work offline. A file that
	// never synced may be empty or stale, so the open fails closed.
	if err := d.pull(ctx, sy); err != nil {
		if last, lerr := sy.LastPull(ctx); lerr != nil || last <= 0 {
			_ = conn.Close()
			return nil, d.redact(fmt.Errorf("turso: first pull: %w", err))
		}

		d.logger().WarnContext(ctx, "turso: first pull failed, continuing with the local file", "error", err)
	}

	return conn, nil
}

func (d *Database) applyPragmas(ctx context.Context, conn *sql.DB) error {
	for _, p := range pragmas {
		err := runPragma(ctx, conn, p)
		if err == nil {
			continue
		}

		if strings.Contains(p, "temp_store") {
			d.logger().WarnContext(ctx, "turso: pragma refused", "pragma", p, "error", d.redact(err))
			continue
		}

		return d.redact(fmt.Errorf("turso: %s: %w", p, err))
	}

	return nil
}

// runPragma runs one PRAGMA; it may answer with a row, so it goes through
// Query and the rows are drained.
func runPragma(ctx context.Context, conn *sql.DB, pragma string) error {
	rows, err := conn.QueryContext(ctx, pragma)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
	}

	return rows.Err()
}

// Sync pushes local changes and pulls remote ones. It is a no-op without
// RemoteURL or before the database is open. Syncs run one at a time.
func (d *Database) Sync(ctx context.Context) error {
	d.syncMu.Lock()
	defer d.syncMu.Unlock()

	sy := d.syncer()
	if sy == nil {
		return nil
	}

	if err := d.push(ctx, sy); err != nil {
		return d.redact(fmt.Errorf("turso: push: %w", err))
	}

	if err := d.pull(ctx, sy); err != nil {
		return d.redact(fmt.Errorf("turso: pull: %w", err))
	}

	return nil
}

// Run syncs every SyncInterval until ctx ends (a vukem Runner). A failed sync
// is logged and tried again at the next interval; Run returns nil when ctx
// ends. Without RemoteURL, or with a negative SyncInterval, it only waits for
// ctx.
func (d *Database) Run(ctx context.Context) error {
	interval := d.opts.SyncInterval
	if interval == 0 {
		interval = DefaultSyncInterval
	}

	if d.opts.RemoteURL == "" || interval < 0 {
		<-ctx.Done()
		return nil
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := d.Sync(ctx); err != nil && ctx.Err() == nil {
				d.logger().WarnContext(ctx, "turso: background sync failed, will retry", "error", err)
			}
		}
	}
}

// close runs before the pool closes: the final push, so the changes of a
// short-lived command (migrate) reach Turso Cloud. It waits for a Sync in
// flight and ends further ones. The SDK handle has no Close of its own.
func (d *Database) close(ctx context.Context) error {
	d.syncMu.Lock()
	defer d.syncMu.Unlock()

	sy := d.syncer()
	d.setSyncer(nil)

	if sy == nil {
		return nil
	}

	if err := d.push(ctx, sy); err != nil {
		return d.redact(fmt.Errorf("turso: final push: %w", err))
	}

	return nil
}

// redactedError carries an SDK error with the remote URL, host and token
// removed from its text. It does not expose the original error (Unwrap is not
// defined: the original may hold the secrets in fields); only the sentinels
// that callers act on stay reachable through errors.Is.
type redactedError struct {
	msg string
	err error
}

func (e *redactedError) Error() string { return e.msg }

// Is keeps the SDK's constraint sentinel and the context errors.
func (e *redactedError) Is(target error) bool {
	switch target {
	case tursogo.ErrTursoConstraint, context.Canceled, context.DeadlineExceeded:
		return errors.Is(e.err, target)
	default:
		return false
	}
}

// redact hides the remote URL, its host and the token wherever the SDK
// echoed them.
func (d *Database) redact(err error) error {
	if err == nil {
		return nil
	}

	msg := err.Error()

	for _, secret := range d.secrets() {
		msg = strings.ReplaceAll(msg, secret, "[redacted]")
	}

	return &redactedError{msg: msg, err: err}
}

// secrets lists what must never reach a message, longest first so a URL is
// replaced before its host.
func (d *Database) secrets() []string {
	var out []string

	if u, err := url.Parse(d.opts.RemoteURL); err == nil && u.Host != "" {
		for _, scheme := range []string{"libsql", "https", "http", "wss", "ws"} {
			out = append(out, scheme+"://"+u.Host)
		}

		out = append(out, u.Host, u.Hostname())
	}

	out = append(out, d.opts.RemoteURL, d.opts.AuthToken)

	out = slices.DeleteFunc(out, func(s string) bool { return s == "" })
	slices.SortFunc(out, func(a, b string) int { return len(b) - len(a) })

	return out
}

// Dialect is Turso's SQL dialect: SQLite grammar under the name "turso", with
// its own capabilities.
var Dialect database.Dialect = dialect{database.SQLite} //nolint:gochecknoglobals // read-only value

// dialect embeds database.SQLite (a database.Dialect value, whose concrete
// type is unexported in package database) to inherit Placeholder and Quote,
// and overrides Name and Capabilities.
type dialect struct{ database.Dialect }

// Name is "turso". Schema.Dialect() returns it too, so migration code that
// tests for "sqlite" must test the family instead.
func (dialect) Name() string { return "turso" }

// Capabilities implements database.Capable. The values start from the
// built-in SQLite's; MaxParams is conservative on purpose (the engine's real
// limit is not verified).
func (dialect) Capabilities() database.Capabilities {
	c := database.MustCapabilities(database.SQLite)
	c.MaxParams = 999
	c.AdvisoryLock = ""

	return c
}
