// Package ssetest is the conformance suite every sse.Broker passes
// (*redis.Client in drivers/redis, *postgres.Broker in drivers/postgres). A
// new broker calls [Run] from one of its tests.
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/frontend.md.
package ssetest

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/sse"
)

// Options adapts the suite to a broker's documented limits.
type Options struct {
	// MaxPayload is the largest payload the broker accepts, in bytes.
	// Zero means the suite sends up to 64 KiB.
	MaxPayload int
	// Timeout bounds every wait for a message. Zero means 10 seconds.
	Timeout time.Duration
}

const (
	defaultTimeout    = 10 * time.Second
	maxSuitePayload   = 64 << 10
	subscribeAttempts = 50
	orderedMessages   = 100
)

type testCase struct {
	name string
	fn   func(testing.TB, sse.Broker, Options)
}

// cases lists the contract, one entry per subtest of Run.
func cases() []testCase {
	return []testCase{
		{"PublishReachesSubscriber", publishReachesSubscriber},
		{"SubscribeIsReadyWhenItReturns", subscribeIsReady},
		{"OnlySubscribedChannel", onlySubscribedChannel},
		{"EverySubscriberReceives", everySubscriberReceives},
		{"SeveralChannels", severalChannels},
		{"InOrder", inOrder},
		{"PublishWithoutSubscribers", publishWithoutSubscribers},
		{"CancelClosesTheChannel", cancelClosesTheChannel},
		{"PayloadRoundTrip", payloadRoundTrip},
		{"CarriesHubEvents", carriesHubEvents},
	}
}

// Run checks broker. Every subtest uses its own random channel names, so
// the suite can share a server with other tests. Run does not close broker.
func Run(t *testing.T, broker sse.Broker, o Options) {
	t.Helper()

	if o.Timeout <= 0 {
		o.Timeout = defaultTimeout
	}

	for _, c := range cases() {
		t.Run(c.name, func(t *testing.T) { c.fn(t, broker, o) })
	}
}

func channelName() string { return "ssetest:" + rand.Text()[:8] }

func subscribe(ctx context.Context, tb testing.TB, b sse.Broker, channels ...string) <-chan []byte {
	tb.Helper()

	ch, err := b.Subscribe(ctx, channels...)
	if err != nil {
		tb.Fatalf("Subscribe(%v) = %v", channels, err)
	}

	return ch
}

func publish(ctx context.Context, tb testing.TB, b sse.Broker, channel string, payload []byte) {
	tb.Helper()

	if err := b.Publish(ctx, channel, payload); err != nil {
		tb.Fatalf("Publish(%q) = %v", channel, err)
	}
}

// receive waits for one payload; it fails instead of hanging.
func receive(tb testing.TB, ch <-chan []byte, o Options) []byte {
	tb.Helper()

	select {
	case p, ok := <-ch:
		if !ok {
			tb.Fatal("the subscription channel closed before the message arrived")
		}

		return p
	case <-time.After(o.Timeout):
		tb.Fatalf("no message within %s", o.Timeout)
	}

	return nil
}

func publishReachesSubscriber(tb testing.TB, b sse.Broker, o Options) {
	tb.Helper()

	ctx, cancel := context.WithCancel(tb.Context())
	defer cancel()

	ch := channelName()
	msgs := subscribe(ctx, tb, b, ch)

	publish(ctx, tb, b, ch, []byte("hello"))

	if got := receive(tb, msgs, o); !bytes.Equal(got, []byte("hello")) {
		tb.Errorf("received %q, want %q", got, "hello")
	}
}

// subscribeIsReady: Hub.Subscribe waits for the broker and publishes right
// after, so Subscribe must be listening before it returns.
func subscribeIsReady(tb testing.TB, b sse.Broker, o Options) {
	tb.Helper()

	for i := range subscribeAttempts {
		func() {
			ctx, cancel := context.WithCancel(tb.Context())
			defer cancel()

			ch := channelName()
			msgs := subscribe(ctx, tb, b, ch)
			want := fmt.Appendf(nil, "m%d", i)

			publish(ctx, tb, b, ch, want)

			if got := receive(tb, msgs, o); !bytes.Equal(got, want) {
				tb.Fatalf("attempt %d: received %q, want %q", i, got, want)
			}
		}()
	}
}

func onlySubscribedChannel(tb testing.TB, b sse.Broker, o Options) {
	tb.Helper()

	ctx, cancel := context.WithCancel(tb.Context())
	defer cancel()

	mine, other := channelName(), channelName()
	msgs := subscribe(ctx, tb, b, mine)

	publish(ctx, tb, b, other, []byte("other"))
	publish(ctx, tb, b, mine, []byte("mine"))
	publish(ctx, tb, b, mine, []byte("sentinel"))

	// Whatever arrives before the sentinel must be the subscribed channel's
	// message: a late delivery from the other channel fails here too.
	if got := receive(tb, msgs, o); !bytes.Equal(got, []byte("mine")) {
		tb.Fatalf("first message = %q, want the one published on the subscribed channel", got)
	}

	if got := receive(tb, msgs, o); !bytes.Equal(got, []byte("sentinel")) {
		tb.Errorf("second message = %q, want the sentinel: a message of another channel arrived", got)
	}
}

