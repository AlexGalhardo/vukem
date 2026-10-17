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

func TestQueryObserversFanOut(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		opts  func(a, b, c *queryLog) []Option
		after func(db *DB, a, b, c *queryLog)
		want  [3]int
	}{
		{
			name: "every WithQueryObserver is notified",
			opts: func(a, b, _ *queryLog) []Option { return []Option{WithQueryObserver(a), WithQueryObserver(b)} },
			want: [3]int{2, 2, 0},
		},
		{
			name:  "AddQueryObserver keeps the existing ones",
			opts:  func(a, _, _ *queryLog) []Option { return []Option{WithQueryObserver(a)} },
			after: func(db *DB, _, b, c *queryLog) { db.AddQueryObserver(b); db.AddQueryObserver(c) },
			want:  [3]int{2, 2, 2},
		},
		{
			name:  "SetQueryObserver replaces them all",
			opts:  func(a, b, _ *queryLog) []Option { return []Option{WithQueryObserver(a), WithQueryObserver(b)} },
			after: func(db *DB, _, _, c *queryLog) { db.SetQueryObserver(c) },
			want:  [3]int{0, 0, 2},
		},
		{
			name:  "nil observers are ignored",
			opts:  func(a, _, _ *queryLog) []Option { return []Option{WithQueryObserver(nil), WithQueryObserver(a)} },
			after: func(db *DB, _, _, _ *queryLog) { db.AddQueryObserver(nil) },
			want:  [3]int{2, 0, 0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a, b, c := &queryLog{}, &queryLog{}, &queryLog{}

			db, err := Open(t.Context(), ":memory:", tt.opts(a, b, c)...)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })

			if tt.after != nil {
				tt.after(db, a, b, c)
			}

			if _, err := db.ExecContext(t.Context(), "SELECT 1"); err != nil {
				t.Fatal(err)
			}

			if err := db.Tx(t.Context(), func(tx *Tx) error {
				_, err := tx.ExecContext(t.Context(), "SELECT 2")
				return err
			}); err != nil {
				t.Fatal(err)
			}

			got := [3]int{len(a.got), len(b.got), len(c.got)}
			if got != tt.want {
				t.Fatalf("observed counts = %v, want %v", got, tt.want)
			}
		})
	}
}
