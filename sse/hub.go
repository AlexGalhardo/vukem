package sse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Broker carries events between app instances. Two implementations ship
// with the framework: *redis.Client from drivers/redis (pub/sub) and
// *postgres.Broker from drivers/postgres (LISTEN/NOTIFY, for an app that
// has PostgreSQL and no Redis; it refuses a payload over 7999 bytes, the
// NOTIFY limit). Subscribe must end the subscription and close the returned
// channel once ctx is done: the Hub cancels it when a topic loses its last
// local subscriber.
type Broker interface {
	Publish(ctx context.Context, channel string, payload []byte) error
	Subscribe(ctx context.Context, channels ...string) (<-chan []byte, error)
}

// subscriberBuffer is the default number of events a slow subscriber may
// lag behind before it is dropped (its stream ends; the client reconnects).
const subscriberBuffer = 64

// Hub fans events published on topics out to subscribed streams. Without
// a Broker it works within one process; with one, every instance receives
// every event. A topic holds one broker subscription while it has local
// subscribers and releases it when the last one leaves.
type Hub struct {
	broker Broker
	buffer int

	mu     sync.Mutex
	subs   map[string]map[*sub]struct{}
	relays map[string]*relay
	closed bool
	stop   context.CancelFunc
	ctx    context.Context
}

type sub struct {
	ch     chan Event
	topics []string
	once   sync.Once
	// detach stops watching the subscriber's context once it is removed.
	detach func() bool
}

// relay is the broker subscription of one topic; cancel ends it. ready
// closes once the subscription is up or has failed (err).
type relay struct {
	cancel context.CancelFunc
	ready  chan struct{}
	err    error
}

// brokerSubscribeTimeout bounds how long Subscribe waits for the broker.
const brokerSubscribeTimeout = 10 * time.Second

var errSubscribeTimeout = errors.New("sse: broker subscribe timed out")

// HubOption configures NewHub.
type HubOption func(*Hub)

// Buffer sets how many events each subscriber may lag behind before it is
// dropped (64 by default; values below 1 keep the default). A client that
// receives bursts, such as a project editor replaying many operations,
// needs a larger one; memory grows with subscribers times n.
func Buffer(n int) HubOption {
	return func(h *Hub) {
		if n > 0 {
			h.buffer = n
		}
	}
}

// NewHub returns a hub; broker may be nil for a single instance.
func NewHub(broker Broker, opts ...HubOption) *Hub {
	ctx, stop := context.WithCancel(context.Background())
	h := &Hub{
		broker: broker, buffer: subscriberBuffer,
		subs: map[string]map[*sub]struct{}{}, relays: map[string]*relay{}, ctx: ctx, stop: stop,
	}

	for _, o := range opts {
		o(h)
	}

	return h
}

func channel(topic string) string { return "sse:" + topic }

// Publish sends ev to every subscriber of topic.
func (h *Hub) Publish(ctx context.Context, topic string, ev Event) error {
	if h.broker == nil {
		h.deliver(topic, ev)
		return nil
	}

	b, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("sse: encode event: %w", err)
	}

	return h.broker.Publish(ctx, channel(topic), b)
}

// Subscribe returns a channel of the events of topics and a function that
// unsubscribes. The channel closes on unsubscribe, ctx cancellation,
// Close, or when the subscriber falls too far behind. When a Broker cannot
// subscribe, the channel comes back already closed; TrySubscribe reports why.
func (h *Hub) Subscribe(ctx context.Context, topics ...string) (<-chan Event, func()) {
	ch, unsub, _ := h.TrySubscribe(ctx, topics...)

	return ch, unsub
}

