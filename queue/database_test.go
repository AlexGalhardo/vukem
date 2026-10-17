package queue_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/internal/testutil"
	"github.com/AlexGalhardo/vukem/queue"
	"github.com/AlexGalhardo/vukem/queue/queuetest"
)

func openSQLite(t *testing.T) *database.DB {
	t.Helper()

	db, err := database.Open(t.Context(), "file:"+filepath.Join(testutil.TempDir(t), "q.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	return db
}

func newDatabaseDriver(t *testing.T, db *database.DB, o queue.DatabaseOptions) *queue.Database {
	t.Helper()

	d, err := queue.NewDatabase(db, o)
	if err != nil {
		t.Fatal(err)
	}

	if err := d.CreateTables(t.Context()); err != nil {
		t.Fatal(err)
	}

	return d
}

func TestDatabaseConformanceSQLite(t *testing.T) {
	t.Parallel()

	var offset atomic.Int64

	d := newDatabaseDriver(t, openSQLite(t), queue.DatabaseOptions{
		Grace: time.Second,
		Now:   func() time.Time { return time.Now().Add(time.Duration(offset.Load())) },
	})

	// CreateTables is idempotent (IF NOT EXISTS), so boot can always call it.
	if err := d.CreateTables(t.Context()); err != nil {
		t.Fatalf("CreateTables() twice = %v, want nil", err)
	}

	queuetest.Run(t, d, queuetest.Options{
		Pending: d.Pending,
		Advance: func(by time.Duration) { offset.Add(int64(by)) },
	})
}

func TestNewDatabaseTableNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		opts    queue.DatabaseOptions
		wantErr string
	}{
		{name: "defaults", opts: queue.DatabaseOptions{}},
		{name: "custom", opts: queue.DatabaseOptions{Table: "app_jobs", FailedTable: "app_failed"}},
		{
			name:    "injection in table",
			opts:    queue.DatabaseOptions{Table: "jobs; DROP TABLE users"},
			wantErr: "invalid table name",
		},
		{name: "quote in failed table", opts: queue.DatabaseOptions{FailedTable: `f"x`}, wantErr: "invalid table name"},
		{name: "leading digit", opts: queue.DatabaseOptions{Table: "1jobs"}, wantErr: "invalid table name"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			d, err := queue.NewDatabase(openSQLite(t), tt.opts)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) || d != nil {
					t.Fatalf("NewDatabase(%+v) = %v, %v, want error %q", tt.opts, d, err, tt.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("NewDatabase(%+v) = %v, want nil", tt.opts, err)
			}
		})
	}
}

