package queue_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/queue"
)

// faultyDriver wraps Memory and injects errors into chosen operations.
type faultyDriver struct {
	*queue.Memory

	mu      sync.Mutex
	popErrs int // the next popErrs Pop calls fail
	ackErr  error
}

func (f *faultyDriver) Pop(ctx context.Context, queues []string) (*queue.Message, error) {
	f.mu.Lock()
	fail := f.popErrs > 0
	if fail {
		f.popErrs--
	}
	f.mu.Unlock()

	if fail {
		return nil, errors.New("connection reset")
	}

	return f.Memory.Pop(ctx, queues)
}

func (f *faultyDriver) Ack(ctx context.Context, m *queue.Message) error {
	if f.ackErr != nil {
		return f.ackErr
	}

	return f.Memory.Ack(ctx, m)
}

func TestWorkReturnsFirstSettleError(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer

	ackErr := errors.New("ack lost")
	d := &faultyDriver{Memory: queue.NewMemory(), ackErr: ackErr}
	q := queue.New(d, slog.New(slog.NewTextHandler(&logs, nil)))

	if err := q.Register(queue.Handle(func(context.Context, sendInvoice) error { return nil })); err != nil {
		t.Fatal(err)
	}

	for i := range 2 {
		if err := q.Dispatch(t.Context(), sendInvoice{OrderID: int64(i)}); err != nil {
			t.Fatal(err)
		}
	}

	// Ack keeps failing, so the same jobs come back; MaxJobs bounds the run.
	err := q.Work(t.Context(), queue.WorkOptions{MaxJobs: 2, StopWhenEmpty: true})
	if !errors.Is(err, ackErr) {
		t.Fatalf("Work() = %v, want %v", err, ackErr)
	}

	if !strings.Contains(logs.String(), "queue: settle job") {
		t.Errorf("logs = %q, want a settle error", logs.String())
	}
}

func TestWorkLogsReserveErrorsAndKeepsPolling(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var (
			logs bytes.Buffer
			ran  []int64
		)

		d := &faultyDriver{Memory: queue.NewMemory(), popErrs: 2}
		q := queue.New(d, slog.New(slog.NewTextHandler(&logs, nil)))

		if err := q.Register(queue.Handle(func(_ context.Context, j sendInvoice) error {
			ran = append(ran, j.OrderID)
			return nil
		})); err != nil {
			t.Fatal(err)
		}

		if err := q.Dispatch(t.Context(), sendInvoice{OrderID: 9}); err != nil {
			t.Fatal(err)
		}

		if err := q.Work(t.Context(), queue.WorkOptions{MaxJobs: 1, Sleep: time.Second}); err != nil {
			t.Fatalf("Work() = %v, want nil", err)
		}

		if diff := cmp.Diff([]int64{9}, ran); diff != "" {
			t.Errorf("jobs run (-want +got):\n%s", diff)
		}

		if n := strings.Count(logs.String(), "queue: reserve job"); n != 2 {
			t.Errorf("reserve errors logged = %d, want 2; logs:\n%s", n, logs.String())
		}
	})
}

func TestWorkReserveErrorAfterCancelIsSilent(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer

	ctx, cancel := context.WithCancel(t.Context())
	d := &cancelOnPop{cancel: cancel}
	q := queue.New(d, slog.New(slog.NewTextHandler(&logs, nil)))

	if err := q.Work(ctx, queue.WorkOptions{}); err != nil {
		t.Fatalf("Work() = %v, want nil", err)
	}

	if logs.Len() != 0 {
		t.Errorf("logs = %q, want nothing logged for a reserve error caused by shutdown", logs.String())
	}
}

// cancelOnPop cancels the worker context and fails the reservation, like
// a driver whose query was interrupted by shutdown.
type cancelOnPop struct {
	queue.Sync

	cancel context.CancelFunc
}

func (c *cancelOnPop) Pop(context.Context, []string) (*queue.Message, error) {
	c.cancel()
	return nil, context.Canceled
}

