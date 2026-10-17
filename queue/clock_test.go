package queue_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/queue"
)

// fakeClock is a settable clock safe for concurrent use.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// Delays and retry backoff follow the queue clock, not the wall clock, so
// a test advancing a fake clock makes delayed jobs due.
func TestClockDrivesDelaysAndBackoff(t *testing.T) {
	t.Parallel()

	clock := &fakeClock{now: time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)}

	var runs atomic.Int32

	d := queue.NewMemory()
	d.SetClock(clock.Now)

	q := newQueue(t, d, queue.Handle(func(context.Context, sendInvoice) error {
		if runs.Add(1) == 1 {
			return errors.New("try again")
		}

		return nil
	}, queue.Backoff(queue.Constant(time.Minute))))
	q.SetClock(clock.Now)

	if err := q.Dispatch(t.Context(), sendInvoice{OrderID: 1}, queue.Delay(10*time.Minute)); err != nil {
		t.Fatal(err)
	}

	work := func() {
		t.Helper()

		if err := q.Work(t.Context(), queue.WorkOptions{StopWhenEmpty: true}); err != nil {
			t.Fatal(err)
		}
	}

	steps := []struct {
		advance time.Duration
		want    int32
	}{
		{0, 0},                // delayed ten minutes on the queue clock
		{10 * time.Minute, 1}, // due: fails and is retried a minute later
		{30 * time.Second, 1}, // backoff not over
		{30 * time.Second, 2},
	}
	for i, s := range steps {
		clock.Advance(s.advance)
		work()

		if got := runs.Load(); got != s.want {
			t.Fatalf("step %d: runs = %d, want %d", i, got, s.want)
		}
	}

	if d.Pending() != 0 {
		t.Fatalf("Pending() = %d, want 0", d.Pending())
	}
}
