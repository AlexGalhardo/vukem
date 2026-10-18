package events

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type userRegistered struct{ Email string }

type orderPaid struct{ ID int }

func TestDispatchOrderAndErrors(t *testing.T) {
	t.Parallel()

	b := New(nil, 0)

	var (
		mu    sync.Mutex
		calls []string
	)

	record := func(s string) {
		mu.Lock()
		defer mu.Unlock()

		calls = append(calls, s)
	}

	Listen(b, func(_ context.Context, e userRegistered) error { record("first:" + e.Email); return nil })
	b.Register(On(func(_ context.Context, e userRegistered) error { record("second:" + e.Email); return nil }))

	if err := b.Dispatch(t.Context(), userRegistered{Email: "a@b.co"}); err != nil {
		t.Fatal(err)
	}

	if strings.Join(calls, ",") != "first:a@b.co,second:a@b.co" {
		t.Fatalf("calls = %v", calls)
	}

	// No listeners: fine.
	if err := b.Dispatch(t.Context(), orderPaid{ID: 1}); err != nil {
		t.Fatal(err)
	}

	// The first failing sync listener stops the chain.
	boom := errors.New("boom")
	Listen(b, func(context.Context, orderPaid) error { return boom })
	Listen(b, func(context.Context, orderPaid) error { record("never"); return nil })

	if err := b.Dispatch(t.Context(), orderPaid{ID: 2}); !errors.Is(err, boom) || slices.Contains(calls, "never") {
		t.Fatalf("err = %v calls = %v", err, calls)
	}

	if err := wrap(func(context.Context, orderPaid) error { return nil })(t.Context(), "wrong"); err == nil {
		t.Fatal("wrong event type accepted")
	}
}

func TestAsyncListenersAndShutdown(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer

	b := New(slog.New(slog.NewTextHandler(&logs, nil)), 2)

	var done atomic.Int32

	release := make(chan struct{})

	ListenAsync(b, func(ctx context.Context, _ orderPaid) error {
		<-release
		done.Add(1)

		return ctx.Err() // the request context was cancelled, the listener's was not
	})
	ListenAsync(b, func(context.Context, orderPaid) error { return errors.New("mail down") })
	ListenAsync(b, func(context.Context, userRegistered) error { panic("bad listener") })

	ctx, cancel := context.WithCancel(t.Context())
	for i := range 3 {
		if err := b.Dispatch(ctx, orderPaid{ID: i}); err != nil {
			t.Fatal(err)
		}
	}

	if err := b.Dispatch(ctx, userRegistered{}); err != nil {
		t.Fatal(err)
	}

	cancel()

	// Shutdown waits for the async work, bounded by its context.
	short, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stop()

	if err := b.Shutdown(short); err == nil {
		t.Fatal("Shutdown returned while listeners were blocked")
	}

	close(release)

	if err := b.Shutdown(context.Background()); err != nil || done.Load() != 3 {
		t.Fatalf("Shutdown = %v done = %d", err, done.Load())
	}

	if !strings.Contains(logs.String(), "mail down") || !strings.Contains(logs.String(), "bad listener") {
		t.Fatalf("logs = %q", logs.String())
	}

	if err := b.Dispatch(t.Context(), orderPaid{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("after shutdown = %v", err)
	}
}

func TestFake(t *testing.T) {
	t.Parallel()

	var d Dispatcher = NewFake()

	_ = d.Dispatch(t.Context(), userRegistered{Email: "x@y.z"})
	_ = d.Dispatch(t.Context(), orderPaid{ID: 7})

	f, _ := d.(*Fake)
	if got := Dispatched[userRegistered](f); len(got) != 1 || got[0].Email != "x@y.z" {
		t.Fatalf("Dispatched = %v", got)
	}

	if len(Dispatched[orderPaid](f)) != 1 || len(Dispatched[string](f)) != 0 {
		t.Fatal("Dispatched by type")
	}
}

func TestBusRecordTo(t *testing.T) {
	t.Parallel()

	b := New(nil, 0)

	var ran int

	b.Register(
		On(func(context.Context, userRegistered) error { ran++; return nil }),
		OnAsync(func(context.Context, userRegistered) error { ran++; return nil }),
	)

	f := NewFake()
	b.RecordTo(f)

	if err := b.Dispatch(t.Context(), userRegistered{Email: "x@y.z"}); err != nil {
		t.Fatalf("Dispatch() = %v, want nil", err)
	}

	if err := b.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}

	if ran != 0 {
		t.Errorf("listeners ran %d times while recording, want 0", ran)
	}

	if got := Dispatched[userRegistered](f); len(got) != 1 || got[0].Email != "x@y.z" {
		t.Fatalf("Dispatched = %v, want the dispatched event", got)
	}

	b2 := New(nil, 0)
	b2.Register(On(func(context.Context, userRegistered) error { ran++; return nil }))
	b2.RecordTo(f)
	b2.RecordTo(nil)

	_ = b2.Dispatch(t.Context(), userRegistered{})

	if ran != 1 {
		t.Errorf("listener after RecordTo(nil) ran %d times, want 1", ran)
	}
}