// TrySubscribe is Subscribe that reports a broker failure: when a topic's
// relay cannot subscribe to the broker it returns that error together with
// an already closed channel, and no subscriber stays registered.
func (h *Hub) TrySubscribe(ctx context.Context, topics ...string) (<-chan Event, func(), error) {
	s := &sub{ch: make(chan Event, h.buffer), topics: topics}

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		close(s.ch)

		return s.ch, func() {}, nil
	}

	type pending struct {
		topic string
		ctx   context.Context
		r     *relay
	}

	var (
		start []pending
		wait  []*relay
	)

	for _, t := range topics {
		if h.subs[t] == nil {
			h.subs[t] = map[*sub]struct{}{}
		}

		h.subs[t][s] = struct{}{}

		if h.broker == nil {
			continue
		}

		r := h.relays[t]
		if r == nil {
			rctx, cancel := context.WithCancel(h.ctx)
			r = &relay{cancel: cancel, ready: make(chan struct{})}
			h.relays[t] = r
			start = append(start, pending{t, rctx, r})
		}

		wait = append(wait, r)
	}

	// AfterFunc spawns no goroutine until ctx ends, so an unsubscribed
	// stream on a long-lived ctx leaves nothing behind.
	s.detach = context.AfterFunc(ctx, func() { h.remove(s) })
	h.mu.Unlock()

	for _, p := range start {
		h.relay(p.ctx, p.topic, p.r)
	}

	// Every subscriber, not only the one that started a relay, waits for it:
	// a Publish right after Subscribe must reach this instance.
	for _, r := range wait {
		<-r.ready

		if r.err != nil {
			// Fail closed: never leave a stream on a topic that receives
			// nothing. The client reconnects and the next Subscribe retries.
			h.remove(s)

			return s.ch, func() {}, r.err
		}
	}

	return s.ch, func() { h.remove(s) }, nil
}

// SubscribeFunc is [Hub.TrySubscribe] with a filter: the returned channel
// receives only the events for which keep returns true, so one topic can
// serve subscribers that may each see a different part of it (a member's
// own requests on a shared "inbox" topic, decided from ev.Data). keep runs
// on a goroutine of the subscription, outside the hub's lock, once per event
// and in order; it may read a store but must not block for long, since a
// subscriber that falls Buffer events behind is dropped like any other. A
// nil keep keeps every event, and a keep that panics ends the subscription.
// The channel closes on unsubscribe, ctx cancellation, Close, or when the
// subscriber falls too far behind.
func (h *Hub) SubscribeFunc(
	ctx context.Context,
	keep func(Event) bool,
	topics ...string,
) (<-chan Event, func(), error) {
	in, unsubscribe, err := h.TrySubscribe(ctx, topics...)
	if err != nil || keep == nil {
		return in, unsubscribe, err
	}

	out := make(chan Event)
	done := make(chan struct{})

	var once sync.Once

	stop := func() {
		once.Do(func() {
			close(done)
			unsubscribe()
		})
	}

	go func() {
		defer close(out)
		// keep runs outside any request's recover: a panic in it ends this
		// subscription (the stream closes, the client reconnects) instead
		// of the process.
		defer func() {
			if recover() != nil {
				stop()
			}
		}()

		for ev := range in {
			if !keep(ev) {
				continue
			}

			select {
			case out <- ev:
			case <-done:
				return
			case <-ctx.Done():
				// Nobody reads out any more; the hub removes the subscriber.
				return
			}
		}
	}()

	return out, stop, nil
}

// relay subscribes to the broker channel of topic, waiting at most
// brokerSubscribeTimeout, then forwards its messages to local subscribers
// until r is cancelled (the topic emptied or the hub closed).
func (h *Hub) relay(ctx context.Context, topic string, r *relay) {
	type result struct {
		msgs <-chan []byte
		err  error
	}

	done := make(chan result, 1)

	// The subscription lives as long as ctx, so the wait is bounded with a
	// timer rather than a deadline on ctx.
	go func() {
		msgs, err := h.broker.Subscribe(ctx, channel(topic))
		done <- result{msgs, err}
	}()

	timer := time.NewTimer(brokerSubscribeTimeout)
	defer timer.Stop()

	var res result

	select {
	case res = <-done:
	case <-timer.C:
		res.err = errSubscribeTimeout
	}

	if res.err != nil {
		// Forget the relay so the next subscriber of topic retries; cancel
		// also ends a subscription that succeeds after the timeout.
		h.mu.Lock()
		if h.relays[topic] == r {
			delete(h.relays, topic)
		}
		h.mu.Unlock()
		r.cancel()

		r.err = fmt.Errorf("sse: subscribe %q: %w", topic, res.err)
		close(r.ready)

		return
	}

	close(r.ready)

	go h.forward(topic, r, res.msgs)
}

