package database

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/internal/testutil"
)

func txDB(t *testing.T, opts ...Option) *DB {
	t.Helper()

	db, err := Open(t.Context(), "file:"+filepath.Join(testutil.TempDir(t), "tx.db"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.ExecContext(t.Context(), `CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatal(err)
	}

	return db
}

func itemNames(t *testing.T, ex Executor) []string {
	t.Helper()

	rows, err := ex.QueryContext(t.Context(), `SELECT name FROM items ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	out := []string{}

	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}

		out = append(out, n)
	}

	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	return out
}

func insertItem(t *testing.T, ex Executor, name string) error {
	t.Helper()

	_, err := ex.ExecContext(t.Context(), `INSERT INTO items (name) VALUES (?)`, name)

	return err
}

func TestTxWithReadOnly(t *testing.T) {
	t.Parallel()

	db := txDB(t)

	if err := insertItem(t, db, "before"); err != nil {
		t.Fatal(err)
	}

	err := db.TxWith(t.Context(), TxOptions{ReadOnly: true}, func(tx *Tx) error {
		if got := itemNames(t, tx); !cmp.Equal(got, []string{"before"}) {
			t.Errorf("read-only transaction sees %v, want the earlier row", got)
		}

		if err := insertItem(t, tx, "nope"); err == nil {
			t.Error("a write inside a read-only transaction succeeded")
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if got := itemNames(t, db); !cmp.Equal(got, []string{"before"}) {
		t.Errorf("rows after the read-only transaction = %v", got)
	}
}

func TestTxWithDefaultsBehaveLikeTx(t *testing.T) {
	t.Parallel()

	db := txDB(t)
	ctx := t.Context()

	if err := db.TxWith(ctx, TxOptions{}, func(tx *Tx) error { return insertItem(t, tx, "committed") }); err != nil {
		t.Fatal(err)
	}

	boom := errors.New("boom")
	if err := db.TxWith(ctx, TxOptions{}, func(tx *Tx) error {
		_ = insertItem(t, tx, "rolled back")
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("TxWith error = %v", err)
	}

	func() {
		defer func() { _ = recover() }()

		_ = db.TxWith(ctx, TxOptions{}, func(tx *Tx) error {
			_ = insertItem(t, tx, "panicked")
			panic("boom")
		})
	}()

	if got := itemNames(t, db); !cmp.Equal(got, []string{"committed"}) {
		t.Errorf("rows = %v, want only the committed one", got)
	}

	ran := false
	if err := db.TxWith(ctx, TxOptions{}, func(tx *Tx) error {
		tx.AfterCommit(func(context.Context) { ran = true })
		return nil
	}); err != nil || !ran {
		t.Errorf("AfterCommit ran = %v, err %v", ran, err)
	}
}

func TestSavepoint(t *testing.T) {
	t.Parallel()

	db := txDB(t)
	ctx := t.Context()
	boom := errors.New("boom")

	err := db.Tx(ctx, func(tx *Tx) error {
		if err := insertItem(t, tx, "outer"); err != nil {
			return err
		}

		if err := tx.Savepoint(ctx, func(sp *Tx) error {
			_ = insertItem(t, sp, "inner failed")
			return boom
		}); !errors.Is(err, boom) {
			t.Errorf("Savepoint error = %v, want boom", err)
		}

		// Two in sequence, and one inside another.
		if err := tx.Savepoint(ctx, func(sp *Tx) error { return insertItem(t, sp, "kept 1") }); err != nil {
			return err
		}

		return tx.Savepoint(ctx, func(sp *Tx) error {
			if err := insertItem(t, sp, "kept 2"); err != nil {
				return err
			}

			_ = sp.Savepoint(ctx, func(in *Tx) error {
				_ = insertItem(t, in, "inner-inner failed")
				return boom
			})

			return insertItem(t, sp, "kept 3")
		})
	})
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"outer", "kept 1", "kept 2", "kept 3"}
	if got := itemNames(t, db); !cmp.Equal(got, want) {
		t.Errorf("rows = %v, want %v", got, want)
	}
}

func TestSavepointPanicLeavesTheTransactionUsable(t *testing.T) {
	t.Parallel()

	db := txDB(t)
	ctx := t.Context()

	err := db.Tx(ctx, func(tx *Tx) error {
		func() {
			defer func() {
				if recover() == nil {
					t.Error("the panic was swallowed")
				}
			}()

			_ = tx.Savepoint(ctx, func(sp *Tx) error {
				_ = insertItem(t, sp, "lost")
				panic("boom")
			})
		}()

		return insertItem(t, tx, "after")
	})
	if err != nil {
		t.Fatal(err)
	}

	if got := itemNames(t, db); !cmp.Equal(got, []string{"after"}) {
		t.Errorf("rows = %v, want only the one after the panic", got)
	}
}

func TestSavepointAfterCommitHooks(t *testing.T) {
	t.Parallel()

	db := txDB(t)
	ctx := t.Context()

	var ran []string

	err := db.Tx(ctx, func(tx *Tx) error {
		tx.AfterCommit(func(context.Context) { ran = append(ran, "outer") })

		_ = tx.Savepoint(ctx, func(sp *Tx) error {
			sp.AfterCommit(func(context.Context) { ran = append(ran, "rolled back") })
			return errors.New("fail")
		})

		return tx.Savepoint(ctx, func(sp *Tx) error {
			sp.AfterCommit(func(context.Context) { ran = append(ran, "released") })
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}

	if !cmp.Equal(ran, []string{"outer", "released"}) {
		t.Errorf("hooks ran = %v, want outer and released only", ran)
	}
}

type sqlLog struct {
	mu  sync.Mutex
	sql []string
}

func (l *sqlLog) ObserveQuery(_ context.Context, q Query) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.sql = append(l.sql, q.SQL)
}

func (l *sqlLog) hasSavepoint() bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	return slices.ContainsFunc(l.sql, func(s string) bool { return strings.HasPrefix(s, "SAVEPOINT") })
}

func TestNested(t *testing.T) {
	t.Parallel()

	log := &sqlLog{}
	db := txDB(t, WithQueryObserver(log))
	ctx := t.Context()

	// On a *DB it opens a transaction: no savepoint.
	if err := Nested(ctx, db, func(tx *Tx) error { return insertItem(t, tx, "a") }); err != nil {
		t.Fatal(err)
	}

	if log.hasSavepoint() {
		t.Error("Nested on a *DB issued SAVEPOINT")
	}

	// On a *Tx it is a savepoint that undoes only its own work.
	err := db.Tx(ctx, func(tx *Tx) error {
		_ = Nested(ctx, tx, func(in *Tx) error {
			_ = insertItem(t, in, "b")
			return errors.New("fail")
		})

		return nil
	})
	if err != nil || !log.hasSavepoint() {
		t.Fatalf("Nested on a *Tx: err %v, SAVEPOINT issued %v", err, log.hasSavepoint())
	}

	if got := itemNames(t, db); !cmp.Equal(got, []string{"a"}) {
		t.Errorf("rows = %v, want [a]", got)
	}

	// RunInTx joins the outer transaction: all or nothing, no savepoint.
	log2 := &sqlLog{}
	db2 := txDB(t, WithQueryObserver(log2))

	err = db2.Tx(ctx, func(tx *Tx) error {
		return RunInTx(ctx, tx, func(in *Tx) error { return insertItem(t, in, "c") })
	})
	if err != nil || log2.hasSavepoint() {
		t.Errorf("RunInTx on a *Tx: err %v, SAVEPOINT issued %v", err, log2.hasSavepoint())
	}
}

func TestSavepointOnTxNotStartedByDBPanics(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Error("Savepoint on a bare Tx did not panic")
		}
	}()

	_ = (&Tx{}).Savepoint(t.Context(), func(*Tx) error { return nil })
}

func TestSavepointFailedReleaseDropsHooksAndWork(t *testing.T) {
	t.Parallel()

	db := txDB(t)
	ctx := t.Context()

	var ran bool

	err := db.Tx(ctx, func(tx *Tx) error {
		err := tx.Savepoint(ctx, func(sp *Tx) error {
			sp.AfterCommit(func(context.Context) { ran = true })

			// Release it behind Savepoint's back: its own RELEASE then fails.
			_, err := sp.ExecContext(ctx, "RELEASE SAVEPOINT vk_sp_1")

			return err
		})
		if err == nil {
			t.Error("a savepoint whose RELEASE failed returned nil")
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if ran {
		t.Error("an AfterCommit hook of a savepoint that failed to release ran")
	}
}

func TestTxWithReadOnlyRefusesWritesInMemoryToo(t *testing.T) {
	t.Parallel()

	db, err := Open(t.Context(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.ExecContext(t.Context(), `CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatal(err)
	}

	err = db.TxWith(t.Context(), TxOptions{ReadOnly: true}, func(tx *Tx) error {
		if err := insertItem(t, tx, "nope"); err == nil {
			t.Error("a write inside a read-only in-memory transaction succeeded")
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// The pragma is undone: the next transaction can write.
	if err := db.Tx(t.Context(), func(tx *Tx) error { return insertItem(t, tx, "ok") }); err != nil {
		t.Errorf("write after a read-only transaction = %v", err)
	}
}
