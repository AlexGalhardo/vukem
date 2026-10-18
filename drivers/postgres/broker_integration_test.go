//go:build integration

package postgres

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/drivers/postgres/pgtest"
	"github.com/AlexGalhardo/vukem/sse"
)

// brokerAdmin mirrors open: POSTGRES_URL, else the compose service
// (task services:up).
func brokerAdmin() string {
	if u := os.Getenv("POSTGRES_URL"); u != "" {
		return u
	}

	return "postgres://vukem:vukem@localhost:55432/vukem_test?sslmode=disable"
}

// brokerDSN is a throwaway database for one test. Notifications never leave
// the database they were sent in, so every connection of a test shares this
// one DSN: a second pgtest.Database would be a second database, and nothing
// published in one would ever arrive in the other.
func brokerDSN(t *testing.T) string {
	t.Helper()

	return pgtest.Database(t, brokerAdmin())
}

// openDSN connects to dsn. Each call is a separate pool, which is what lets
// one test stand in for two processes.
func openDSN(t *testing.T, dsn string, opts ...database.Option) *database.DB {
	t.Helper()

	db, err := database.Open(t.Context(), dsn, append([]database.Option{database.WithDriver(Driver())}, opts...)...)
	if err != nil {
		t.Fatalf("connect: %v (run task services:up)", err)
	}

	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})

	return db
}

func brokerOn(t *testing.T, dsn string, opts ...database.Option) *Broker {
	t.Helper()

	b, err := NewBroker(openDSN(t, dsn, opts...))
	if err != nil {
		t.Fatalf("NewBroker: %v", err)
	}

	return b
}

func newBroker(t *testing.T) *Broker {
	t.Helper()

	return brokerOn(t, brokerDSN(t))
}

// receive waits for one payload, so a hung subscription fails the test
// instead of hanging the package.
func receive(t *testing.T, ch <-chan []byte) string {
	t.Helper()

	select {
	case got, ok := <-ch:
		if !ok {
			t.Fatal("the subscription closed before delivering anything")
		}

		return string(got)
	case <-time.After(10 * time.Second):
		t.Fatal("no notification within 10s")

		return ""
	}
}

// This is the property the whole driver rests on: a publish on one
// connection reaches a subscriber on another. The web service and the
// queue:work service are separate processes, so without it no progress ever
// leaves the worker.
func TestBrokerPublishOnOneConnectionReachesASubscriberOnAnother(t *testing.T) {
	t.Parallel()

	const channel = "sse:conversions:cross-process"

	dsn := brokerDSN(t)
	listener := brokerOn(t, dsn)
	publisher := brokerOn(t, dsn) // a second pool over the same database: the worker

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	ch, err := listener.Subscribe(ctx, channel)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	if err := publisher.Publish(ctx, channel, []byte("from the worker")); err != nil {
		t.Fatalf("publish: %v", err)
	}

	if got := receive(t, ch); got != "from the worker" {
		t.Fatalf("got %q", got)
	}
}