func TestWorkMaxJobsReleasesExtraReservation(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		d := queue.NewMemory()
		started := make(chan struct{})
		release := make(chan struct{})

		var once sync.Once

		// Whichever worker takes the slot blocks; the other one is over the limit.
		q := newQueue(t, d, queue.Handle(func(context.Context, sendInvoice) error {
			once.Do(func() { close(started) })
			<-release

			return nil
		}))

		for i := range 3 {
			_ = q.Dispatch(t.Context(), sendInvoice{OrderID: int64(i + 1)})
		}

		done := make(chan error)

		go func() { done <- q.Work(t.Context(), queue.WorkOptions{Concurrency: 2, MaxJobs: 1}) }()

		<-started
		synctest.Wait() // the second worker reserved a job over the limit and gave it back
		close(release)

		if err := <-done; err != nil {
			t.Fatal(err)
		}

		if n := d.Pending(); n != 2 {
			t.Errorf("Pending() = %d, want 2 (the extra reservation released)", n)
		}

		m, err := d.Pop(t.Context(), []string{"default"})
		if err != nil || m == nil || m.Attempts != 0 {
			t.Errorf("released job = %+v, %v, want 0 attempts", m, err)
		}
	})
}

func TestMissingTimeoutUsesHandlerTimeout(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var left time.Duration

		d := queue.NewMemory()
		q := newQueue(t, d, queue.Handle(func(ctx context.Context, _ sendInvoice) error {
			deadline, ok := ctx.Deadline()
			if !ok {
				return errors.New("no deadline")
			}

			left = time.Until(deadline)

			return nil
		}, queue.Timeout(7*time.Second)))

		// Stored by an older release without a timeout.
		_ = d.Push(
			t.Context(),
			&queue.Message{ID: "old", Kind: "send_invoice", Queue: "default", Payload: []byte(`{}`)},
		)

		if err := q.Work(t.Context(), queue.WorkOptions{StopWhenEmpty: true}); err != nil {
			t.Fatal(err)
		}

		if left != 7*time.Second {
			t.Errorf("handler deadline in %v, want 7s", left)
		}
	})
}

func TestRegistrationAndMessageErrors(t *testing.T) {
	t.Parallel()

	q := newQueue(t, queue.NewMemory(),
		queue.HandleKind("strict", func(context.Context, sendInvoice) error { return nil }, queue.Retries(0)))

	if err := q.Register(queue.HandleKind("", func(context.Context, struct{}) error { return nil })); err == nil {
		t.Error("Register(empty kind) = nil, want error")
	}

	if err := q.DispatchKind(t.Context(), "strict", make(chan int)); err == nil ||
		!strings.Contains(err.Error(), "queue: encode strict") {
		t.Errorf("DispatchKind(unencodable) = %v, want encode error", err)
	}

	m, err := q.Message(sendInvoice{OrderID: 3})
	if !errors.Is(err, queue.ErrUnknownKind) || m != nil {
		t.Errorf("Message(unregistered) = %v, %v, want ErrUnknownKind", m, err)
	}

	d := queue.NewMemory()
	q = newQueue(
		t,
		d,
		queue.HandleKind("strict", func(context.Context, sendInvoice) error { return nil }, queue.Retries(0)),
	)

	_ = d.Push(t.Context(), &queue.Message{ID: "bad", Kind: "strict", Queue: "default", Payload: []byte(`[1]`)})

	if err := q.Work(t.Context(), queue.WorkOptions{StopWhenEmpty: true}); err != nil {
		t.Fatal(err)
	}

	failed := d.Failed()
	if len(failed) != 1 || !strings.HasPrefix(failed[0].LastError, "decode strict payload:") {
		t.Errorf("failed = %+v, want one decode error", failed)
	}
}