func everySubscriberReceives(tb testing.TB, b sse.Broker, o Options) {
	tb.Helper()

	ctx, cancel := context.WithCancel(tb.Context())
	defer cancel()

	ch := channelName()
	first, second := subscribe(ctx, tb, b, ch), subscribe(ctx, tb, b, ch)

	publish(ctx, tb, b, ch, []byte("both"))

	for i, msgs := range []<-chan []byte{first, second} {
		if got := receive(tb, msgs, o); !bytes.Equal(got, []byte("both")) {
			tb.Errorf("subscriber %d received %q, want %q", i, got, "both")
		}
	}
}

func severalChannels(tb testing.TB, b sse.Broker, o Options) {
	tb.Helper()

	ctx, cancel := context.WithCancel(tb.Context())
	defer cancel()

	a, c := channelName(), channelName()
	msgs := subscribe(ctx, tb, b, a, c)

	publish(ctx, tb, b, a, []byte("from-a"))
	publish(ctx, tb, b, c, []byte("from-c"))

	got := []string{string(receive(tb, msgs, o)), string(receive(tb, msgs, o))}
	slices.Sort(got)

	if !slices.Equal(got, []string{"from-a", "from-c"}) {
		tb.Errorf("received %v, want one message from each channel", got)
	}
}

func inOrder(tb testing.TB, b sse.Broker, o Options) {
	tb.Helper()

	ctx, cancel := context.WithCancel(tb.Context())
	defer cancel()

	ch := channelName()
	msgs := subscribe(ctx, tb, b, ch)

	// A consumer that keeps up: a broker may drop what a slow consumer has no
	// room for (the Postgres broker does, on purpose), so the reader drains
	// while the publisher runs instead of after it.
	got := make(chan []string, 1)

	go func() {
		var seen []string

		for len(seen) < orderedMessages {
			select {
			case p, ok := <-msgs:
				if !ok {
					got <- seen
					return
				}

				seen = append(seen, string(p))
			case <-time.After(o.Timeout):
				got <- seen
				return
			}
		}

		got <- seen
	}()

	for i := range orderedMessages {
		publish(ctx, tb, b, ch, fmt.Appendf(nil, "%03d", i))
	}

	seen := <-got
	if len(seen) != orderedMessages {
		tb.Fatalf("received %d of %d messages", len(seen), orderedMessages)
	}

	for i, p := range seen {
		if want := fmt.Sprintf("%03d", i); p != want {
			tb.Fatalf("message %d = %q, want %q", i, p, want)
		}
	}
}

func publishWithoutSubscribers(tb testing.TB, b sse.Broker, _ Options) {
	tb.Helper()

	if err := b.Publish(tb.Context(), channelName(), []byte("nobody")); err != nil {
		tb.Errorf("Publish without subscribers = %v, want nil", err)
	}
}

func cancelClosesTheChannel(tb testing.TB, b sse.Broker, o Options) {
	tb.Helper()

	ctx, cancel := context.WithCancel(tb.Context())
	defer cancel()

	msgs := subscribe(ctx, tb, b, channelName())

	cancel()

	deadline := time.After(o.Timeout)

	for {
		select {
		case _, ok := <-msgs:
			if !ok {
				return
			}
		case <-deadline:
			tb.Fatalf("the channel did not close within %s of cancelling the context", o.Timeout)
		}
	}
}

// payloadRoundTrip sends JSON with escapes and multibyte text, padded to the
// largest size the broker takes (at most 64 KiB).
func payloadRoundTrip(tb testing.TB, b sse.Broker, o Options) {
	tb.Helper()

	ctx, cancel := context.WithCancel(tb.Context())
	defer cancel()

	size := maxSuitePayload
	if o.MaxPayload > 0 {
		size = min(o.MaxPayload, maxSuitePayload)
	}

	const head = "{\"s\":\"quote \\\" backslash \\\\ newline \\n tab \\t é 日本語 😀\",\"pad\":\""

	const tail = "\"}"

	payload := []byte(head + strings.Repeat("x", max(size-len(head)-len(tail), 0)) + tail)

	ch := channelName()
	msgs := subscribe(ctx, tb, b, ch)

	publish(ctx, tb, b, ch, payload)

	if got := receive(tb, msgs, o); !bytes.Equal(got, payload) {
		tb.Errorf("payload of %d bytes came back as %d bytes and differs", len(payload), len(got))
	}
}

// carriesHubEvents: two hubs on one broker stand for two app instances.
func carriesHubEvents(tb testing.TB, b sse.Broker, o Options) {
	tb.Helper()

	ctx, cancel := context.WithCancel(tb.Context())
	defer cancel()

	publisher, subscriber := sse.NewHub(b), sse.NewHub(b)
	defer publisher.Close()
	defer subscriber.Close()

	topic := channelName()
	events, unsub := subscriber.Subscribe(ctx, topic)

	defer unsub()

	want := sse.Event{ID: "7", Event: "ping", Data: "line1\nline2"}
	if err := publisher.Publish(ctx, topic, want); err != nil {
		tb.Fatalf("Publish = %v", err)
	}

	select {
	case got, ok := <-events:
		if !ok {
			tb.Fatal("the hub subscription closed before the event arrived")
		}

		if got.ID != want.ID || got.Event != want.Event || got.Data != want.Data {
			tb.Errorf("event = %+v, want %+v", got, want)
		}
	case <-time.After(o.Timeout):
		tb.Fatalf("no event from the other hub within %s", o.Timeout)
	}
}
