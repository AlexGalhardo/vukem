package dbtest

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/queue"
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

	var (
		mu   sync.Mutex
		runs = map[int]int{}
	)

	q := queue.New(d, slog.New(slog.DiscardHandler))
	if err := q.Register(queue.Handle(func(_ context.Context, j dbtJob) error {
		mu.Lock()
		runs[j.N]++
		mu.Unlock()

		if j.N < 0 {
			return errors.New("negative")
		}

		return nil
	}, queue.Retries(0))); err != nil {
		t.Fatal(err)
	}

	pending := func() int {
		t.Helper()

		n, err := d.Pending(ctx)
		if err != nil {
			t.Fatal(err)
		}

		return n
	}

	t.Run("ConcurrentWorkersRunEachJobOnce", func(t *testing.T) {
		const jobs = 60

		for i := range jobs {
			if err := q.Dispatch(ctx, dbtJob{N: i}); err != nil {
				t.Fatal(err)
			}
		}

		// Two "processes" with four workers each.
		var wg sync.WaitGroup
		for range 2 {
			wg.Go(func() {
				if err := q.Work(ctx, queue.WorkOptions{Concurrency: 4, StopWhenEmpty: true}); err != nil {
					t.Error(err)
				}
			})
		}

		wg.Wait()

		mu.Lock()
		defer mu.Unlock()

		for i := range jobs {
			if runs[i] != 1 {
				t.Errorf("job %d ran %d times", i, runs[i])
			}
		}

		if n := pending(); n != 0 {
			t.Errorf("pending = %d", n)
		}
	})

	t.Run("DispatchTxFollowsTheTransaction", func(t *testing.T) {
		rollback := errors.New("rollback")

		err := db.Tx(ctx, func(tx *database.Tx) error {
			if err := q.DispatchTx(ctx, tx, dbtJob{N: 1000}); err != nil {
				return err
			}

			return rollback
		})
		if !errors.Is(err, rollback) || pending() != 0 {
			t.Fatalf("rolled back tx = %v, pending %d", err, pending())
		}

		if err := db.Tx(
			ctx,
			func(tx *database.Tx) error { return q.DispatchTx(ctx, tx, dbtJob{N: 1001}) },
		); err != nil {
			t.Fatal(err)
		}

		if pending() != 1 {
			t.Fatalf("committed tx: pending = %d", pending())
		}

		if err := q.Work(ctx, queue.WorkOptions{StopWhenEmpty: true}); err != nil || pending() != 0 {
			t.Fatalf("work = %v pending %d", err, pending())
		}
	})

	t.Run("FailedRetryForget", func(t *testing.T) {
		_ = q.Dispatch(ctx, dbtJob{N: -1})
		_ = q.Dispatch(ctx, dbtJob{N: -2})

		if err := q.Work(ctx, queue.WorkOptions{StopWhenEmpty: true}); err != nil {
			t.Fatal(err)
		}

		failed, err := d.Failed(ctx)
		if err != nil || len(failed) != 2 || failed[0].LastError != "negative" || failed[0].Attempts != 1 {
			t.Fatalf("failed = %+v %v", failed, err)
		}

		if n, err := d.Retry(ctx, failed[0].ID); err != nil || n != 1 || pending() != 1 {
			t.Fatalf("retry = %d %v pending %d", n, err, pending())
		}

		if n, err := d.Forget(ctx); err != nil || n != 1 {
			t.Fatalf("forget all = %d %v", n, err)
		}

		if err := q.Work(ctx, queue.WorkOptions{StopWhenEmpty: true}); err != nil {
			t.Fatal(err)
		}

		if failed, _ := d.Failed(ctx); len(failed) != 1 {
			t.Fatalf("retried job should fail again: %+v", failed)
		}

		_, _ = d.Forget(ctx)
	})

	t.Run("AbandonedReservationIsTakenOver", func(t *testing.T) {
		_ = q.Dispatch(ctx, dbtJob{N: 2000}, queue.Timeout(time.Second))

		m, err := d.Pop(ctx, []string{"default"}) // a worker reserves it and dies
		if err != nil || m == nil {
			t.Fatalf("pop = %v %v", m, err)
		}

		if again, _ := d.Pop(ctx, []string{"default"}); again != nil {
			t.Fatal("reserved job handed out twice")
		}

		offset.Add(int64(3 * time.Second)) // past timeout + grace

		if again, _ := d.Pop(ctx, []string{"default"}); again == nil || again.ID != m.ID {
			t.Fatalf("abandoned job not taken over: %v", again)
		}

		_ = d.Ack(ctx, m)
	})
}
