package queue_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/queue"
)

type sendInvoice struct {
	OrderID int64 `json:"order_id"`
}

func (sendInvoice) Kind() string { return "send_invoice" }

type unknownJob struct{}

func (unknownJob) Kind() string { return "unknown" }

func newQueue(t *testing.T, d queue.Driver, regs ...queue.Registration) *queue.Queue {
	t.Helper()

	q := queue.New(d, slog.New(slog.DiscardHandler))
	if err := q.Register(regs...); err != nil {
		t.Fatal(err)
	}

	return q
}

func TestRegisterAndDispatchErrors(t *testing.T) {
	t.Parallel()

	reg := queue.Handle(func(context.Context, sendInvoice) error { return nil })
	q := newQueue(t, queue.NewMemory(), reg)

	if err := q.Register(reg); !errors.Is(err, queue.ErrDuplicateKind) {
		t.Errorf("duplicate = %v", err)
	}

	if err := q.Dispatch(t.Context(), unknownJob{}); !errors.Is(err, queue.ErrUnknownKind) {
		t.Errorf("unknown = %v", err)
	}
}

func TestSyncDriverRunsInline(t *testing.T) {
	t.Parallel()

	var got []int64

	q := newQueue(t, queue.Sync{}, queue.Handle(func(_ context.Context, j sendInvoice) error {
		got = append(got, j.OrderID)
		if j.OrderID == 2 {
			return errors.New("boom")
		}

		return nil
	}))

	if err := q.Dispatch(t.Context(), sendInvoice{OrderID: 1}); err != nil {
		t.Fatal(err)
	}

	if err := q.Dispatch(t.Context(), sendInvoice{OrderID: 2}); err == nil || err.Error() != "boom" {
		t.Fatalf("sync error = %v", err)
	}

	if diff := cmp.Diff([]int64{1, 2}, got); diff != "" {
		t.Error(diff)
	}
}

func TestRetriesBackoffAndFailure(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var attempts []time.Time

		d := queue.NewMemory()
		start := time.Now()
		q := newQueue(t, d, queue.Handle(func(context.Context, sendInvoice) error {
			attempts = append(attempts, time.Now())
			return errors.New("smtp down")
		}, queue.Retries(2), queue.Backoff(queue.Exponential(10*time.Second))))

		if err := q.Dispatch(t.Context(), sendInvoice{OrderID: 7}); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error)

		go func() { done <- q.Work(ctx, queue.WorkOptions{Sleep: time.Second}) }()

		time.Sleep(time.Minute)
		synctest.Wait()
		cancel()

		if err := <-done; err != nil {
			t.Fatal(err)
		}

		offsets := make([]time.Duration, len(attempts))
		for i, a := range attempts {
			offsets[i] = a.Sub(start).Round(time.Second)
		}

		// Attempt 1 now, retry 1 after 10s, retry 2 after a further 20s.
		if diff := cmp.Diff([]time.Duration{0, 10 * time.Second, 30 * time.Second}, offsets); diff != "" {
			t.Errorf("attempt times (-want +got):\n%s", diff)
		}

		failed := d.Failed()
		if len(failed) != 1 || failed[0].Attempts != 3 || failed[0].LastError != "smtp down" || d.Pending() != 0 {
			t.Fatalf("failed = %+v pending=%d", failed, d.Pending())
		}
	})
}

func TestDelayQueuesAndPriority(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var (
			mu  sync.Mutex
			got []int64
		)

		q := newQueue(t, queue.NewMemory(), queue.Handle(func(_ context.Context, j sendInvoice) error {
			mu.Lock()
			got = append(got, j.OrderID)
			mu.Unlock()

			return nil
		}))

		ctx := t.Context()
		_ = q.Dispatch(ctx, sendInvoice{OrderID: 1})
		_ = q.Dispatch(ctx, sendInvoice{OrderID: 2}, queue.OnQueue("high"))
		_ = q.Dispatch(ctx, sendInvoice{OrderID: 3}, queue.Delay(time.Hour), queue.OnQueue("high"))
		_ = q.Dispatch(ctx, sendInvoice{OrderID: 4}, queue.OnQueue("ignored"))

		if err := q.Work(ctx, queue.WorkOptions{Queues: []string{"high", "default"}, StopWhenEmpty: true}); err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff([]int64{2, 1}, got); diff != "" {
			t.Errorf("before the delay (-want +got):\n%s", diff)
		}

		time.Sleep(time.Hour)

		if err := q.Work(ctx, queue.WorkOptions{Queues: []string{"high"}, MaxJobs: 1}); err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff([]int64{2, 1, 3}, got); diff != "" {
			t.Errorf("after the delay (-want +got):\n%s", diff)
		}
	})
}

