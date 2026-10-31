// Package pgtest gives each test its own throwaway PostgreSQL database.
//
// [Database] creates an empty database named pgtest_<time>_<random> on the server
// behind an admin URL, returns a URL pointing at it and drops it when the
// test ends. Pass the URL to vukemtest as DATABASE_URL to run an app test
// against real PostgreSQL instead of the default in-memory SQLite:
//
//	func TestCreatePost(t *testing.T) {
//		app := vukemtest.New(t, bootstrap.New, vukemtest.Options{
//			Env: map[string]string{"DATABASE_URL": pgtest.Database(t, "")},
//		})
//		// ...
//	}
//
// The server must be running: Database fails the test with a clear message
// when it cannot connect, so guard Postgres-only tests with testing.Short
// or a build tag if they must be optional.
package pgtest

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	// defaultDropTimeout bounds the cleanup; t.Context is already canceled by then.
	defaultDropTimeout = 30 * time.Second
	closeTimeout       = 5 * time.Second

	// dropTimeoutEnv overrides defaultDropTimeout with a Go duration ("2m").
	dropTimeoutEnv = "PGTEST_DROP_TIMEOUT"

	// defaultSweepAfter is the age past which an idle pgtest database counts
	// as an orphan of a killed run.
	defaultSweepAfter = 6 * time.Hour

	// sweepAfterEnv overrides defaultSweepAfter with a Go duration; 0 disables the sweep.
	sweepAfterEnv = "PGTEST_SWEEP_AFTER"
)

// nameRe matches the names newName generates and nothing else, so the sweep
// never drops a database it did not create. Group 1 is the creation time in
// Unix seconds (PostgreSQL keeps no creation time of its own).
var nameRe = regexp.MustCompile(`^pgtest_(\d{10})_[a-z2-7]{12}$`)

// sweeps runs the sweep once per process for each admin URL.
var sweeps sync.Map // adminURL -> true

// dropTimeoutFrom reads PGTEST_DROP_TIMEOUT. DROP DATABASE forces a
// checkpoint, which can pass 30 s on a slow disk under many parallel tests.
func dropTimeoutFrom(getenv func(string) string) (time.Duration, error) {
	v := getenv(dropTimeoutEnv)
	if v == "" {
		return defaultDropTimeout, nil
	}

	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s=%q: want a positive duration such as 2m", dropTimeoutEnv, v)
	}

	return d, nil
}

// sweepAfterFrom reads PGTEST_SWEEP_AFTER; 0 disables the sweep.
func sweepAfterFrom(getenv func(string) string) (time.Duration, error) {
	v := getenv(sweepAfterEnv)
	if v == "" {
		return defaultSweepAfter, nil
	}

	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("%s=%q: want a Go duration such as 6h, or 0 to disable", sweepAfterEnv, v)
	}

	return d, nil
}

// orphan reports whether name is a generated pgtest name created more than
// after before now.
func orphan(name string, now time.Time, after time.Duration) bool {
	m := nameRe.FindStringSubmatch(name)
	if m == nil {
		return false
	}

	sec, err := strconv.ParseInt(m[1], 10, 64)

	return err == nil && now.Sub(time.Unix(sec, 0)) > after
}

// sweep drops generated pgtest databases older than after that have no
// connections: leftovers of runs killed before their cleanup ran. It returns
// the dropped names. A database that gains a connection meanwhile makes the
// plain DROP fail and is skipped.
func sweep(ctx context.Context, adminURL string, now time.Time, after time.Duration) ([]string, error) {
	var dropped []string

	err := withConn(ctx, adminURL, func(ctx context.Context, conn *pgx.Conn) error {
		rows, err := conn.Query(ctx, `SELECT d.datname FROM pg_database d WHERE d.datname LIKE 'pgtest\_%'
			AND NOT EXISTS (SELECT 1 FROM pg_stat_activity a WHERE a.datname = d.datname)`)
		if err != nil {
			return err
		}

		names, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}

		for _, name := range names {
			if !orphan(name, now, after) {
				continue
			}

			if _, err := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()); err == nil {
				dropped = append(dropped, name)
			}
		}

		return nil
	})

	return dropped, err
}

