package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/AlexGalhardo/vukem/internal/testutil"
)

func openTemp(t *testing.T) *DB {
	t.Helper()

	db, err := Open(t.Context(), "file:"+filepath.Join(testutil.TempDir(t), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() }) // Windows: close before TempDir removal

	return db
}

func TestSQLitePragmas(t *testing.T) {
	t.Parallel()

	db := openTemp(t)

	pragmas := map[string]string{"journal_mode": "wal", "foreign_keys": "1", "busy_timeout": "5000", "synchronous": "1"}
	for name, want := range pragmas {
		var got string
		if err := db.writer.QueryRowContext(t.Context(), "PRAGMA "+name).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if !strings.EqualFold(got, want) {
			t.Errorf("PRAGMA %s = %s, want %s", name, got, want)
		}
	}

	if db.Dialect().Name() != "sqlite" || db.SQL() == nil || db.Ping(t.Context()) != nil {
		t.Fatal("accessors")
	}
}

func TestReaderIsReadOnly(t *testing.T) {
	t.Parallel()

	db := openTemp(t)
	if _, err := db.ExecContext(t.Context(), "CREATE TABLE t (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}

	if _, err := db.reader.ExecContext(t.Context(), "INSERT INTO t (id) VALUES (1)"); err == nil {
		t.Fatal("reader pool accepted a write")
	}
}

func TestConcurrentWritesWithoutBusy(t *testing.T) {
	t.Parallel()

	db := openTemp(t)
	ctx := t.Context()

	if _, err := db.ExecContext(ctx, "CREATE TABLE counter (id INTEGER PRIMARY KEY, n INTEGER NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO counter (id, n) VALUES (1, 0)"); err != nil {
		t.Fatal(err)
	}

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)

	for range 50 {
		wg.Go(func() {
			err := db.Tx(ctx, func(tx *Tx) error {
				var n int
				if err := tx.QueryRowContext(ctx, "SELECT n FROM counter WHERE id = 1").Scan(&n); err != nil {
					return err
				}
				_, err := tx.ExecContext(ctx, "UPDATE counter SET n = ? WHERE id = 1", n+1)
				return err
			})

			var n int
			if err == nil {
				err = db.QueryRowContext(ctx, "SELECT n FROM counter WHERE id = 1").Scan(&n)
			}

			if err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		})
	}
	wg.Wait()

	if len(errs) > 0 {
		t.Fatalf("%d errors, first: %v", len(errs), errs[0])
	}

	var n int
	_ = db.QueryRowContext(ctx, "SELECT n FROM counter WHERE id = 1").Scan(&n)
	if n != 50 {
		t.Fatalf("counter = %d, want 50 (lost updates)", n)
	}
}

