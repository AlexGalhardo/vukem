package schedule_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AlexGalhardo/vukem/cache"
	"github.com/AlexGalhardo/vukem/schedule"
)

// records is a slog.Handler that keeps every record, with the attributes of
// With, for assertions.
type records struct {
	mu    *sync.Mutex
	list  *[]slog.Record
	attrs []slog.Attr
}

func newRecords() records { return records{mu: &sync.Mutex{}, list: &[]slog.Record{}} }

func (records) Enabled(context.Context, slog.Level) bool { return true }

func (h records) Handle(_ context.Context, r slog.Record) error {
	r = r.Clone()
	r.AddAttrs(h.attrs...)

	h.mu.Lock()
	defer h.mu.Unlock()

	*h.list = append(*h.list, r)

	return nil
}

func (h records) WithAttrs(as []slog.Attr) slog.Handler {
	h.attrs = append(append([]slog.Attr(nil), h.attrs...), as...)
	return h
}

func (h records) WithGroup(string) slog.Handler { return h }

// find returns the attributes of the first record with msg.
func (h records) find(msg string) (map[string]slog.Value, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for i := range *h.list {
		r := &(*h.list)[i]
		if r.Message != msg {
			continue
		}

		out := map[string]slog.Value{}
		r.Attrs(func(a slog.Attr) bool { out[a.Key] = a.Value; return true })

		return out, true
	}

	return nil, false
}

// brokenStore fails every lock attempt (lock store down).
type brokenStore struct{ cache.Store }

func (brokenStore) Add(context.Context, string, []byte, time.Duration) (bool, error) {
	return false, errors.New("connection refused")
}

func (brokenStore) Shared() bool { return true }

// A lock store outage must not skip tasks silently: Work drops RunDue's
// result, so every failure has to reach the log.
func TestWorkLogsLockErrors(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		opt  func(*schedule.Task) *schedule.Task
	}{
		{name: "without overlapping", opt: func(t *schedule.Task) *schedule.Task { return t.WithoutOverlapping() }},
		{name: "on one server", opt: (*schedule.Task).OnOneServer},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				h := newRecords()
				s := schedule.New(schedule.Options{
					Log:   slog.New(h),
					Locks: cache.New(brokenStore{cache.NewMemory(0)}, ""),
				})
				tt.opt(s.Func("report", func(context.Context) error { return nil }).EveryMinute())

				ctx, cancel := context.WithCancel(t.Context())
				done := make(chan error)

				go func() { done <- s.Work(ctx, nil) }()

				time.Sleep(90 * time.Second)
				cancel()
				<-done

				attrs, ok := h.find("scheduled task failed")
				if !ok {
					t.Fatal(`Work() with a broken lock store logged no "scheduled task failed"`)
				}

				if attrs["task"].String() != "report" || attrs["err"].String() != "lock: connection refused" {
					t.Errorf("logged %v, want task report and err lock: connection refused", attrs)
				}
			})
		})
	}
}

// took is how long the task ran, not how late RunDue was called for its
// minute (schedule:run passes the minute's start).
func TestRunDueTookExcludesDelay(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		h := newRecords()
		s := schedule.New(schedule.Options{Log: slog.New(h)})
		s.Func("quick", func(context.Context) error { time.Sleep(2 * time.Second); return nil }).EveryMinute()

		if err := s.RunDue(t.Context(), time.Now().Add(-45*time.Second)); err != nil {
			t.Fatal(err)
		}

		attrs, ok := h.find("scheduled task done")
		if !ok {
			t.Fatal(`RunDue() logged no "scheduled task done"`)
		}

		if got := attrs["took"].Duration(); got != 2*time.Second {
			t.Errorf("took = %v, want 2s", got)
		}
	})
}
