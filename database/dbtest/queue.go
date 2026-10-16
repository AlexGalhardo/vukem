package dbtest

import (
	"context"
	"errors"
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
}