func TestTxCommitRollbackPanic(t *testing.T) {
	t.Parallel()

	db, err := Open(t.Context(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := t.Context()
	_, _ = db.ExecContext(ctx, "CREATE TABLE t (v TEXT)")

	count := func() int {
		var n int
		_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM t").Scan(&n)
		return n
	}

	_ = db.Tx(ctx, func(tx *Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO t VALUES ('a')")
		return err
	})

	boom := errors.New("boom")
	if err := db.Tx(ctx, func(tx *Tx) error {
		_, _ = tx.ExecContext(ctx, "INSERT INTO t VALUES ('b')")
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("Tx error = %v", err)
	}

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("panic swallowed")
			}
		}()
		_ = db.Tx(ctx, func(tx *Tx) error {
			_, _ = tx.ExecContext(ctx, "INSERT INTO t VALUES ('c')")
			panic("x")
		})
	}()

	if n := count(); n != 1 {
		t.Fatalf("rows = %d, want 1 (rollback on error and panic)", n)
	}

	// RunInTx reuses an open transaction.
	err = db.Tx(ctx, func(tx *Tx) error {
		return RunInTx(ctx, tx, func(inner *Tx) error {
			if inner != tx || inner.SQL() == nil || inner.Dialect() != SQLite {
				return errors.New("not reused")
			}
			_, err := inner.ExecContext(ctx, "INSERT INTO t VALUES ('d')")
			return err
		})
	})
	if err != nil || count() != 2 {
		t.Fatalf("RunInTx = %v rows=%d", err, count())
	}
	if err := RunInTx(ctx, db, func(*Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestOpenErrorsAndDialects(t *testing.T) {
	t.Parallel()

	if _, err := Open(t.Context(), "mysql://x"); err == nil || !strings.Contains(err.Error(), "no driver") {
		t.Fatalf("unknown scheme = %v", err)
	}

	if _, err := Open(
		t.Context(),
		"postgres://u:p@127.0.0.1:1/db",
		WithDriver(Driver{SQLName: "nope", Dialect: Postgres, Schemes: []string{"postgres"}}),
	); err == nil {
		t.Fatal("unregistered sql driver accepted")
	}

	if Postgres.Placeholder(3) != "$3" || SQLite.Placeholder(3) != "?" || Postgres.Quote(`a"b.c`) != `"a""b"."c"` {
		t.Fatal("dialect helpers")
	}

	if MySQL.Name() != "mysql" || MySQL.Placeholder(3) != "?" || MySQL.Quote("a`b.c") != "`a``b`.`c`" {
		t.Fatal("mysql dialect helpers")
	}

	failDSN := Driver{
		SQLName: "nope", Dialect: MySQL, Schemes: []string{"mysql"},
		DSN: func(string) (string, error) { return "", errors.New("bad url") },
	}
	if _, err := Open(t.Context(), "mysql://u:secret@h/db", WithDriver(failDSN)); err == nil ||
		!strings.Contains(err.Error(), "bad url") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("DSN error = %v", err)
	}

	if _, err := Open(t.Context(), "file:"+filepath.Join(t.TempDir(), "missing", "dir", "x.db")); err == nil {
		t.Fatal("unreachable path accepted")
	}

	db, err := Open(
		t.Context(),
		"sqlite:"+filepath.Join(testutil.TempDir(t), "c.db"),
		WithConfig(Config{MaxOpen: 3, MaxIdle: 2}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if db.reader.Stats().MaxOpenConnections != 3 || db.writer.Stats().MaxOpenConnections != 1 {
		t.Fatalf(
			"pool limits: reader=%d writer=%d",
			db.reader.Stats().MaxOpenConnections,
			db.writer.Stats().MaxOpenConnections,
		)
	}
}

func TestManager(t *testing.T) {
	t.Parallel()

	a := openTemp(t)
	b := openTemp(t)

	m := NewManager(a)
	if err := m.Add("analytics", b); err != nil {
		t.Fatal(err)
	}
	if err := m.Add("analytics", b); err == nil {
		t.Fatal("duplicate accepted")
	}
	if m.Default() != a || m.Conn("analytics") != b || m.Conn("x") != nil || len(m.Names()) != 2 {
		t.Fatal("lookup")
	}
	if err := m.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if err := m.Ping(context.Background()); err == nil {
		t.Fatal("ping after close")
	}
}

type fakeEncrypter struct{}

func (fakeEncrypter) Seal(p, _ []byte) string { return string(p) }

func (fakeEncrypter) Open(t string, _ []byte) ([]byte, error) { return []byte(t), nil }

func TestEncrypterReachesDBAndTx(t *testing.T) {
	t.Parallel()

	plain := openTemp(t)
	if plain.Encrypter() != nil {
		t.Fatalf("Encrypter() = %v without WithEncrypter, want nil", plain.Encrypter())
	}

	enc := fakeEncrypter{}

	db, err := Open(t.Context(), ":memory:", WithEncrypter(enc))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if db.Encrypter() != enc {
		t.Fatalf("DB.Encrypter() = %v, want the configured encrypter", db.Encrypter())
	}

	err = db.Tx(t.Context(), func(tx *Tx) error {
		if tx.Encrypter() != enc {
			t.Errorf("Tx.Encrypter() = %v, want the configured encrypter", tx.Encrypter())
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTxAfterCommit(t *testing.T) {
	t.Parallel()

	db, err := Open(t.Context(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := t.Context()

	var ran []string

	err = db.Tx(ctx, func(tx *Tx) error {
		tx.AfterCommit(func(context.Context) { ran = append(ran, "a") })

		return RunInTx(ctx, tx, func(inner *Tx) error {
			inner.AfterCommit(func(context.Context) { ran = append(ran, "b") })

			if len(ran) != 0 {
				t.Error("AfterCommit hook ran before the commit")
			}

			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}

	if want := []string{"a", "b"}; strings.Join(ran, ",") != strings.Join(want, ",") {
		t.Fatalf("hooks after commit = %v, want %v", ran, want)
	}

	ran = nil
	_ = db.Tx(ctx, func(tx *Tx) error {
		tx.AfterCommit(func(context.Context) { ran = append(ran, "rolled back") })
		return errors.New("boom")
	})

	func() {
		defer func() { _ = recover() }()
		_ = db.Tx(ctx, func(tx *Tx) error {
			tx.AfterCommit(func(context.Context) { ran = append(ran, "panicked") })
			panic("x")
		})
	}()

	if len(ran) != 0 {
		t.Fatalf("hooks after rollback = %v, want none", ran)
	}
}

func TestTxAfterCommitHookPanicKeepsTheRest(t *testing.T) {
	t.Parallel()

	db, err := Open(t.Context(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	var logs strings.Builder

	db.log = slog.New(slog.NewTextHandler(&logs, nil))

	var ran []string

	err = db.Tx(t.Context(), func(tx *Tx) error {
		tx.AfterCommit(func(context.Context) { ran = append(ran, "a") })
		tx.AfterCommit(func(context.Context) { panic("boom") })
		tx.AfterCommit(func(context.Context) { ran = append(ran, "c") })

		return nil
	})
	if err != nil {
		t.Fatalf("Tx() = %v, want nil (the commit happened)", err)
	}

	if strings.Join(ran, ",") != "a,c" {
		t.Errorf("hooks run = %v, want [a c]", ran)
	}

	if !strings.Contains(logs.String(), "boom") {
		t.Errorf("log = %q, want the hook panic", logs.String())
	}
}

func TestTxAfterCommitOutsideDBTxPanics(t *testing.T) {
	t.Parallel()

	defer func() {
		p := recover()
		if p == nil || !strings.Contains(fmt.Sprint(p), "AfterCommit") {
			t.Fatalf("recover() = %v, want a panic naming AfterCommit", p)
		}
	}()

	new(Tx).AfterCommit(func(context.Context) {})
}

func TestTxLockKey(t *testing.T) {
	t.Parallel()

	db := openTemp(t)

	err := db.Tx(t.Context(), func(tx *Tx) error { return tx.LockKey(t.Context(), "quota:user:42") })
	if err != nil {
		t.Errorf("LockKey on SQLite = %v, want nil", err)
	}

	for _, key := range []string{"", strings.Repeat("k", 256)} {
		err := db.Tx(t.Context(), func(tx *Tx) error { return tx.LockKey(t.Context(), key) })
		if err == nil {
			t.Errorf("LockKey(%d bytes) = nil, want an error", len(key))
		}
	}

	if err := (&Tx{dialect: MySQL}).LockKey(t.Context(), "k"); !errors.Is(err, ErrLockKeyUnsupported) {
		t.Errorf("LockKey on MySQL = %v, want ErrLockKeyUnsupported", err)
	}
}

type stateError struct{ state string }

func (e stateError) Error() string    { return "driver error: " + e.state }
func (e stateError) SQLState() string { return e.state }

func TestUniqueViolation(t *testing.T) {
	t.Parallel()

	db, err := Open(t.Context(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	for _, q := range []string{
		`CREATE TABLE u (id INTEGER PRIMARY KEY, email TEXT UNIQUE)`,
		`INSERT INTO u VALUES (1, 'duplicate key')`,
	} {
		if _, err := db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}

	_, uniqueErr := db.ExecContext(t.Context(), `INSERT INTO u VALUES (2, 'duplicate key')`)
	_, pkErr := db.ExecContext(t.Context(), `INSERT INTO u VALUES (1, 'x')`)
	_, otherErr := db.ExecContext(t.Context(), `INSERT INTO nope VALUES ('duplicate key')`)

	tests := []struct {
		name string
		hook func(error) bool
		err  error
		want bool
	}{
		{"sqlite unique", nil, uniqueErr, true},
		{"sqlite primary key", nil, pkErr, true},
		{"sqlite other error whose text says duplicate key", nil, otherErr, false},
		{"sqlstate 23505", nil, stateError{"23505"}, true},
		{"other sqlstate", nil, stateError{"23503"}, false},
		{"mysql ER_DUP_ENTRY text", nil, errors.New("Error 1062 (23000): Duplicate entry 'a' for key 'k'"), true},
		{"text inside a message does not count", nil, errors.New(`bad value "Error 1062 (" duplicate key`), false},
		{"driver hook decides", func(error) bool { return true }, errors.New("anything"), true},
		{"driver hook says no", func(error) bool { return false }, stateError{"23505"}, false},
		{"nil error", nil, nil, false},
	}

	for _, tt := range tests {
		if got := uniqueViolation(tt.hook, tt.err); got != tt.want {
			t.Errorf("%s: uniqueViolation = %v, want %v", tt.name, got, tt.want)
		}
	}

	if !db.UniqueViolation(uniqueErr) {
		t.Error("db.UniqueViolation(unique error) = false")
	}
}

func TestWriteRoutedQueries(t *testing.T) {
	t.Parallel()

	db, err := Open(t.Context(), "file:"+filepath.Join(testutil.TempDir(t), "w.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.ExecContext(t.Context(), `CREATE TABLE w (id INTEGER PRIMARY KEY, note TEXT)`); err != nil {
		t.Fatal(err)
	}

	var id int
	if err := db.QueryRowWriteContext(t.Context(), `INSERT INTO w (note) VALUES ('a') RETURNING id`).
		Scan(&id); err != nil ||
		id != 1 {
		t.Fatalf("QueryRowWriteContext = %d, %v; want id 1", id, err)
	}

	rows, err := db.QueryWriteContext(t.Context(), `INSERT INTO w (note) VALUES ('b'), ('c') RETURNING id`)
	if err != nil {
		t.Fatal(err)
	}

	defer rows.Close()

	n := 0
	for rows.Next() {
		n++
	}

	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	if n != 2 {
		t.Errorf("QueryWriteContext returned %d rows, want 2", n)
	}

	// A read that merely mentions the word still works.
	var s string
	if err := db.QueryRowContext(t.Context(), `SELECT 'returning'`).Scan(&s); err != nil || s != "returning" {
		t.Errorf("SELECT 'returning' = %q, %v", s, err)
	}

	for in, want := range map[string]bool{"x RETURNING id": true, "x returning": true, "return": false, "": false} {
		if got := containsFold(in, "RETURNING"); got != want {
			t.Errorf("containsFold(%q) = %v, want %v", in, got, want)
		}
	}
}

type oddDialect struct{ sqliteDialect }

func (oddDialect) Capabilities() Capabilities { return Capabilities{Family: "oracle"} }

func TestOpenRejectsUnknownFamily(t *testing.T) {
	t.Parallel()

	_, err := openGeneric(t.Context(), Driver{SQLName: "sqlite", Dialect: oddDialect{}}, "x", Config{})
	if err == nil || !strings.Contains(err.Error(), `Family "oracle"`) {
		t.Fatalf("openGeneric(odd family) = %v, want a Family error", err)
	}
}

// ownPoolDriver is a Driver that opens its own pool: MaxOpen 3, and a Close
// hook that counts calls and records whether the pool was still usable.
func ownPoolDriver(closes *int, poolOpenAtClose *bool, openErr error) Driver {
	var pool *sql.DB

	return Driver{
		SQLName: "unused", Dialect: SQLite, Schemes: []string{"own"},
		Open: func(_ context.Context, _ string) (*sql.DB, error) {
			if openErr != nil {
				return nil, openErr
			}

			var err error
			if pool, err = sql.Open("sqlite", ":memory:"); err != nil {
				return nil, err
			}

			pool.SetMaxOpenConns(3)

			return pool, nil
		},
		Close: func(ctx context.Context) error {
			*closes++
			*poolOpenAtClose = pool.PingContext(ctx) == nil

			return nil
		},
	}
}

func TestDriverOpenUsesTheDriversPoolAndLimits(t *testing.T) {
	t.Parallel()

	var (
		closes int
		usable bool
	)

	db, err := Open(t.Context(), "own://anything", WithDriver(ownPoolDriver(&closes, &usable, nil)),
		WithConfig(Config{MaxOpen: 10, MaxIdle: 5}))
	if err != nil {
		t.Fatal(err)
	}

	defer db.Close()

	if got := db.SQL().Stats().MaxOpenConnections; got != 3 {
		t.Errorf("MaxOpenConnections = %d, want the driver's 3, not the Config's 10", got)
	}

	if _, err := db.ExecContext(t.Context(), "CREATE TABLE t (id INTEGER)"); err != nil {
		t.Errorf("the pool the driver opened does not work: %v", err)
	}
}

func TestDriverCloseRunsOnceBeforeThePoolCloses(t *testing.T) {
	t.Parallel()

	var (
		closes int
		usable bool
	)

	db, err := Open(t.Context(), "own://anything", WithDriver(ownPoolDriver(&closes, &usable, nil)))
	if err != nil {
		t.Fatal(err)
	}

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	_ = db.Close()

	if closes != 1 {
		t.Errorf("Driver.Close ran %d times, want once", closes)
	}

	if !usable {
		t.Error("the pool was already closed when Driver.Close ran")
	}
}

func TestDriverOpenErrorDoesNotEchoTheURL(t *testing.T) {
	t.Parallel()

	var (
		closes int
		usable bool
	)

	_, err := Open(t.Context(), "own://user:s3cret@host/db",
		WithDriver(ownPoolDriver(&closes, &usable, errors.New("boom"))))
	if err == nil || !strings.Contains(err.Error(), "boom") || strings.Contains(err.Error(), "s3cret") {
		t.Fatalf("Open = %v, want the driver's error without the URL", err)
	}
}

func TestDriverCloseRunsWhenThePingAfterOpenFails(t *testing.T) {
	t.Parallel()

	var closes int

	d := Driver{
		SQLName: "unused", Dialect: SQLite, Schemes: []string{"own"},
		Open: func(context.Context, string) (*sql.DB, error) {
			// A read-only file in a directory that does not exist: the open
			// succeeds lazily and the ping fails.
			return sql.Open("sqlite", "file:"+filepath.Join(testutil.TempDir(t), "missing", "x.db")+"?mode=ro")
		},
		Close: func(context.Context) error {
			closes++
			return nil
		},
	}

	if _, err := Open(t.Context(), "own://x", WithDriver(d)); err == nil {
		t.Fatal("Open succeeded although the ping failed")
	}

	if closes != 1 {
		t.Errorf("Driver.Close ran %d times after a failed ping, want once (the driver may hold a sync handle)", closes)
	}
}
