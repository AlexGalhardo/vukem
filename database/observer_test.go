package database

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
)

type queryLog struct {
	mu  sync.Mutex
	got []string
	err []bool
}

func (l *queryLog) ObserveQuery(_ context.Context, q Query) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.got = append(l.got, q.SQL)
	l.err = append(l.err, q.Err != nil)
}

func TestQueryObserverSeesDBAndTxStatements(t *testing.T) {
	t.Parallel()

	obs := &queryLog{}

	db, err := Open(t.Context(), ":memory:", WithQueryObserver(obs))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := t.Context()

	if _, err := db.ExecContext(ctx, "CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)"); err != nil {
		t.Fatal(err)
	}

	err = db.Tx(ctx, func(tx *Tx) error {
		if _, err := tx.ExecContext(ctx, "INSERT INTO t (v) VALUES (?)", "a"); err != nil {
			return err
		}

		var n int

		return tx.QueryRowContext(ctx, "SELECT count(*) FROM t").Scan(&n)
	})
	if err != nil {
		t.Fatal(err)
	}

	rows, err := db.QueryContext(ctx, "SELECT v FROM t")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	for rows.Next() {
	}

	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	var v string
	if err := db.QueryRowContext(ctx, "SELECT v FROM t WHERE id = ?", 99).Scan(&v); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("Scan() = %v, want ErrNoRows", err)
	}

	_, _ = db.ExecContext(ctx, "SELECT * FROM missing")

	want := []string{
		"CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)",
		"INSERT INTO t (v) VALUES (?)",
		"SELECT count(*) FROM t",
		"SELECT v FROM t",
		"SELECT v FROM t WHERE id = ?",
		"SELECT * FROM missing",
	}
	if diff := cmp.Diff(want, obs.got); diff != "" {
		t.Errorf("observed (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff([]bool{false, false, false, false, false, true}, obs.err); diff != "" {
		t.Errorf("errors (-want +got):\n%s", diff)
	}
}
