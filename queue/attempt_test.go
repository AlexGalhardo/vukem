package queue_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/queue"
)

// A handler that wraps ErrPermanent is not retried: the job goes straight
// to the failed jobs, keeping the handler's error.
func TestErrPermanentFailsWithoutRetry(t *testing.T) {
	t.Parallel()

	clock := &fakeClock{now: time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)}

	var runs atomic.Int32

	d := queue.NewMemory()
	d.SetClock(clock.Now)

	q := newQueue(t, d, queue.Handle(func(context.Context, sendInvoice) error {
		runs.Add(1)

		return fmt.Errorf("corrupt input: %w", queue.ErrPermanent)
	}, queue.Retries(3), queue.Backoff(queue.Constant(time.Minute))))
	q.SetClock(clock.Now)

	if err := q.Dispatch(t.Context(), sendInvoice{OrderID: 1}); err != nil {
		t.Fatal(err)
	}

	// Four rounds: enough for the three retries the job was dispatched with.
	for range 4 {
		if err := q.Work(t.Context(), queue.WorkOptions{StopWhenEmpty: true}); err != nil {
			t.Fatal(err)
		}

		clock.Advance(time.Minute)
	}

	if got := runs.Load(); got != 1 {
		t.Errorf("handler ran %d times, want 1: a permanent failure is not retried", got)
	}

	if got := d.Pending(); got != 0 {
		t.Errorf("Pending() = %d, want 0", got)
	}

	failed := d.Failed()
	if len(failed) != 1 {
		t.Fatalf("Failed() = %d jobs, want 1: a permanent failure is still recorded", len(failed))
	}

	if got := failed[0].LastError; !strings.Contains(got, "corrupt input") {
		t.Errorf("LastError = %q, want the handler's error", got)
	}

	if got := failed[0].Attempts; got != 1 {
		t.Errorf("Attempts = %d, want 1", got)
	}
}

// A handler reads its attempt number and its retry limit from the context,
// so it can tell "will be retried" from "this was the last attempt".
func TestAttemptFromRunsThroughRetries(t *testing.T) {
	t.Parallel()

	clock := &fakeClock{now: time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)}

	var (
		mu  sync.Mutex
		got []string
	)

	d := queue.NewMemory()
	d.SetClock(clock.Now)

	q := newQueue(t, d, queue.Handle(func(ctx context.Context, _ sendInvoice) error {
		a, ok := queue.AttemptFrom(ctx)

		mu.Lock()
		got = append(got, fmt.Sprintf("%d/%d last=%v ok=%v", a.Number, a.MaxRetries, a.Last(), ok))
		mu.Unlock()

		return errors.New("try again")
	}, queue.Retries(2), queue.Backoff(queue.Constant(time.Minute))))
	q.SetClock(clock.Now)

	if err := q.Dispatch(t.Context(), sendInvoice{OrderID: 1}); err != nil {
		t.Fatal(err)
	}

	for range 3 {
		if err := q.Work(t.Context(), queue.WorkOptions{StopWhenEmpty: true}); err != nil {
			t.Fatal(err)
		}

		clock.Advance(time.Minute)
	}

	want := []string{
		"1/2 last=false ok=true",
		"2/2 last=false ok=true",
		"3/2 last=true ok=true",
	}

	mu.Lock()
	defer mu.Unlock()

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("attempts seen by the handler (-want +got):\n%s", diff)
	}

	if len(d.Failed()) != 1 {
		t.Errorf("Failed() = %d jobs, want 1 after the last attempt", len(d.Failed()))
	}
}

// An Inline driver (Sync) never retries, so the handler sees its single
// attempt as the last one.
func TestAttemptFromOnSyncDriver(t *testing.T) {
	t.Parallel()

	var (
		got queue.Attempt
		ok  bool
	)

	q := newQueue(t, queue.Sync{}, queue.Handle(func(ctx context.Context, _ sendInvoice) error {
		got, ok = queue.AttemptFrom(ctx)

		return nil
	}, queue.Retries(5)))

	if err := q.Dispatch(t.Context(), sendInvoice{OrderID: 1}); err != nil {
		t.Fatal(err)
	}

	want := queue.Attempt{Number: 1, MaxRetries: 0}
	if !ok || got != want || !got.Last() {
		t.Errorf("AttemptFrom() = %+v, %v (last=%v), want %+v, true (last=true)", got, ok, got.Last(), want)
	}
}

// Outside a handler there is no attempt to report, so AttemptFrom says so
// instead of handing out a zero Attempt that reads as a last attempt.
func TestAttemptFromOutsideAHandler(t *testing.T) {
	t.Parallel()

	if got, ok := queue.AttemptFrom(t.Context()); ok || got != (queue.Attempt{}) {
		t.Errorf("AttemptFrom(plain ctx) = %+v, %v, want zero, false", got, ok)
	}
}

// A118: the doc comment for AttemptFrom used to justify its second return
// value with "where a zero Attempt would read as a last attempt". It reads
// as the opposite — Last is Number > MaxRetries, so a zero Attempt is
// 0 > 0 — and the hazard is the mirror image of the one claimed: a handler
// settling its row only on Last never settles it outside a queue. Pinned
// here so the comment and the code cannot drift apart again.
func TestZeroAttemptIsNotTheLast(t *testing.T) {
	t.Parallel()

	if (queue.Attempt{}).Last() {
		t.Error("Attempt{}.Last() = true; the doc comment describes the other way round")
	}

	// And the guard AttemptFrom offers is the one to use.
	if _, ok := queue.AttemptFrom(t.Context()); ok {
		t.Error("AttemptFrom(plain ctx) reported an attempt")
	}

	for _, tt := range []struct {
		a    queue.Attempt
		want bool
	}{
		{queue.Attempt{Number: 1, MaxRetries: 2}, false},
		{queue.Attempt{Number: 3, MaxRetries: 2}, true},
		{queue.Attempt{Number: 1, MaxRetries: 0}, true},
	} {
		if got := tt.a.Last(); got != tt.want {
			t.Errorf("Attempt%+v.Last() = %v, want %v", tt.a, got, tt.want)
		}
	}
}