func TestTimeoutPanicAndUnknownKind(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		d := queue.NewMemory()
		q := newQueue(t, d, queue.Handle(func(ctx context.Context, j sendInvoice) error {
			if j.OrderID == 1 {
				<-ctx.Done() // runs past the timeout

				return ctx.Err()
			}

			panic("bad invoice")
		}, queue.Retries(0), queue.Timeout(5*time.Second)))

		_ = q.Dispatch(t.Context(), sendInvoice{OrderID: 1})
		_ = q.Dispatch(t.Context(), sendInvoice{OrderID: 2})

		// A message whose kind is no longer registered fails at once.
		_ = d.Push(t.Context(), &queue.Message{ID: "x", Kind: "gone", Queue: "default", MaxRetries: 5})

		if err := q.Work(t.Context(), queue.WorkOptions{StopWhenEmpty: true}); err != nil {
			t.Fatal(err)
		}

		var errs []string
		for _, m := range d.Failed() {
			errs = append(errs, strings.SplitN(m.LastError, "\n", 2)[0])
		}

		// The pushed message has no AvailableAt, so it is the oldest.
		want := []string{`queue: no handler for job kind: "gone"`, "context deadline exceeded", "panic: bad invoice"}
		if diff := cmp.Diff(want, errs); diff != "" {
			t.Errorf("failures (-want +got):\n%s", diff)
		}
	})
}

func TestGracefulDrainAndConcurrency(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var running, peak, finished atomic.Int32

		d := queue.NewMemory()
		q := newQueue(t, d, queue.Handle(func(ctx context.Context, _ sendInvoice) error {
			n := running.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}

			time.Sleep(10 * time.Second) // keeps running after the worker is told to stop

			running.Add(-1)
			finished.Add(1)

			return ctx.Err()
		}))

		for i := range 6 {
			_ = q.Dispatch(t.Context(), sendInvoice{OrderID: int64(i)})
		}

		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error)

		go func() { done <- q.Work(ctx, queue.WorkOptions{Concurrency: 3}) }()

		time.Sleep(time.Second)
		cancel() // three jobs are in flight

		if err := <-done; err != nil {
			t.Fatal(err)
		}

		if finished.Load() != 3 || peak.Load() != 3 || d.Pending() != 3 {
			t.Fatalf("finished=%d peak=%d pending=%d, want 3/3/3", finished.Load(), peak.Load(), d.Pending())
		}
	})
}

func TestExponentialAndConstant(t *testing.T) {
	t.Parallel()

	exp := queue.Exponential(time.Second)
	got := []time.Duration{exp(1), exp(2), exp(3), exp(40), queue.Constant(time.Minute)(9)}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, time.Hour, time.Minute}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Error(diff)
	}
}

type ingestJob struct {
	N int `json:"n"`
}

func (ingestJob) Kind() string { return "ingest" }

func TestWorkQueueLimitsCapPerQueueConcurrency(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var heavy, heavyPeak, light, lightPeak atomic.Int32

		track := func(n *atomic.Int32, peak *atomic.Int32) {
			v := n.Add(1)
			for p := peak.Load(); v > p && !peak.CompareAndSwap(p, v); p = peak.Load() {
			}

			time.Sleep(10 * time.Second)
			n.Add(-1)
		}

		d := queue.NewMemory()
		q := newQueue(t, d,
			queue.Handle(func(context.Context, ingestJob) error { track(&heavy, &heavyPeak); return nil },
				queue.OnQueue("ingest")),
			queue.Handle(func(context.Context, sendInvoice) error { track(&light, &lightPeak); return nil }),
		)

		for i := range 6 {
			_ = q.Dispatch(t.Context(), ingestJob{N: i})
			_ = q.Dispatch(t.Context(), sendInvoice{OrderID: int64(i)})
		}

		err := q.Work(t.Context(), queue.WorkOptions{
			// ingest first: without the limit it would take every worker.
			Queues:        []string{"ingest", "default"},
			Concurrency:   6,
			Limits:        map[string]int{"ingest": 2},
			StopWhenEmpty: true,
		})
		if err != nil {
			t.Fatal(err)
		}

		if heavyPeak.Load() != 2 || lightPeak.Load() < 3 || d.Pending() != 0 {
			t.Fatalf("ingest peak=%d default peak=%d pending=%d, want 2/>=3/0",
				heavyPeak.Load(), lightPeak.Load(), d.Pending())
		}
	})
}

func TestWorkQueueLimitsRejectNegative(t *testing.T) {
	t.Parallel()

	q := newQueue(t, queue.NewMemory())

	err := q.Work(t.Context(), queue.WorkOptions{Limits: map[string]int{"default": -1}})
	if !errors.Is(err, queue.ErrInvalidLimit) {
		t.Fatalf("err = %v, want ErrInvalidLimit", err)
	}
}

func TestJitterStaysWithinTheFraction(t *testing.T) {
	t.Parallel()

	fn := queue.Jitter(queue.Constant(time.Minute), 0.25)
	lo, hi := 45*time.Second, 75*time.Second
	distinct := map[time.Duration]bool{}

	for range 200 {
		d := fn(1)
		if d < lo || d > hi {
			t.Fatalf("delay %v outside [%v, %v]", d, lo, hi)
		}

		distinct[d] = true
	}

	if len(distinct) < 20 {
		t.Fatalf("only %d distinct delays in 200 draws: no spread", len(distinct))
	}
}