func TestDatabaseCreatesNamedTables(t *testing.T) {
	t.Parallel()

	db := openSQLite(t)
	newDatabaseDriver(t, db, queue.DatabaseOptions{Table: "app_jobs", FailedTable: "app_failed"})

	rows, err := db.QueryContext(t.Context(),
		`SELECT name FROM sqlite_master WHERE type IN ('table', 'index') AND name LIKE 'app_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var got []string

	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}

		got = append(got, n)
	}

	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	want := []string{"app_failed", "app_jobs", "app_jobs_queue_available"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("tables (-want +got):\n%s", diff)
	}
}

func TestDatabaseRoundTripsMessageFields(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	d := newDatabaseDriver(t, openSQLite(t), queue.DatabaseOptions{Now: func() time.Time { return now }})
	ctx := t.Context()

	in := &queue.Message{
		ID: "m1", Kind: "send_invoice", Queue: "mail", Payload: []byte(`{"order_id":7}`),
		Attempts: 2, MaxRetries: 5, Timeout: 1500 * time.Millisecond,
		AvailableAt: now.Add(-time.Second), CreatedAt: now.Add(-time.Minute), LastError: "smtp down",
	}
	if err := d.Push(ctx, in); err != nil {
		t.Fatal(err)
	}

	if m, err := d.Pop(ctx, []string{"default"}); err != nil || m != nil {
		t.Fatalf("Pop(default) = %+v, %v, want nil, nil", m, err)
	}

	got, err := d.Pop(ctx, []string{"default", "mail"})
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(in, got); diff != "" {
		t.Errorf("Pop() message (-want +got):\n%s", diff)
	}

	if err := d.Fail(ctx, got); err != nil {
		t.Fatal(err)
	}

	failed, err := d.Failed(ctx)
	if err != nil {
		t.Fatal(err)
	}

	want := []queue.FailedJob{{Message: *in, FailedAt: now}}
	if diff := cmp.Diff(want, failed); diff != "" {
		t.Errorf("Failed() (-want +got):\n%s", diff)
	}

	if n, err := d.Pending(ctx); err != nil || n != 0 {
		t.Errorf("Pending() = %d, %v, want 0 after Fail", n, err)
	}
}

func TestDatabaseStaleWorkerCannotSettleAReReservedJob(t *testing.T) {
	t.Parallel()

	settles := map[string]func(*queue.Database) func(context.Context, *queue.Message) error{
		"Ack":     func(d *queue.Database) func(context.Context, *queue.Message) error { return d.Ack },
		"Release": func(d *queue.Database) func(context.Context, *queue.Message) error { return d.Release },
		"Fail":    func(d *queue.Database) func(context.Context, *queue.Message) error { return d.Fail },
	}

	for name, settle := range settles {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var offset atomic.Int64

			now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
			d := newDatabaseDriver(t, openSQLite(t), queue.DatabaseOptions{
				Now: func() time.Time { return now.Add(time.Duration(offset.Load())) },
			})
			ctx := t.Context()

			if err := d.Push(ctx, &queue.Message{
				ID: "j", Kind: "k", Queue: "default", Payload: []byte(`{}`), Timeout: time.Second,
				AvailableAt: now, CreatedAt: now,
			}); err != nil {
				t.Fatal(err)
			}

			stale, err := d.Pop(ctx, []string{"default"})
			if err != nil || stale == nil {
				t.Fatalf("first Pop() = %v, %v", stale, err)
			}

			// The first worker hangs past timeout + grace; another worker takes the job.
			offset.Add(int64(2 * time.Minute))

			fresh, err := d.Pop(ctx, []string{"default"})
			if err != nil || fresh == nil {
				t.Fatalf("second Pop() = %v, %v", fresh, err)
			}

			if err := settle(d)(ctx, stale); !errors.Is(err, queue.ErrReservationLost) {
				t.Fatalf("stale %s() = %v, want ErrReservationLost", name, err)
			}

			if n, _ := d.Pending(ctx); n != 1 {
				t.Fatalf("Pending() after stale %s = %d, want 1 (job owned by the second worker)", name, n)
			}

			if failed, _ := d.Failed(ctx); len(failed) != 0 {
				t.Fatalf("Failed() after stale %s = %v, want none", name, failed)
			}

			if err := d.Ack(ctx, fresh); err != nil {
				t.Fatalf("fresh Ack() = %v", err)
			}

			if n, _ := d.Pending(ctx); n != 0 {
				t.Errorf("Pending() after fresh Ack = %d, want 0", n)
			}
		})
	}
}

func TestDatabaseRetryResetsAttemptsAndAvailability(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	d := newDatabaseDriver(t, openSQLite(t), queue.DatabaseOptions{Now: clock})
	ctx := t.Context()

	for _, id := range []string{"a", "b"} {
		m := &queue.Message{
			ID: id, Kind: "k", Queue: "default", Payload: []byte(`{}`), Attempts: 4,
			AvailableAt: now.Add(-time.Hour), CreatedAt: now.Add(-time.Hour), LastError: "boom",
		}
		if err := d.Push(ctx, m); err != nil {
			t.Fatal(err)
		}

		if err := d.Fail(ctx, m); err != nil {
			t.Fatal(err)
		}
	}

	if n, err := d.Retry(ctx, "missing"); err != nil || n != 0 {
		t.Fatalf("Retry(missing) = %d, %v, want 0, nil", n, err)
	}

	if n, err := d.Retry(ctx, "a"); err != nil || n != 1 {
		t.Fatalf("Retry(a) = %d, %v, want 1, nil", n, err)
	}

	m, err := d.Pop(ctx, []string{"default"})
	if err != nil || m == nil {
		t.Fatalf("Pop() = %v, %v", m, err)
	}

	if m.ID != "a" || m.Attempts != 0 || !m.AvailableAt.Equal(now) || m.LastError != "boom" {
		t.Errorf("retried message = %+v, want id a, 0 attempts, available now, last error kept", m)
	}

	failed, err := d.Failed(ctx)
	if err != nil || len(failed) != 1 || failed[0].ID != "b" {
		t.Fatalf("Failed() = %+v, %v, want only b", failed, err)
	}

	if n, err := d.Forget(ctx, "b", "missing"); err != nil || n != 1 {
		t.Fatalf("Forget(b, missing) = %d, %v, want 1, nil", n, err)
	}

	if failed, err := d.Failed(ctx); err != nil || len(failed) != 0 {
		t.Fatalf("Failed() after Forget = %+v, %v, want empty", failed, err)
	}
}

func TestDatabaseRetryRollsBackOnConflict(t *testing.T) {
	t.Parallel()

	d := newDatabaseDriver(t, openSQLite(t), queue.DatabaseOptions{})
	ctx := t.Context()
	m := &queue.Message{ID: "dup", Kind: "k", Queue: "default", Payload: []byte(`{}`), CreatedAt: time.Now()}

	if err := d.Push(ctx, m); err != nil {
		t.Fatal(err)
	}

	if err := d.Fail(ctx, m); err != nil {
		t.Fatal(err)
	}

	// A job with the same id is queued again: retrying must not lose the
	// failed copy.
	if err := d.Push(ctx, m); err != nil {
		t.Fatal(err)
	}

	if _, err := d.Retry(ctx); err == nil {
		t.Fatal("Retry() with a duplicate id = nil, want error")
	}

	if failed, err := d.Failed(ctx); err != nil || len(failed) != 1 {
		t.Fatalf("Failed() after failed Retry = %+v, %v, want the job kept", failed, err)
	}
}

func TestDatabaseDispatchTx(t *testing.T) {
	t.Parallel()

	db := openSQLite(t)
	d := newDatabaseDriver(t, db, queue.DatabaseOptions{})
	ctx := t.Context()

	var ran []int64

	q := newQueue(t, d, queue.Handle(func(_ context.Context, j sendInvoice) error {
		ran = append(ran, j.OrderID)
		return nil
	}))

	rollback := errors.New("rollback")

	err := db.Tx(ctx, func(tx *database.Tx) error {
		if err := q.DispatchTx(ctx, tx, sendInvoice{OrderID: 1}); err != nil {
			return err
		}

		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("Tx() = %v, want rollback", err)
	}

	if err := db.Tx(
		ctx,
		func(tx *database.Tx) error { return q.DispatchTx(ctx, tx, sendInvoice{OrderID: 2}) },
	); err != nil {
		t.Fatal(err)
	}

	if err := q.DispatchTx(ctx, db, unknownJob{}); !errors.Is(err, queue.ErrUnknownKind) {
		t.Errorf("DispatchTx(unknown) = %v, want ErrUnknownKind", err)
	}

	if err := q.Work(ctx, queue.WorkOptions{StopWhenEmpty: true}); err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff([]int64{2}, ran); diff != "" {
		t.Errorf("jobs run (-want +got):\n%s", diff)
	}
}

func TestDatabaseErrorsOnClosedDB(t *testing.T) {
	t.Parallel()

	db := openSQLite(t)
	d := newDatabaseDriver(t, db, queue.DatabaseOptions{})
	ctx := t.Context()

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	m := &queue.Message{ID: "x", Kind: "k", Queue: "default"}

	tests := []struct {
		name    string
		run     func() error
		wantMsg string
	}{
		{name: "CreateTables", run: func() error { return d.CreateTables(ctx) }, wantMsg: "queue: create tables"},
		{
			name:    "Pop",
			run:     func() error { _, err := d.Pop(ctx, []string{"default"}); return err },
			wantMsg: "queue: reserve",
		},
		{name: "Push", run: func() error { return d.Push(ctx, m) }},
		{name: "Ack", run: func() error { return d.Ack(ctx, m) }},
		{name: "Release", run: func() error { return d.Release(ctx, m) }},
		{name: "Fail", run: func() error { return d.Fail(ctx, m) }},
		{name: "Failed", run: func() error { _, err := d.Failed(ctx); return err }},
		{name: "Retry", run: func() error { _, err := d.Retry(ctx); return err }},
		{name: "Forget", run: func() error { _, err := d.Forget(ctx); return err }},
		{name: "Pending", run: func() error { _, err := d.Pending(ctx); return err }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.run()
			if err == nil || !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("%s() on closed DB = %v, want error containing %q", tt.name, err, tt.wantMsg)
			}
		})
	}
}

// sqlLog records every statement sent through a DB.
type sqlLog struct {
	mu   sync.Mutex
	stmt []string
}

func (l *sqlLog) ObserveQuery(_ context.Context, q database.Query) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.stmt = append(l.stmt, strings.Join(strings.Fields(q.SQL), " "))
}

func (l *sqlLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return append([]string(nil), l.stmt...)
}

// dialectDB runs SQL rendered for dl on a SQLite connection: SQLite rejects
// the server-only syntax (FOR UPDATE, inline INDEX), so the statements are
// recorded and the error path is exercised without a server.
func dialectDB(t *testing.T, dl database.Dialect) (*database.DB, *sqlLog) {
	t.Helper()

	db := database.FromSQL(openSQLite(t).SQL(), dl)
	log := &sqlLog{}
	db.SetQueryObserver(log)

	return db, log
}

func TestDatabaseMySQLStatements(t *testing.T) {
	t.Parallel()

	db, log := dialectDB(t, database.MySQL)

	d, err := queue.NewDatabase(db, queue.DatabaseOptions{Table: "jobs", FailedTable: "failed"})
	if err != nil {
		t.Fatal(err)
	}

	if err := d.CreateTables(t.Context()); err == nil || !strings.Contains(err.Error(), "queue: create tables") {
		t.Fatalf("CreateTables() on SQLite with MySQL DDL = %v, want create tables error", err)
	}

	if _, err := d.Pop(
		t.Context(),
		[]string{"default"},
	); err == nil ||
		!strings.Contains(err.Error(), "queue: reserve") {
		t.Fatalf("Pop() on SQLite with MySQL locking = %v, want reserve error", err)
	}

	got := log.all()
	if len(got) != 2 {
		t.Fatalf("statements = %q, want CREATE TABLE and SELECT", got)
	}

	checks := []struct {
		stmt string
		want []string
	}{
		{got[0], []string{
			"CREATE TABLE IF NOT EXISTS `jobs`", "id VARBINARY(255) NOT NULL PRIMARY KEY",
			"last_error LONGTEXT NOT NULL,", "INDEX `jobs_queue_available` (queue, available_at)",
		}},
		{got[1], []string{"FROM `jobs` WHERE queue = ?", "LIMIT 1 FOR UPDATE SKIP LOCKED"}},
	}

	for _, c := range checks {
		for _, w := range c.want {
			if !strings.Contains(c.stmt, w) {
				t.Errorf("statement %q does not contain %q", c.stmt, w)
			}
		}
	}

	if strings.Contains(got[0], "DEFAULT ''") {
		t.Errorf("MySQL DDL %q has a TEXT default, which MySQL 8 refuses", got[0])
	}
}

func TestDatabasePostgresStatements(t *testing.T) {
	t.Parallel()

	db, log := dialectDB(t, database.Postgres)

	d, err := queue.NewDatabase(db, queue.DatabaseOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := d.Pop(
		t.Context(),
		[]string{"default"},
	); err == nil ||
		!strings.Contains(err.Error(), "queue: reserve") {
		t.Fatalf("Pop() on SQLite with Postgres locking = %v, want reserve error", err)
	}

	got := log.all()
	if len(got) != 1 {
		t.Fatalf("statements = %q, want one UPDATE", got)
	}

	for _, w := range []string{
		`UPDATE "jobs" SET reserved_at = $1`, `WHERE queue = $2 AND available_at <= $3`,
		`reserved_at + timeout_ms + $4 <= $5`, `LIMIT 1 FOR UPDATE SKIP LOCKED) RETURNING id,`,
	} {
		if !strings.Contains(got[0], w) {
			t.Errorf("statement %q does not contain %q", got[0], w)
		}
	}
}

func TestDatabaseSQLiteHasNoRowLocks(t *testing.T) {
	t.Parallel()

	db, log := dialectDB(t, database.SQLite)
	d := newDatabaseDriver(t, db, queue.DatabaseOptions{})

	if m, err := d.Pop(t.Context(), []string{"default"}); err != nil || m != nil {
		t.Fatalf("Pop() on empty queue = %v, %v, want nil, nil", m, err)
	}

	for _, s := range log.all() {
		if strings.Contains(s, "FOR UPDATE") {
			t.Errorf("SQLite statement %q uses FOR UPDATE, which SQLite rejects", s)
		}
	}
}

func TestQueueDriverAndNilLogger(t *testing.T) {
	t.Parallel()

	d := queue.NewMemory()
	q := queue.New(d, nil)

	if q.Driver() != d {
		t.Errorf("Driver() = %v, want the driver passed to New", q.Driver())
	}

	reg := queue.Handle(func(context.Context, sendInvoice) error { return nil })
	if reg.Kind() != "send_invoice" {
		t.Errorf("Registration.Kind() = %q, want send_invoice", reg.Kind())
	}

	if err := q.Register(reg); err != nil {
		t.Fatal(err)
	}

	if err := q.Dispatch(t.Context(), sendInvoice{OrderID: 1}); err != nil {
		t.Fatal(err)
	}

	if err := q.DispatchTx(t.Context(), nil, sendInvoice{OrderID: 1}); !errors.Is(err, queue.ErrNoTx) {
		t.Errorf("DispatchTx() on Memory = %v, want ErrNoTx", err)
	}
}
