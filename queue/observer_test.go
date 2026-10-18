package queue_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/queue"
)

type jobLog struct {
	mu  sync.Mutex
	got []string
}

func (l *jobLog) ObserveJob(_ context.Context, e queue.JobEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.got = append(l.got, e.Kind+":"+string(e.Status))
}

func TestObserverSeesDispatchAndAttempts(t *testing.T) {
	t.Parallel()

	fail := errors.New("boom")
	handler := queue.Handle(func(_ context.Context, j sendInvoice) error {
		if j.OrderID == 2 {
			return fail
		}

		return nil
	}, queue.Retries(0))

	t.Run("sync", func(t *testing.T) {
		t.Parallel()

		obs := &jobLog{}
		q := newQueue(t, queue.Sync{}, handler)
		q.SetObserver(obs)

		_ = q.Dispatch(t.Context(), sendInvoice{OrderID: 1})
		_ = q.Dispatch(t.Context(), sendInvoice{OrderID: 2})

		want := []string{
			"send_invoice:dispatched", "send_invoice:processed",
			"send_invoice:dispatched", "send_invoice:failed",
		}
		if diff := cmp.Diff(want, obs.got); diff != "" {
			t.Errorf("events (-want +got):\n%s", diff)
		}
	})

	t.Run("worker", func(t *testing.T) {
		t.Parallel()

		obs := &jobLog{}
		d := queue.NewMemory()
		q := newQueue(t, d, handler)
		q.SetObserver(obs)

		if err := q.Dispatch(t.Context(), sendInvoice{OrderID: 1}); err != nil {
			t.Fatal(err)
		}

		m, err := d.Pop(t.Context(), []string{"default"})
		if err != nil || m == nil {
			t.Fatalf("Pop() = %v, %v", m, err)
		}

		if err := q.Process(t.Context(), m); err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff([]string{"send_invoice:dispatched", "send_invoice:processed"}, obs.got); diff != "" {
			t.Errorf("events (-want +got):\n%s", diff)
		}
	})
}

// jobEvents records the events themselves, not only their status.
type jobEvents struct {
	mu  sync.Mutex
	got []queue.JobEvent
}

func (l *jobEvents) ObserveJob(_ context.Context, e queue.JobEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.got = append(l.got, e)
}

// events returns "<attempt>/<max retries> final=<final>" per failed
// attempt, so a dashboard observer can be checked for what it needs to
// tell a retry from a dead job.
func (l *jobEvents) failures() []string {
	l.mu.Lock()
	defer l.mu.Unlock()

	var out []string

	for _, e := range l.got {
		if e.Status != queue.JobFailed {
			continue
		}

		out = append(out, fmt.Sprintf("%d/%d final=%v", e.Attempt, e.MaxRetries, e.Final))
	}

	return out
}

// An Observer sees the retry limit and whether the failure was the job's
// last, which JobFailed alone cannot say.
func TestObserverSeesRetryLimitAndFinalFailure(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err   error
		opts  []queue.Option
		runs  int
		want  []string
		rerun bool
	}{
		"retries run out": {
			err:  errors.New("try again"),
			opts: []queue.Option{queue.Retries(1), queue.Backoff(queue.Constant(time.Minute))},
			runs: 2,
			want: []string{"1/1 final=false", "2/1 final=true"},
		},
		"permanent on the first attempt": {
			err:  fmt.Errorf("corrupt input: %w", queue.ErrPermanent),
			opts: []queue.Option{queue.Retries(3), queue.Backoff(queue.Constant(time.Minute))},
			runs: 2,
			want: []string{"1/3 final=true"},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			clock := &fakeClock{now: time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)}
			obs := &jobEvents{}

			d := queue.NewMemory()
			d.SetClock(clock.Now)

			q := newQueue(t, d, queue.Handle(func(context.Context, sendInvoice) error { return tc.err }, tc.opts...))
			q.SetClock(clock.Now)
			q.SetObserver(obs)

			if err := q.Dispatch(t.Context(), sendInvoice{OrderID: 1}); err != nil {
				t.Fatal(err)
			}

			for range tc.runs {
				if err := q.Work(t.Context(), queue.WorkOptions{StopWhenEmpty: true}); err != nil {
					t.Fatal(err)
				}

				clock.Advance(time.Minute)
			}

			if diff := cmp.Diff(tc.want, obs.failures()); diff != "" {
				t.Errorf("failure events (-want +got):\n%s", diff)
			}
		})
	}
}

type panicJobObserver struct{}

func (panicJobObserver) ObserveJob(context.Context, queue.JobEvent) { panic("observer bug") }

func TestObserverPanicIsContained(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer

	q := queue.New(queue.Sync{}, slog.New(slog.NewTextHandler(&logs, nil)))
	if err := q.Register(queue.Handle(func(context.Context, sendInvoice) error { return nil })); err != nil {
		t.Fatal(err)
	}

	var typedNil *jobLog

	obs := &jobLog{}
	q.SetObserver(panicJobObserver{})
	q.AddObserver(typedNil) // skipped: its method would panic
	q.AddObserver(obs)

	if err := q.Dispatch(t.Context(), sendInvoice{OrderID: 4242}); err != nil {
		t.Fatalf("Dispatch() with a panicking observer = %v, want nil", err)
	}

	if diff := cmp.Diff([]string{"send_invoice:dispatched", "send_invoice:processed"}, obs.got); diff != "" {
		t.Errorf("observer after the panicking one (-want +got):\n%s", diff)
	}

	out := logs.String()
	if !strings.Contains(out, "job observer panicked") || !strings.Contains(out, "queue_test.panicJobObserver") {
		t.Errorf("log = %q, want the observer type", out)
	}

	if strings.Contains(out, "4242") {
		t.Errorf("log = %q, leaks the payload", out)
	}

	q.SetObserver(nil)

	if err := q.Dispatch(t.Context(), sendInvoice{OrderID: 1}); err != nil || len(obs.got) != 2 {
		t.Fatalf("SetObserver(nil) kept observers: err %v, events %v", err, obs.got)
	}
}
