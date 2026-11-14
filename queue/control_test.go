package queue_test

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/queue"
)

type exportVideo struct{ ID int64 }

func (exportVideo) Kind() string { return "export_video" }

// recorder is an Observer that also implements ProgressObserver.
type recorder struct {
	ticks    chan struct{}
	mu       sync.Mutex
	events   []queue.JobEvent
	progress []queue.JobProgress
}

func newRecorder() *recorder { return &recorder{ticks: make(chan struct{}, 64)} }

func (r *recorder) ObserveJob(_ context.Context, e queue.JobEvent) {
	r.mu.Lock()
	r.events = append(r.events, e)
	r.mu.Unlock()

	select {
	case r.ticks <- struct{}{}:
	default:
	}
}

func (r *recorder) ObserveProgress(_ context.Context, p queue.JobProgress) {
	r.mu.Lock()
	r.progress = append(r.progress, p)
	r.mu.Unlock()
}

func (r *recorder) snapshot() ([]queue.JobEvent, []queue.JobProgress) {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]queue.JobEvent(nil), r.events...), append([]queue.JobProgress(nil), r.progress...)
}

func TestProgressReachesHookAndObserver(t *testing.T) {
	t.Parallel()

	var (
		mu   sync.Mutex
		hook []queue.JobProgress
	)

	rec := newRecorder()
	q := queue.New(queue.Sync{}, slog.New(slog.DiscardHandler))
	q.AddObserver(rec)

	err := q.Register(queue.Handle(func(ctx context.Context, _ exportVideo) error {
		queue.Progress(ctx, 0.25, "decoding")
		queue.Progress(ctx, 7, "clamped high")
		queue.Progress(ctx, -1, "clamped low")
		queue.Progress(ctx, math.NaN(), "ignored")

		return nil
	}, queue.WithProgress(func(_ context.Context, p queue.JobProgress) {
		mu.Lock()
		hook = append(hook, p)
		mu.Unlock()
	})))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := q.DispatchID(t.Context(), exportVideo{ID: 1}); err != nil {
		t.Fatal(err)
	}

	events, observed := rec.snapshot()
	if len(events) == 0 || events[0].ID == "" {
		t.Fatalf("events = %+v, want a dispatched event with an id", events)
	}

	want := []queue.JobProgress{
		{ID: events[0].ID, Kind: "export_video", Queue: "default", Attempt: 1, Fraction: 0.25, Message: "decoding"},
		{ID: events[0].ID, Kind: "export_video", Queue: "default", Attempt: 1, Fraction: 1, Message: "clamped high"},
		{ID: events[0].ID, Kind: "export_video", Queue: "default", Attempt: 1, Fraction: 0, Message: "clamped low"},
	}

	mu.Lock()
	defer mu.Unlock()

	if diff := cmp.Diff(want, hook); diff != "" {
		t.Errorf("hook progress mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(want, observed); diff != "" {
		t.Errorf("observer progress mismatch (-want +got):\n%s", diff)
	}
}

func TestProgressOutsideQueueIsNoop(t *testing.T) {
	t.Parallel()

	queue.Progress(t.Context(), 0.5, "no queue")
}

func TestProgressPanickingHookDoesNotFailTheJob(t *testing.T) {
	t.Parallel()

	q := queue.New(queue.Sync{}, slog.New(slog.DiscardHandler))

	err := q.Register(queue.Handle(func(ctx context.Context, _ exportVideo) error {
		queue.Progress(ctx, 0.5, "half")
		return nil
	}, queue.WithProgress(func(context.Context, queue.JobProgress) { panic("boom") })))
	if err != nil {
		t.Fatal(err)
	}

	if err := q.Dispatch(t.Context(), exportVideo{}); err != nil {
		t.Fatalf("Dispatch() = %v, want nil", err)
	}
}

func TestDispatchIDReturnsTheStoredID(t *testing.T) {
	t.Parallel()

	mem := queue.NewMemory()
	q := queue.New(mem, slog.New(slog.DiscardHandler))

	if err := q.Register(queue.Handle(func(context.Context, exportVideo) error { return nil })); err != nil {
		t.Fatal(err)
	}

	id, err := q.DispatchID(t.Context(), exportVideo{ID: 1})
	if err != nil {
		t.Fatal(err)
	}

	m, err := mem.Pop(t.Context(), []string{"default"})
	if err != nil || m == nil {
		t.Fatalf("Pop() = %v, %v", m, err)
	}

	if m.ID != id || id == "" {
		t.Errorf("DispatchID() = %q, stored id %q", id, m.ID)
	}

	uid, err := q.DispatchID(t.Context(), exportVideo{ID: 2}, queue.Unique("k"))
	if err != nil {
		t.Fatal(err)
	}

	again, err := q.DispatchID(t.Context(), exportVideo{ID: 2}, queue.Unique("k"))
	if err != nil {
		t.Fatal(err)
	}

	if uid != again {
		t.Errorf("unique ids differ: %q vs %q", uid, again)
	}

	other := queue.New(queue.Sync{}, slog.New(slog.DiscardHandler))
	if id, err := other.DispatchID(t.Context(), exportVideo{}); !errors.Is(err, queue.ErrUnknownKind) || id != "" {
		t.Errorf("unknown kind: id %q err %v", id, err)
	}
}

func TestCancelStopsARunningJobWithoutRetry(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	gotCause := make(chan error, 1)
	rec := newRecorder()

	q := queue.New(queue.NewMemory(), slog.New(slog.DiscardHandler))
	q.AddObserver(rec)

	err := q.Register(queue.Handle(func(ctx context.Context, _ exportVideo) error {
		close(started)
		<-ctx.Done()
		gotCause <- context.Cause(ctx)

		return ctx.Err()
	}, queue.Retries(5), queue.Timeout(time.Minute)))
	if err != nil {
		t.Fatal(err)
	}

	id, err := q.DispatchID(t.Context(), exportVideo{ID: 1})
	if err != nil {
		t.Fatal(err)
	}

	workCtx, stop := context.WithCancel(t.Context())
	defer stop()

	done := make(chan error, 1)

	go func() {
		done <- q.Work(workCtx, queue.WorkOptions{Sleep: time.Millisecond})
	}()

	<-started

	if err := q.Cancel(id); err != nil {
		t.Fatalf("Cancel() = %v", err)
	}

	select {
	case cause := <-gotCause:
		if !errors.Is(cause, queue.ErrCancelled) {
			t.Errorf("context cause = %v, want ErrCancelled", cause)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("handler context was not cancelled")
	}

	waitFor(t, rec, func() bool {
		events, _ := rec.snapshot()
		for _, e := range events {
			if e.Status == queue.JobFailed {
				return true
			}
		}

		return false
	})

	events, _ := rec.snapshot()

	var failed queue.JobEvent

	for _, e := range events {
		if e.Status == queue.JobFailed {
			failed = e
		}
	}

	if !failed.Final || !errors.Is(failed.Err, queue.ErrCancelled) || !errors.Is(failed.Err, queue.ErrPermanent) {
		t.Errorf("failed event = %+v, want Final with ErrCancelled and ErrPermanent", failed)
	}

	if err := q.Cancel(id); !errors.Is(err, queue.ErrNotRunning) {
		t.Errorf("second Cancel() = %v, want ErrNotRunning", err)
	}

	stop()

	if err := <-done; err != nil {
		t.Errorf("Work() = %v", err)
	}
}

func TestCancelUnknownJob(t *testing.T) {
	t.Parallel()

	q := queue.New(queue.NewMemory(), slog.New(slog.DiscardHandler))

	if err := q.Cancel("nope"); !errors.Is(err, queue.ErrNotRunning) {
		t.Errorf("Cancel() = %v, want ErrNotRunning", err)
	}
}

func TestCancelIgnoredByHandlerStillCompletes(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	release := make(chan struct{})
	rec := newRecorder()

	q := queue.New(queue.NewMemory(), slog.New(slog.DiscardHandler))
	q.AddObserver(rec)

	err := q.Register(queue.Handle(func(context.Context, exportVideo) error {
		close(started)
		<-release

		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}

	id, err := q.DispatchID(t.Context(), exportVideo{})
	if err != nil {
		t.Fatal(err)
	}

	workCtx, stop := context.WithCancel(t.Context())
	defer stop()

	done := make(chan error, 1)

	go func() { done <- q.Work(workCtx, queue.WorkOptions{Sleep: time.Millisecond}) }()

	<-started

	if err := q.Cancel(id); err != nil {
		t.Fatal(err)
	}

	close(release)

	waitFor(t, rec, func() bool {
		events, _ := rec.snapshot()
		for _, e := range events {
			if e.Status == queue.JobProcessed {
				return true
			}
		}

		return false
	})

	stop()
	<-done
}

// waitFor blocks until cond holds, re-checking on every observed event.
func waitFor(t *testing.T, rec *recorder, cond func() bool) {
	t.Helper()

	timeout := time.After(10 * time.Second)

	for !cond() {
		select {
		case <-rec.ticks:
		case <-timeout:
			t.Fatal("condition not met in time")
		}
	}
}
