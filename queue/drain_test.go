package queue_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AlexGalhardo/vukem/queue"
)

// A job still running StopTimeout after Work is told to stop has its
// context cancelled; it goes back to the queue without using an attempt,
// so a job with no retries left is not failed by a shutdown.
func TestWorkStopTimeoutCancelsRunningJobs(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		d := queue.NewMemory()
		q := newQueue(t, d, queue.Handle(func(ctx context.Context, _ sendInvoice) error {
			<-ctx.Done()
			return ctx.Err()
		}, queue.Retries(0), queue.Timeout(time.Hour)))

		_ = q.Dispatch(t.Context(), sendInvoice{OrderID: 1})

		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error)

		go func() { done <- q.Work(ctx, queue.WorkOptions{StopTimeout: 5 * time.Second}) }()

		time.Sleep(time.Second)
		cancel()

		start := time.Now()
		if err := <-done; err != nil {
			t.Fatalf("Work() = %v, want nil", err)
		}

		if waited := time.Since(start); waited != 5*time.Second {
			t.Fatalf("Work waited %v after cancel, want StopTimeout (5s)", waited)
		}

		if len(d.Failed()) != 0 || d.Pending() != 1 {
			t.Fatalf("failed=%d pending=%d, want the job released (0/1)", len(d.Failed()), d.Pending())
		}

		m, _ := d.Pop(t.Context(), []string{"default"})
		if m == nil || m.Attempts != 0 {
			t.Fatalf("released job = %+v, want attempts 0", m)
		}
	})
}

// Without StopTimeout, Work keeps waiting for running jobs (bounded by
// their own timeout), as before.
func TestWorkWithoutStopTimeoutWaitsForJobs(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var finished atomic.Bool

		q := newQueue(t, queue.NewMemory(), queue.Handle(func(context.Context, sendInvoice) error {
			time.Sleep(time.Minute)
			finished.Store(true)

			return nil
		}))

		_ = q.Dispatch(t.Context(), sendInvoice{OrderID: 1})

		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error)

		go func() { done <- q.Work(ctx, queue.WorkOptions{}) }()

		time.Sleep(time.Second)
		cancel()

		if err := <-done; err != nil || !finished.Load() {
			t.Fatalf("Work() = %v finished=%v, want nil and the job finished", err, finished.Load())
		}
	})
}

// stopRound dispatches nothing: it runs Work until one job is in flight,
// cancels it and waits for Work to give up on it after StopTimeout.
func stopRound(t *testing.T, q *queue.Queue) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error)

	go func() { done <- q.Work(ctx, queue.WorkOptions{StopTimeout: 5 * time.Second}) }()

	time.Sleep(time.Second)
	cancel()

	if err := <-done; err != nil {
		t.Fatalf("Work() = %v, want nil", err)
	}
}

// Only a failure caused by the shutdown cancel is free: a job that returns
// its own error while stopping is retried or failed as usual.
func TestWorkStopTimeoutKeepsRealFailures(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		d := queue.NewMemory()
		q := newQueue(t, d, queue.Handle(func(ctx context.Context, _ sendInvoice) error {
			<-ctx.Done()
			return errors.New("card declined")
		}, queue.Retries(0), queue.Timeout(time.Hour)))

		_ = q.Dispatch(t.Context(), sendInvoice{OrderID: 1})
		stopRound(t, q)

		if failed := d.Failed(); len(failed) != 1 || failed[0].LastError != "card declined" {
			t.Fatalf("failed = %+v, want the job failed with its own error", failed)
		}
	})
}

// Interruptions are free three times in a row; the next one uses an
// attempt, so a job that never finishes before StopTimeout cannot be re-run
// for free on every deploy.
func TestWorkStopTimeoutCapsFreeInterruptions(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		d := queue.NewMemory()
		q := newQueue(t, d, queue.Handle(func(ctx context.Context, _ sendInvoice) error {
			<-ctx.Done()
			return ctx.Err()
		}, queue.Retries(0), queue.Timeout(time.Hour)))

		_ = q.Dispatch(t.Context(), sendInvoice{OrderID: 1})

		for round := range 3 {
			stopRound(t, q)

			if len(d.Failed()) != 0 || d.Pending() != 1 {
				t.Fatalf("round %d: failed=%d pending=%d, want the job released", round, len(d.Failed()), d.Pending())
			}
		}

		stopRound(t, q)

		if len(d.Failed()) != 1 {
			t.Fatalf("after 4 interruptions failed=%d pending=%d, want the job failed", len(d.Failed()), d.Pending())
		}
	})
}

// blockingAck never settles until its context ends.
type blockingAck struct{ *queue.Memory }

func (blockingAck) Ack(ctx context.Context, _ *queue.Message) error {
	<-ctx.Done()
	return ctx.Err()
}

// A store that hangs while settling cannot hold the worker: each settle
// call is bounded.
func TestWorkBoundsSettleCalls(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		q := newQueue(t, blockingAck{queue.NewMemory()},
			queue.Handle(func(context.Context, sendInvoice) error { return nil }))

		_ = q.Dispatch(t.Context(), sendInvoice{OrderID: 1})

		start := time.Now()
		err := q.Work(t.Context(), queue.WorkOptions{StopWhenEmpty: true})

		if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) != 10*time.Second {
			t.Fatalf("Work() = %v after %v, want DeadlineExceeded after 10s", err, time.Since(start))
		}
	})
}