func (h *Hub) forward(topic string, r *relay, msgs <-chan []byte) {
	for b := range msgs {
		var ev Event
		if json.Unmarshal(b, &ev) == nil {
			h.deliverFrom(topic, r, ev)
		}
	}

	// The broker ended the subscription (connection lost): end the streams
	// so clients reconnect and the next Subscribe opens a new one.
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.relays[topic] != r {
		return // torn down on purpose: the topic emptied or the hub closed
	}

	delete(h.relays, topic)
	r.cancel()

	for s := range h.subs[topic] {
		h.removeLocked(s)
	}
}

// deliverFrom delivers ev only while r is the topic's current relay, so a
// relay being torn down never duplicates events of its replacement.
func (h *Hub) deliverFrom(topic string, r *relay, ev Event) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.relays[topic] == r {
		h.deliverLocked(topic, ev)
	}
}

func (h *Hub) deliver(topic string, ev Event) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.deliverLocked(topic, ev)
}

func (h *Hub) deliverLocked(topic string, ev Event) {
	for s := range h.subs[topic] {
		select {
		case s.ch <- ev:
		default:
			h.removeLocked(s) // too slow: drop it rather than block publishers
		}
	}
}

func (h *Hub) remove(s *sub) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.removeLocked(s)
}

func (h *Hub) removeLocked(s *sub) {
	for _, t := range s.topics {
		set := h.subs[t]
		if set == nil {
			continue
		}

		delete(set, s)

		if len(set) == 0 {
			delete(h.subs, t)

			if r := h.relays[t]; r != nil {
				delete(h.relays, t)
				r.cancel()
			}
		}
	}

	s.once.Do(func() {
		if s.detach != nil {
			s.detach()
		}

		close(s.ch)
	})
}

// Subscribers reports how many subscribers topic has.
func (h *Hub) Subscribers(topic string) int {
	h.mu.Lock()
	defer h.mu.Unlock()

	return len(h.subs[topic])
}

// Stream sends the events of topics to s until the client disconnects,
// the server shuts down or the hub closes.
func (h *Hub) Stream(s *Stream, topics ...string) error {
	events, unsubscribe := h.Subscribe(s.Context(), topics...)
	defer unsubscribe()

	pump(s, events)

	return nil
}

// StreamFunc is [Hub.Stream] that sends only the events keep returns true
// for (see [Hub.SubscribeFunc]): each client of a shared topic gets the
// events it may see, decided with what the handler knows about the caller.
func (h *Hub) StreamFunc(s *Stream, keep func(Event) bool, topics ...string) error {
	events, unsubscribe, _ := h.SubscribeFunc(s.Context(), keep, topics...)
	defer unsubscribe()

	pump(s, events)

	return nil
}

// pump sends events to s until the channel closes or the client leaves; a
// send that fails means the client went away, which ends the stream cleanly.
func pump(s *Stream, events <-chan Event) {
	for {
		select {
		case <-s.Context().Done():
			return
		case ev, ok := <-events:
			if !ok || s.SendEvent(ev) != nil {
				return
			}
		}
	}
}

// Close ends every subscription; later subscriptions end immediately.
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return
	}

	h.closed = true
	h.stop()

	for _, set := range h.subs {
		for s := range set {
			h.removeLocked(s)
		}
	}
}
