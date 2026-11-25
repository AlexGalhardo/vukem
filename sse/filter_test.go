package sse_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/sse"
)

func TestHubSubscribeFuncFiltersPerSubscriber(t *testing.T) {
	t.Parallel()

	for name, broker := range map[string]sse.Broker{"memory": nil, "broker": &memBroker{}} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			hub := sse.NewHub(broker)
			t.Cleanup(hub.Close)

			owner := func(who string) func(sse.Event) bool {
				return func(ev sse.Event) bool { return ev.ID == who }
			}

			ana, stopAna, err := hub.SubscribeFunc(t.Context(), owner("ana"), "inbox")
			if err != nil {
				t.Fatal(err)
			}
			defer stopAna()

			bob, stopBob, err := hub.SubscribeFunc(t.Context(), owner("bob"), "inbox")
			if err != nil {
				t.Fatal(err)
			}
			defer stopBob()

			for i, who := range []string{"ana", "bob", "ana"} {
				ev := sse.Event{ID: who, Data: string(rune('1' + i))}
				if err := hub.Publish(t.Context(), "inbox", ev); err != nil {
					t.Fatal(err)
				}
			}

			if diff := cmp.Diff([]string{"1", "3"}, take(t, ana, 2)); diff != "" {
				t.Errorf("ana's events (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff([]string{"2"}, take(t, bob, 1)); diff != "" {
				t.Errorf("bob's events (-want +got):\n%s", diff)
			}

			stopAna()

			if _, open := <-ana; open {
				t.Error("channel still open after unsubscribe")
			}

			if got := hub.Subscribers("inbox"); got != 1 {
				t.Errorf("Subscribers(inbox) = %d after one unsubscribe, want 1", got)
			}
		})
	}
}

func TestHubSubscribeFuncNilKeepsEverything(t *testing.T) {
	t.Parallel()

	hub := sse.NewHub(nil)
	t.Cleanup(hub.Close)

	ch, stop, err := hub.SubscribeFunc(t.Context(), nil, "posts")
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	if err := hub.Publish(t.Context(), "posts", sse.Event{Data: "1"}); err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff([]string{"1"}, take(t, ch, 1)); diff != "" {
		t.Errorf("events (-want +got):\n%s", diff)
	}
}

// A stopped subscription whose consumer no longer reads must not leave the
// filtering goroutine blocked on the send.
func TestHubSubscribeFuncStopUnblocks(t *testing.T) {
	t.Parallel()

	hub := sse.NewHub(nil)
	t.Cleanup(hub.Close)

	ch, stop, err := hub.SubscribeFunc(t.Context(), func(sse.Event) bool { return true }, "posts")
	if err != nil {
		t.Fatal(err)
	}

	for range 3 {
		if err := hub.Publish(t.Context(), "posts", sse.Event{Data: "x"}); err != nil {
			t.Fatal(err)
		}
	}

	stop()
	stop() // idempotent

	deadline := time.After(5 * time.Second)

	for {
		select {
		case _, open := <-ch:
			if !open {
				return
			}
		case <-deadline:
			t.Fatal("channel not closed after stop")
		}
	}
}

func take(t *testing.T, ch <-chan sse.Event, n int) []string {
	t.Helper()

	var got []string

	for range n {
		select {
		case ev := <-ch:
			got = append(got, ev.Data)
		case <-time.After(5 * time.Second):
			t.Fatalf("got %v, want %d events", got, n)
		}
	}

	select {
	case ev, open := <-ch:
		if open {
			t.Errorf("extra event %+v", ev)
		}
	case <-time.After(50 * time.Millisecond):
	}

	return got
}

func TestHubSubscribeFuncSurvivesAPanickingFilter(t *testing.T) {
	t.Parallel()

	hub := sse.NewHub(nil)
	t.Cleanup(hub.Close)

	ch, stop, err := hub.SubscribeFunc(t.Context(), func(sse.Event) bool { panic("store down") }, "posts")
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	if err := hub.Publish(t.Context(), "posts", sse.Event{Data: "1"}); err != nil {
		t.Fatal(err)
	}

	select {
	case _, open := <-ch:
		if open {
			t.Error("got an event from a filter that panicked")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("channel not closed after the filter panicked")
	}

	if got := hub.Subscribers("posts"); got != 0 {
		t.Errorf("Subscribers(posts) = %d after the panic, want 0", got)
	}
}

// A subscriber that only cancels its context, never calling stop, must not
// leave the filtering goroutine blocked on a send nobody reads.
func TestHubSubscribeFuncEndsWithItsContext(t *testing.T) {
	t.Parallel()

	hub := sse.NewHub(nil)
	t.Cleanup(hub.Close)

	ctx, cancel := context.WithCancel(t.Context())

	ch, _, err := hub.SubscribeFunc(ctx, func(sse.Event) bool { return true }, "posts")
	if err != nil {
		t.Fatal(err)
	}

	for range 3 {
		if err := hub.Publish(t.Context(), "posts", sse.Event{Data: "x"}); err != nil {
			t.Fatal(err)
		}
	}

	cancel()

	deadline := time.After(5 * time.Second)

	for {
		select {
		case _, open := <-ch:
			if !open {
				return
			}
		case <-deadline:
			t.Fatal("channel not closed after the context ended")
		}
	}
}