func TestMessageBuildsStoredJob(t *testing.T) {
	t.Parallel()

	q := newQueue(t, queue.NewMemory(),
		queue.Handle(func(context.Context, sendInvoice) error { return nil }, queue.Timeout(time.Minute)))

	m, err := q.Message(sendInvoice{OrderID: 3}, queue.OnQueue("mail"), queue.Retries(4), queue.Delay(time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	if len(m.ID) != 32 || m.Kind != "send_invoice" || m.Queue != "mail" || m.MaxRetries != 4 ||
		m.Timeout != time.Minute || m.AvailableAt.Sub(m.CreatedAt) != time.Hour || string(m.Payload) != `{"order_id":3}` {
		t.Errorf("Message() = %+v", m)
	}
}

func TestSyncDriverStorageIsEmpty(t *testing.T) {
	t.Parallel()

	var s queue.Sync

	ctx := t.Context()
	m := &queue.Message{ID: "x"}

	if err := s.Push(ctx, m); err == nil {
		t.Error("Sync.Push() = nil, want error (no storage)")
	}

	if got, err := s.Pop(ctx, []string{"default"}); got != nil || err != nil {
		t.Errorf("Sync.Pop() = %v, %v, want nil, nil", got, err)
	}

	for name, fn := range map[string]func(context.Context, *queue.Message) error{
		"Ack": s.Ack, "Release": s.Release, "Fail": s.Fail,
	} {
		if err := fn(ctx, m); err != nil {
			t.Errorf("Sync.%s() = %v, want nil", name, err)
		}
	}
}

type otherJob struct {
	Name string `json:"name"`
}

func (otherJob) Kind() string { return "other" }

func TestFakeRecordsDispatchedJobs(t *testing.T) {
	t.Parallel()

	fake := queue.NewFake()

	var ran []int64

	q := newQueue(t, fake,
		queue.Handle(func(_ context.Context, j sendInvoice) error {
			ran = append(ran, j.OrderID)
			return nil
		}),
		queue.Handle(func(context.Context, otherJob) error { return nil }),
	)

	ctx := t.Context()
	_ = q.Dispatch(ctx, sendInvoice{OrderID: 1})
	_ = q.Dispatch(ctx, otherJob{Name: "x"})
	_ = q.Dispatch(ctx, sendInvoice{OrderID: 2})
	// A payload that does not decode into the job type is skipped.
	_ = fake.Push(ctx, &queue.Message{ID: "bad", Kind: "send_invoice", Queue: "default", Payload: []byte(`"x"`)})

	if diff := cmp.Diff([]sendInvoice{{OrderID: 1}, {OrderID: 2}}, queue.Dispatched[sendInvoice](fake)); diff != "" {
		t.Errorf("Dispatched[sendInvoice] (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff([]otherJob{{Name: "x"}}, queue.Dispatched[otherJob](fake)); diff != "" {
		t.Errorf("Dispatched[otherJob] (-want +got):\n%s", diff)
	}

	msgs := fake.Messages()
	msgs[0].Kind = "mutated"

	var kinds []string
	for _, m := range fake.Messages() {
		kinds = append(kinds, m.Kind)
	}

	if diff := cmp.Diff([]string{"send_invoice", "other", "send_invoice", "send_invoice"}, kinds); diff != "" {
		t.Errorf("Messages() kinds (-want +got), must be a copy:\n%s", diff)
	}

	// The fake stores jobs too, so Work runs them.
	if err := q.Work(ctx, queue.WorkOptions{StopWhenEmpty: true}); err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff([]int64{1, 2}, ran); diff != "" {
		t.Errorf("jobs run (-want +got):\n%s", diff)
	}
}

// A worker that stops when the queue is empty cannot tell "empty" from a
// store error, so it returns the error instead of exiting cleanly with
// jobs still queued (queue:work -once / -stop-when-empty exit non-zero).
func TestWorkStopWhenEmptyReturnsReserveErrors(t *testing.T) {
	t.Parallel()

	for name, o := range map[string]queue.WorkOptions{
		"stop when empty": {StopWhenEmpty: true, Concurrency: 2},
		"once":            {StopWhenEmpty: true, MaxJobs: 1},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := &faultyDriver{Memory: queue.NewMemory(), popErrs: 1}
			q := newQueue(t, d, queue.Handle(func(context.Context, sendInvoice) error { return nil }))

			if err := q.Dispatch(t.Context(), sendInvoice{OrderID: 1}); err != nil {
				t.Fatal(err)
			}

			if err := q.Work(t.Context(), o); err == nil || !strings.Contains(err.Error(), "connection reset") {
				t.Fatalf("Work() = %v, want the reserve error", err)
			}
		})
	}
}
