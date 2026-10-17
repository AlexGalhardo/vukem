package dbtest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/queue"
	"github.com/AlexGalhardo/vukem/queue/queuetest"
)

type dbtJob struct {
	N int `json:"n"`
}

func (dbtJob) Kind() string { return "dbt_job" }

func testQueue(ctx context.Context, t *testing.T, db *database.DB) {
	t.Helper()

	var offset atomic.Int64 // added to the real clock, which stamps messages

	d, err := queue.NewDatabase(db, queue.DatabaseOptions{
		Table: "dbt_jobs", FailedTable: "dbt_failed_jobs", Grace: time.Second,
		Now: func() time.Time { return time.Now().Add(time.Duration(offset.Load())) },
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := d.CreateTables(ctx); err != nil {
		t.Fatal(err)
	}

	queuetest.Run(t, d, queuetest.Options{
		Pending: d.Pending,
		Advance: func(by time.Duration) { offset.Add(int64(by)) },
	})

	t.Run("DispatchTxFollowsTheTransaction", func(t *testing.T) {
		q := queue.New(d, slog.New(slog.DiscardHandler))
		if err := q.Register(queue.Handle(func(context.Context, dbtJob) error { return nil })); err != nil {
			t.Fatal(err)
		}

		pending := func() int {
			n, err := d.Pending(ctx)
			if err != nil {
				t.Fatal(err)
			}

			return n
		}

		rollback := errors.New("rollback")

		err := db.Tx(ctx, func(tx *database.Tx) error {
			if err := q.DispatchTx(ctx, tx, dbtJob{N: 1}); err != nil {
				return err
			}

			return rollback
		})
		if !errors.Is(err, rollback) || pending() != 0 {
			t.Fatalf("rolled back tx = %v, pending %d", err, pending())
		}

		if err := db.Tx(ctx, func(tx *database.Tx) error { return q.DispatchTx(ctx, tx, dbtJob{N: 2}) }); err != nil {
			t.Fatal(err)
		}

		if pending() != 1 {
			t.Fatalf("committed tx: pending = %d", pending())
		}

		if err := q.Work(ctx, queue.WorkOptions{StopWhenEmpty: true}); err != nil || pending() != 0 {
			t.Fatalf("work = %v pending %d", err, pending())
		}
	})

	t.Run("SameMillisecondIsFIFO", func(t *testing.T) {
		testQueueSameMillisecondFIFO(ctx, t, d)
	})

	t.Run("CreateTablesUpgradesOldSchema", func(t *testing.T) {
		testQueueUpgradesOldSchema(ctx, t, db)
	})
}

// sameMillisecond returns n messages created and available at the same
// millisecond, with ids in descending order so an id tiebreak reverses
// them.
func sameMillisecond(n int, at time.Time) []*queue.Message {
	ms := make([]*queue.Message, n)
	for i := range ms {
		ms[i] = &queue.Message{
			ID: fmt.Sprintf("job-%04d", n-i), Kind: "dbt_job", Queue: "fifo", Payload: []byte(`{}`),
			Timeout: time.Minute, AvailableAt: at, CreatedAt: at,
		}
	}

	return ms
}

// drainIDs pops every job of queue name and acks it, in pop order.
func drainIDs(ctx context.Context, t *testing.T, d *queue.Database, name string) []string {
	t.Helper()

	var ids []string

	for {
		m, err := d.Pop(ctx, []string{name})
		if err != nil {
			t.Fatalf("Pop() = %v", err)
		}

		if m == nil {
			return ids
		}

		ids = append(ids, m.ID)

		if err := d.Ack(ctx, m); err != nil {
			t.Fatalf("Ack(%s) = %v", m.ID, err)
		}
	}
}

func checkOrder(t *testing.T, got, want []string) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("popped %d jobs, want %d", len(got), len(want))
	}

	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("pop #%d = %s, want %s (push order)", i, got[i], want[i])
		}
	}
}

func testQueueSameMillisecondFIFO(ctx context.Context, t *testing.T, d *queue.Database) {
	t.Helper()

	msgs := sameMillisecond(200, time.Now().Add(-time.Hour).Truncate(time.Millisecond))

	want := make([]string, len(msgs))
	for i, m := range msgs {
		if err := d.Push(ctx, m); err != nil {
			t.Fatalf("Push(%s) = %v", m.ID, err)
		}

		want[i] = m.ID
	}

	checkOrder(t, drainIDs(ctx, t, d, "fifo"), want)
}

// testQueueUpgradesOldSchema runs CreateTables on tables made by an older
// release (no seq column) holding a pending job.
func testQueueUpgradesOldSchema(ctx context.Context, t *testing.T, db *database.DB) {
	t.Helper()

	dl := db.Dialect()
	cols := `id VARCHAR(255) NOT NULL PRIMARY KEY, queue VARCHAR(255) NOT NULL, kind VARCHAR(255) NOT NULL,
		payload TEXT NOT NULL, attempts INTEGER NOT NULL DEFAULT 0, max_retries INTEGER NOT NULL DEFAULT 0,
		timeout_ms BIGINT NOT NULL DEFAULT 0, available_at BIGINT NOT NULL, created_at BIGINT NOT NULL,
		last_error TEXT NOT NULL`

	at := time.Now().Add(-2 * time.Hour).Truncate(time.Millisecond)

	for _, stmt := range []string{
		`CREATE TABLE ` + dl.Quote("dbt_old_jobs") + ` (` + cols + `, reserved_at BIGINT NOT NULL DEFAULT 0)`,
		`CREATE TABLE ` + dl.Quote("dbt_old_failed_jobs") + ` (` + cols + `, failed_at BIGINT NOT NULL)`,
		fmt.Sprintf(`INSERT INTO %s (id, queue, kind, payload, timeout_ms, available_at, created_at, last_error)
			VALUES ('old', 'fifo', 'dbt_job', '{}', 60000, %d, %d, '')`, dl.Quote("dbt_old_jobs"), at.UnixMilli(), at.UnixMilli()),
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("old schema %q: %v", stmt, err)
		}
	}

	d, err := queue.NewDatabase(db, queue.DatabaseOptions{Table: "dbt_old_jobs", FailedTable: "dbt_old_failed_jobs"})
	if err != nil {
		t.Fatal(err)
	}

	for range 2 { // the second run finds the column and changes nothing
		if err := d.CreateTables(ctx); err != nil {
			t.Fatalf("CreateTables() on an old schema = %v", err)
		}
	}

	want := []string{"old"}

	for _, m := range sameMillisecond(20, at) {
		if err := d.Push(ctx, m); err != nil {
			t.Fatalf("Push(%s) = %v", m.ID, err)
		}

		want = append(want, m.ID)
	}

	checkOrder(t, drainIDs(ctx, t, d, "fifo"), want)
}
