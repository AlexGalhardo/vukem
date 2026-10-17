package database

import (
	"context"
	"errors"
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