func TestJitterClampsTheFraction(t *testing.T) {
	t.Parallel()

	if d := queue.Jitter(queue.Constant(time.Second), 0)(1); d != time.Second {
		t.Fatalf("zero fraction changed the delay: %v", d)
	}

	for range 100 {
		if d := queue.Jitter(queue.Constant(time.Second), 7)(1); d < 0 || d > 2*time.Second {
			t.Fatalf("fraction 7 not clamped to 1: %v", d)
		}
	}
}

func TestUniqueDispatchIsIdempotentWhileTheJobExists(t *testing.T) {
	t.Parallel()

	drivers := map[string]func(t *testing.T) queue.Driver{
		"memory": func(*testing.T) queue.Driver { return queue.NewMemory() },
		"database": func(t *testing.T) queue.Driver {
			t.Helper()

			return newDatabaseDriver(t, openSQLite(t), queue.DatabaseOptions{})
		},
	}

	for name, mk := range drivers {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := mk(t)
			ran := 0
			q := newQueue(t, d, queue.Handle(func(context.Context, sendInvoice) error { ran++; return nil }))

			for range 3 {
				if err := q.Dispatch(t.Context(), sendInvoice{OrderID: 1}, queue.Unique("order-1")); err != nil {
					t.Fatalf("dispatch: %v", err)
				}
			}

			// A different key and a non-unique dispatch are not deduplicated.
			if err := q.Dispatch(t.Context(), sendInvoice{OrderID: 2}, queue.Unique("order-2")); err != nil {
				t.Fatal(err)
			}

			if err := q.Dispatch(t.Context(), sendInvoice{OrderID: 3}); err != nil {
				t.Fatal(err)
			}

			for {
				m, err := d.Pop(t.Context(), []string{"default"})
				if err != nil {
					t.Fatal(err)
				}

				if m == nil {
					break
				}

				if err := q.Process(t.Context(), m); err != nil {
					t.Fatal(err)
				}
			}

			if ran != 3 {
				t.Fatalf("handler ran %d times, want 3 (unique x1, other key, plain)", ran)
			}

			// Once the job has finished, the key is free again.
			if err := q.Dispatch(t.Context(), sendInvoice{OrderID: 1}, queue.Unique("order-1")); err != nil {
				t.Fatal(err)
			}

			m, err := d.Pop(t.Context(), []string{"default"})
			if err != nil || m == nil {
				t.Fatalf("key not free after completion: %v, %v", m, err)
			}
		})
	}
}

func TestUniqueDoesNotHideARunningJob(t *testing.T) {
	t.Parallel()

	d := queue.NewMemory()
	q := newQueue(t, d, queue.Handle(func(context.Context, sendInvoice) error { return nil }))

	if err := q.Dispatch(t.Context(), sendInvoice{}, queue.Unique("k")); err != nil {
		t.Fatal(err)
	}

	if m, err := d.Pop(t.Context(), []string{"default"}); err != nil || m == nil {
		t.Fatalf("pop: %v, %v", m, err)
	}

	// Reserved (running) and not yet acked: still counted as existing.
	if err := q.Dispatch(t.Context(), sendInvoice{}, queue.Unique("k")); err != nil {
		t.Fatal(err)
	}

	if m, _ := d.Pop(t.Context(), []string{"default"}); m != nil {
		t.Fatalf("a second copy was stored while the first was running: %+v", m)
	}
}

// A unique job keeps its id, so failing twice and retrying must not clash with the failed table or a live copy.
func TestUniqueFailedJobsDoNotClashOnRetry(t *testing.T) {
	t.Parallel()

	d := newDatabaseDriver(t, openSQLite(t), queue.DatabaseOptions{})
	q := newQueue(t, d, queue.Handle(func(context.Context, sendInvoice) error {
		return fmt.Errorf("boom: %w", queue.ErrPermanent)
	}))

	failOnce := func() {
		t.Helper()

		if err := q.Dispatch(t.Context(), sendInvoice{OrderID: 1}, queue.Unique("k")); err != nil {
			t.Fatal(err)
		}

		m, err := d.Pop(t.Context(), []string{"default"})
		if err != nil || m == nil {
			t.Fatalf("pop: %v, %v", m, err)
		}

		_ = q.Process(t.Context(), m)
	}

	failOnce()
	failOnce() // the key is free after a failure; the second failure replaces the first row

	failed, err := d.Failed(t.Context())
	if err != nil || len(failed) != 1 {
		t.Fatalf("failed = %d, %v, want one row", len(failed), err)
	}

	// A live copy exists while the failed one is retried: the batch succeeds and the live copy wins.
	if err := q.Dispatch(t.Context(), sendInvoice{OrderID: 1}, queue.Unique("k")); err != nil {
		t.Fatal(err)
	}

	n, err := d.Retry(t.Context())
	if err != nil || n != 0 {
		t.Fatalf("Retry = %d, %v, want 0 (superseded by the live copy) and no error", n, err)
	}

	if rest, _ := d.Failed(t.Context()); len(rest) != 0 {
		t.Fatalf("failed rows left: %d", len(rest))
	}
}