// Subscribe must have the LISTEN in place before it returns: the Hub
// publishes as soon as Subscribe returns, and an event lost there is the
// race a progress stream hits every time (the browser subscribes, then the
// worker starts).
func TestBrokerSubscribeIsListeningBeforeItReturns(t *testing.T) {
	t.Parallel()

	const channel = "sse:conversions:01JABCDEF"

	b := newBroker(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	ch, err := b.Subscribe(ctx, channel)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	if err := b.Publish(ctx, channel, []byte(`{"progress":10}`)); err != nil {
		t.Fatalf("publish: %v", err)
	}

	if got := receive(t, ch); got != `{"progress":10}` {
		t.Fatalf("got %q", got)
	}
}

// Channels stay isolated: one conversion's progress on another conversion's
// stream would hand one user another user's work.
func TestBrokerDeliversOnlyTheSubscribedChannel(t *testing.T) {
	t.Parallel()

	b := newBroker(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	mine, err := b.Subscribe(ctx, "sse:conversions:mine")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	if err := b.Publish(ctx, "sse:conversions:theirs", []byte("not for me")); err != nil {
		t.Fatalf("publish: %v", err)
	}

	if err := b.Publish(ctx, "sse:conversions:mine", []byte("for me")); err != nil {
		t.Fatalf("publish: %v", err)
	}

	// The second publish arriving proves the first had its chance: both went
	// out in order, and the server queues a listener's notifications in order.
	if got := receive(t, mine); got != "for me" {
		t.Fatalf("got %q, so another channel's event was delivered", got)
	}
}

// Every subscriber of a channel gets every event: two browsers watching one
// conversion must both see it finish.
func TestBrokerDeliversToEverySubscriberOfAChannel(t *testing.T) {
	t.Parallel()

	const channel = "sse:conversions:shared"

	b := newBroker(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	first, err := b.Subscribe(ctx, channel)
	if err != nil {
		t.Fatalf("subscribe first: %v", err)
	}

	second, err := b.Subscribe(ctx, channel)
	if err != nil {
		t.Fatalf("subscribe second: %v", err)
	}

	if err := b.Publish(ctx, channel, []byte("done")); err != nil {
		t.Fatalf("publish: %v", err)
	}

	var wg sync.WaitGroup

	for _, ch := range []<-chan []byte{first, second} {
		wg.Go(func() {
			if got := receive(t, ch); got != "done" {
				t.Errorf("got %q", got)
			}
		})
	}

	wg.Wait()
}

// One subscription may carry several channels, since sse.Broker allows it.
func TestBrokerSubscribesToSeveralChannelsAtOnce(t *testing.T) {
	t.Parallel()

	b := newBroker(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	ch, err := b.Subscribe(ctx, "sse:a", "sse:b")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	if err := b.Publish(ctx, "sse:b", []byte("b")); err != nil {
		t.Fatalf("publish: %v", err)
	}

	if got := receive(t, ch); got != "b" {
		t.Fatalf("got %q", got)
	}
}

// A cancelled context must end the subscription and close the channel: that
// is the contract sse.Hub relies on when a topic loses its last subscriber.
func TestBrokerCancellingTheContextClosesTheSubscription(t *testing.T) {
	t.Parallel()

	b := newBroker(t)

	ctx, cancel := context.WithCancel(t.Context())

	ch, err := b.Subscribe(ctx, "sse:conversions:closing")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	cancel()

	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("a payload arrived after the context was cancelled")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the subscription channel was not closed within 10s of cancelling")
	}
}

// A finished subscription must not hand its connection back to the pool
// still listening: pgx's stdlib driver does not reset LISTEN between
// borrowers, so the next borrower would keep the subscription alive and the
// server could never truncate its notification queue. The pool holds one
// connection, so the connection checked here is the one the subscription
// used, if it was kept at all.
func TestBrokerLeavesNoConnectionListening(t *testing.T) {
	t.Parallel()

	db := openDSN(t, brokerDSN(t), database.WithConfig(database.Config{MaxOpen: 1, MaxIdle: 1}))

	b, err := NewBroker(db)
	if err != nil {
		t.Fatalf("NewBroker: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())

	ch, err := b.Subscribe(ctx, "sse:conversions:released")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	cancel()

	select {
	case <-ch: // closed once the connection is back in the pool
	case <-time.After(10 * time.Second):
		t.Fatal("the subscription channel was not closed within 10s of cancelling")
	}

	var listening int

	if err := db.QueryRowContext(t.Context(),
		"SELECT count(*) FROM pg_listening_channels()").Scan(&listening); err != nil {
		t.Fatalf("count the channels of the pooled connection: %v", err)
	}

	if listening != 0 {
		t.Fatalf("the pooled connection still listens on %d channel(s)", listening)
	}
}

// PostgreSQL caps a NOTIFY payload and fails the statement above it.
// Refusing early, with a named error, beats a publish that fails deep inside
// the driver on the one event that mattered.
func TestBrokerPublishRefusesAnOversizedPayload(t *testing.T) {
	t.Parallel()

	b := newBroker(t)

	err := b.Publish(t.Context(), "sse:conversions:big", []byte(strings.Repeat("x", MaxNotifyPayload+1)))
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("err = %v, want ErrPayloadTooLarge", err)
	}
}

// A payload exactly at the limit must still be delivered, and one byte more
// must be refused by the server itself: together these pin MaxNotifyPayload
// to the server's real boundary instead of a guess with a margin.
func TestMaxNotifyPayloadIsTheServerLimit(t *testing.T) {
	t.Parallel()

	const channel = "sse:conversions:at-the-limit"

	b := newBroker(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	ch, err := b.Subscribe(ctx, channel)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	payload := strings.Repeat("x", MaxNotifyPayload)
	if err := b.Publish(ctx, channel, []byte(payload)); err != nil {
		t.Fatalf("publish a payload at the limit: %v", err)
	}

	if got := receive(t, ch); got != payload {
		t.Fatalf("got %d bytes, want %d", len(got), len(payload))
	}

	// Straight to pg_notify, past Publish's own check.
	_, err = b.db.ExecContext(ctx, "SELECT pg_notify($1, $2)",
		notifyIdentifier(channel), strings.Repeat("x", MaxNotifyPayload+1))
	if err == nil {
		t.Fatalf("the server accepted %d bytes, so MaxNotifyPayload is lower than it needs to be",
			MaxNotifyPayload+1)
	}
}

// A long channel name must work: PostgreSQL identifiers stop at 63 bytes and
// an SSE topic passes that easily. Hashing the name is what makes this pass,
// so this test is what stops anyone simplifying it back to interpolation.
func TestBrokerAcceptsALongChannelName(t *testing.T) {
	t.Parallel()

	channel := "sse:conversions:" + strings.Repeat("a", 200)

	b := newBroker(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	ch, err := b.Subscribe(ctx, channel)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	if err := b.Publish(ctx, channel, []byte("ok")); err != nil {
		t.Fatalf("publish: %v", err)
	}

	if got := receive(t, ch); got != "ok" {
		t.Fatalf("got %q", got)
	}
}

// A channel name holding a quote must not be able to change the statement.
// The name never reaches SQL as text — it is hashed — and the table the name
// tries to drop is the proof, not the comment above notifyIdentifier.
func TestBrokerChannelNameCannotInjectSQL(t *testing.T) {
	t.Parallel()

	b := newBroker(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	if _, err := b.db.ExecContext(ctx, "CREATE TABLE broker_injection (id int)"); err != nil {
		t.Fatalf("create the table the channel name tries to drop: %v", err)
	}

	channel := `sse:"; DROP TABLE broker_injection; --`

	ch, err := b.Subscribe(ctx, channel)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	if err := b.Publish(ctx, channel, []byte("still here")); err != nil {
		t.Fatalf("publish: %v", err)
	}

	if got := receive(t, ch); got != "still here" {
		t.Fatalf("got %q", got)
	}

	var tables int

	if err := b.db.QueryRowContext(ctx,
		"SELECT count(*) FROM information_schema.tables WHERE table_name = 'broker_injection'").
		Scan(&tables); err != nil {
		t.Fatalf("look for the table: %v", err)
	}

	if tables != 1 {
		t.Fatal("the channel name dropped the table")
	}
}

func TestBrokerSubscribeNeedsAChannel(t *testing.T) {
	t.Parallel()

	b := newBroker(t)

	if _, err := b.Subscribe(t.Context()); err == nil {
		t.Fatal("Subscribe with no channel was accepted")
	}
}

// The finding this driver answers, end to end: an sse.Hub in the worker
// process publishes and an sse.Hub in the web process streams the event.
func TestBrokerCarriesHubEventsBetweenProcesses(t *testing.T) {
	t.Parallel()

	const topic = "conversions:01JABCDEF"

	dsn := brokerDSN(t)

	web := sse.NewHub(brokerOn(t, dsn))
	defer web.Close()

	worker := sse.NewHub(brokerOn(t, dsn))
	defer worker.Close()

	events, unsubscribe := web.Subscribe(t.Context(), topic)
	defer unsubscribe()

	want := sse.Event{Event: "progress", Data: `{"percent":42}`}
	if err := worker.Publish(t.Context(), topic, want); err != nil {
		t.Fatalf("publish through the worker hub: %v", err)
	}

	select {
	case got, ok := <-events:
		if !ok {
			t.Fatal("the stream closed before delivering anything")
		}

		if got != want {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the worker's event did not reach the web hub within 10s")
	}
}
