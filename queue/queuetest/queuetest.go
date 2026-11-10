// Package queuetest is the conformance suite for queue drivers:
//
//	queuetest.Run(t, driver, queuetest.Options{Pending: driver.Pending, Advance: clock.Add})
//
// Memory and database run it in core, Redis in drivers/redis.
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/subsystems.md#queue.
package queuetest

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/queue"
)

// Options describe the driver under test.
type Options struct {
	// Pending counts stored jobs (waiting or running).
	Pending func(ctx context.Context) (int, error)
	// Advance moves the driver's clock forward; nil skips the test of
	// abandoned reservations.
	Advance func(d time.Duration)
}

type confJob struct {
	N int `json:"n"`
}

func (confJob) Kind() string { return "queuetest_job" }

// Run executes the suite. The driver must start empty; subtests run in
// order.
func Run(t *testing.T, d queue.Driver, o Options) {
	t.Helper()

	ctx := t.Context()

	var (
		mu   sync.Mutex
		runs = map[int]int{}
	)

	q := queue.New(d, slog.New(slog.DiscardHandler))
	if err := q.Register(queue.Handle(func(_ context.Context, j confJob) error {
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

	pending := func(t *testing.T) int {
		t.Helper()

		n, err := o.Pending(ctx)
		if err != nil {
			t.Fatal(err)
		}

		return n
	}

	t.Run("ConcurrentWorkersRunEachJobOnce", func(t *testing.T) {
		const jobs = 60

		for i := range jobs {
			if err := q.Dispatch(ctx, confJob{N: i}); err != nil {
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

		if n := pending(t); n != 0 {
			t.Errorf("pending = %d", n)
		}
	})

	t.Run("QueuesAndDelay", func(t *testing.T) {
		_ = q.Dispatch(ctx, confJob{N: 100}, queue.OnQueue("low"))
		_ = q.Dispatch(ctx, confJob{N: 101}, queue.OnQueue("high"))
		_ = q.Dispatch(ctx, confJob{N: 102}, queue.Delay(time.Hour))

		first, err := d.Pop(ctx, []string{"high", "low"})
		if err != nil || first == nil || first.Queue != "high" {
			t.Fatalf("first pop = %+v %v, want the high queue", first, err)
		}

		if delayed, _ := d.Pop(ctx, []string{"default"}); delayed != nil {
			t.Fatalf("delayed job popped early: %+v", delayed)
		}

		second, _ := d.Pop(ctx, []string{"high", "low"})
		if second == nil || second.Queue != "low" {
			t.Fatalf("second pop = %+v", second)
		}

		for _, m := range []*queue.Message{first, second} {
			if err := d.Ack(ctx, m); err != nil {
				t.Fatal(err)
			}
		}

		if n := pending(t); n != 1 {
			t.Fatalf("pending = %d, want the delayed job", n)
		}

		if o.Advance == nil {
			return
		}

		o.Advance(time.Hour)

		m, _ := d.Pop(ctx, []string{"default"})
		if m == nil {
			t.Fatal("delayed job not available after the delay")
		}

		_ = d.Ack(ctx, m)
	})

	t.Run("ReleaseKeepsAttempts", func(t *testing.T) {
		_ = q.Dispatch(ctx, confJob{N: 200})

		m, _ := d.Pop(ctx, []string{"default"})
		if m == nil {
			t.Fatal("nothing to pop")
		}

		m.Attempts, m.LastError, m.AvailableAt = 2, "try again", time.Now().Add(-time.Second)
		if err := d.Release(ctx, m); err != nil {
			t.Fatal(err)
		}

		again, _ := d.Pop(ctx, []string{"default"})
		if again == nil || again.ID != m.ID || again.Attempts != 2 || again.LastError != "try again" {
			t.Fatalf("released job = %+v", again)
		}

		_ = d.Ack(ctx, again)
	})

	t.Run("FailedRetryForget", func(t *testing.T) {
		fs, ok := d.(queue.FailedStore)
		if !ok {
			t.Skip("driver does not keep failed jobs")
		}

		_ = q.Dispatch(ctx, confJob{N: -1})
		_ = q.Dispatch(ctx, confJob{N: -2})

		if err := q.Work(ctx, queue.WorkOptions{StopWhenEmpty: true}); err != nil {
			t.Fatal(err)
		}

		failed, err := fs.Failed(ctx)
		if err != nil || len(failed) != 2 || failed[0].LastError != "negative" || failed[0].Attempts != 1 ||
			failed[0].FailedAt.IsZero() {
			t.Fatalf("failed = %+v %v", failed, err)
		}

		if n, err := fs.Retry(ctx, failed[0].ID); err != nil || n != 1 || pending(t) != 1 {
			t.Fatalf("retry = %d %v pending %d", n, err, pending(t))
		}

		if n, err := fs.Forget(ctx); err != nil || n != 1 {
			t.Fatalf("forget all = %d %v", n, err)
		}

		if err := q.Work(ctx, queue.WorkOptions{StopWhenEmpty: true}); err != nil {
			t.Fatal(err)
		}

		if failed, _ := fs.Failed(ctx); len(failed) != 1 || failed[0].Attempts != 1 {
			t.Fatalf("retried job should fail again with fresh attempts: %+v", failed)
		}

		if n, _ := fs.Retry(ctx); n != 1 {
			t.Fatalf("retry all = %d", n)
		}

		_ = q.Work(ctx, queue.WorkOptions{StopWhenEmpty: true})
		_, _ = fs.Forget(ctx)
	})

	t.Run("AbandonedReservationIsTakenOver", func(t *testing.T) {
		if o.Advance == nil {
			t.Skip("no clock control")
		}

		_ = q.Dispatch(ctx, confJob{N: 300}, queue.Timeout(time.Second))

		m, err := d.Pop(ctx, []string{"default"}) // a worker reserves it and dies
		if err != nil || m == nil {
			t.Fatalf("pop = %v %v", m, err)
		}

		if again, _ := d.Pop(ctx, []string{"default"}); again != nil {
			t.Fatal("reserved job handed out twice")
		}

		o.Advance(time.Hour) // past timeout + grace

		again, _ := d.Pop(ctx, []string{"default"})
		if again == nil || again.ID != m.ID {
			t.Fatalf("abandoned job not taken over: %v", again)
		}

		_ = d.Ack(ctx, again)

		if n := pending(t); n != 0 {
			t.Fatalf("pending = %d", n)
		}
	})

	// A worker that outlived its reservation must not settle the job the
	// next worker now holds.
	t.Run("StaleSettleKeepsTheNewReservation", func(t *testing.T) {
		if o.Advance == nil {
			t.Skip("no clock control")
		}

		settles := []struct {
			name   string
			settle func(*queue.Message) error
		}{
			{"Ack", func(m *queue.Message) error { return d.Ack(ctx, m) }},
			{"Release", func(m *queue.Message) error { return d.Release(ctx, m) }},
			{"Fail", func(m *queue.Message) error { return d.Fail(ctx, m) }},
		}

		for _, tt := range settles {
			t.Run(tt.name, func(t *testing.T) {
				_ = q.Dispatch(ctx, confJob{N: 301}, queue.Timeout(time.Second))

				m, err := d.Pop(ctx, []string{"default"})
				if err != nil || m == nil {
					t.Fatalf("pop = %v %v", m, err)
				}

				o.Advance(time.Hour) // past timeout + grace: m's worker is presumed dead

				again, _ := d.Pop(ctx, []string{"default"})
				if again == nil || again.ID != m.ID {
					t.Fatalf("abandoned job not taken over: %v", again)
				}

				if err := tt.settle(m); !errors.Is(err, queue.ErrReservationLost) {
					t.Fatalf("stale %s = %v, want ErrReservationLost", tt.name, err)
				}

				if third, _ := d.Pop(ctx, []string{"default"}); third != nil {
					t.Fatalf("stale %s let the job be handed out again: %v", tt.name, third)
				}

				if err := d.Ack(ctx, again); err != nil {
					t.Fatalf("Ack(new holder) = %v", err)
				}

				if n := pending(t); n != 0 {
					t.Fatalf("pending = %d", n)
				}
			})
		}
	})

	// A driver is a queue.Uniquer or it refuses Unique; either way it never stores a duplicate silently.
	t.Run("UniqueJobs", func(t *testing.T) {
		before := pending(t)
		first := q.Dispatch(ctx, confJob{N: 400}, queue.Unique("conformance"))

		if u, ok := d.(queue.Uniquer); !ok || !u.SupportsUnique() {
			if !errors.Is(first, queue.ErrUniqueUnsupported) {
				t.Fatalf("Unique on a driver that is not a Uniquer = %v, want ErrUniqueUnsupported", first)
			}

			return
		}

		if first != nil {
			t.Fatal(first)
		}

		if err := q.Dispatch(ctx, confJob{N: 400}, queue.Unique("conformance")); err != nil {
			t.Fatalf("duplicate dispatch = %v, want nil (a no-op)", err)
		}

		if n := pending(t); n != before+1 {
			t.Fatalf("pending = %d after two unique dispatches, want %d", n, before+1)
		}

		m, err := d.Pop(ctx, []string{"default"})
		if err != nil || m == nil {
			t.Fatalf("pop = %v %v", m, err)
		}

		// Reserved (running) still counts: a dispatch now must not add a second copy.
		if err := q.Dispatch(ctx, confJob{N: 400}, queue.Unique("conformance")); err != nil {
			t.Fatal(err)
		}

		if n := pending(t); n != before+1 {
			t.Fatalf("pending = %d while the unique job runs, want %d", n, before+1)
		}

		_ = d.Ack(ctx, m)

		if n := pending(t); n != before {
			t.Fatalf("pending = %d after ack, want %d", n, before)
		}
	})
}
