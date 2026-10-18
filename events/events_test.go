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

func sendWelcome(context.Context, userRegistered) error { return errors.New("smtp down") }

type mailer struct{}

func (*mailer) send(context.Context, userRegistered) error { return errors.New("smtp down") }

func TestListenerNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		reg  Registration
		want string
	}{
		{name: "function", reg: On(sendWelcome), want: "events.sendWelcome"},
		{name: "method value", reg: On((&mailer{}).send), want: "events.(*mailer).send"},
		{
			name: "closure",
			reg:  On(func(context.Context, userRegistered) error { return errors.New("smtp down") }),
			want: "events.TestListenerNames.func1",
		},
		{name: "queued", reg: OnQueued("mail.welcome", sendWelcome), want: "mail.welcome"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.reg.l.name; got != tt.want {
				t.Errorf("listener name = %q, want %q", got, tt.want)
			}
		})
	}
}

// A panicking synchronous listener must not crash a worker or a command:
// Dispatch recovers it, logs it and returns it as an error.
func TestSyncListenerPanicIsRecovered(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	b := New(slog.New(slog.NewTextHandler(&buf, nil)), 0)
	Listen(b, func(context.Context, userRegistered) error { panic("nil map") })

	var ran bool

	Listen(b, func(context.Context, userRegistered) error { ran = true; return nil })

	err := b.Dispatch(t.Context(), userRegistered{})
	if err == nil || !strings.Contains(err.Error(), "panic: nil map") {
		t.Fatalf("Dispatch() = %v, want the recovered panic", err)
	}

	if ran {
		t.Error("Dispatch() ran the listener after the panicking one")
	}

	if log := buf.String(); !strings.Contains(log, "listener panicked") || !strings.Contains(log, "nil map") {
		t.Errorf("log = %q, want the panic", log)
	}
}