// sweepOnce runs sweep the first time it sees adminURL in this process; later
// calls return at once.
// Failures are logged, never fatal: the sweep is housekeeping.
func sweepOnce(tb testing.TB, adminURL string, after time.Duration) {
	tb.Helper()

	if after == 0 {
		return
	}

	if _, seen := sweeps.LoadOrStore(adminURL, true); seen {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultDropTimeout)
	defer cancel()

	dropped, err := sweep(ctx, adminURL, time.Now(), after)
	if err != nil {
		tb.Logf("pgtest: sweep orphan databases: %v", err)
	}

	if len(dropped) > 0 {
		tb.Logf("pgtest: dropped %d orphan database(s): %s", len(dropped), strings.Join(dropped, ", "))
	}
}

// Database creates an empty database on the server at adminURL, returns
// its URL and drops it in tb.Cleanup after terminating its connections.
// adminURL is a postgres:// or postgresql:// URL (keyword/value DSNs such
// as "host=... dbname=..." are rejected) whose role may CREATE DATABASE;
// when empty it is read from TEST_DATABASE_URL, then POSTGRES_URL. The
// server must be PostgreSQL 13 or later. Any failure fails the test.
//
// Register Database before anything that opens connections to the new
// database (such as vukemtest.New) so those close first: cleanups run in
// reverse order.
func Database(tb testing.TB, adminURL string) string {
	tb.Helper()

	if adminURL == "" {
		adminURL = adminURLFrom(os.Getenv)
	}

	if adminURL == "" {
		tb.Fatal("pgtest: no PostgreSQL server: set TEST_DATABASE_URL or POSTGRES_URL " +
			"(e.g. postgres://user:pass@localhost:5432/postgres?sslmode=disable)")
	}

	dropTimeout, err := dropTimeoutFrom(os.Getenv)
	if err != nil {
		tb.Fatalf("pgtest: %v", err)
	}

	sweepAfter, err := sweepAfterFrom(os.Getenv)
	if err != nil {
		tb.Fatalf("pgtest: %v", err)
	}

	sweepOnce(tb, adminURL, sweepAfter)

	name := newName(time.Now())

	dbURL, err := withDatabase(adminURL, name)
	if err != nil {
		tb.Fatalf("pgtest: %v", err)
	}

	// Registered before CREATE so a database whose CREATE failed late (for
	// example a canceled context) is still removed; IF EXISTS covers the
	// case where nothing was created.
	tb.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), dropTimeout)
		defer cancel()

		if err := drop(ctx, adminURL, name); err != nil {
			tb.Errorf("pgtest: drop database %s: %v", name, err)
		}
	})

	err = withConn(tb.Context(), adminURL, func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize())
		return err
	})
	if err != nil {
		tb.Fatalf("pgtest: create database %s: %v", name, err)
	}

	return dbURL
}

// adminURLFrom is the first non-empty of TEST_DATABASE_URL and POSTGRES_URL.
func adminURLFrom(getenv func(string) string) string {
	for _, k := range []string{"TEST_DATABASE_URL", "POSTGRES_URL"} {
		if v := getenv(k); v != "" {
			return v
		}
	}

	return ""
}

// newName is pgtest_<unix seconds>_<12 random base32 characters>, lowercase
// so the name matches how PostgreSQL folds unquoted identifiers. The
// timestamp lets the sweep age a database; the name stays well under
// PostgreSQL's 63-byte limit.
func newName(now time.Time) string {
	return fmt.Sprintf("pgtest_%010d_%s", now.Unix(), strings.ToLower(rand.Text()[:12]))
}

// withDatabase returns adminURL with its database path replaced by name.
func withDatabase(adminURL, name string) (string, error) {
	u, err := url.Parse(adminURL)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return "", errors.New("admin URL must be a postgres:// or postgresql:// URL")
	}

	u.Path, u.RawPath = "/"+name, ""

	return u.String(), nil
}

// withConn runs fn on a fresh admin connection and closes it with its own
// timeout, so a canceled ctx still releases the connection.
func withConn(ctx context.Context, adminURL string, fn func(context.Context, *pgx.Conn) error) error {
	conn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), closeTimeout)
		defer cancel()

		_ = conn.Close(closeCtx)
	}()

	return fn(ctx, conn)
}

// drop terminates leftover connections (pools the test did not close), then
// drops WITH (FORCE) (PostgreSQL 13+), which also ends any connection a pool
// reopens in between.
func drop(ctx context.Context, adminURL, name string) error {
	return withConn(ctx, adminURL, func(ctx context.Context, conn *pgx.Conn) error {
		if _, err := conn.Exec(ctx,
			"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()",
			name,
		); err != nil {
			return fmt.Errorf("terminate connections: %w", err)
		}

		_, err := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")

		return err
	})
}
