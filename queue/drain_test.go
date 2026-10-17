package queue_test

import (
	"context"
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
