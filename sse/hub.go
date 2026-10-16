package sse

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// Broker carries events between app instances; *redis.Client from
// drivers/redis implements it.
type Broker interface {
	Publish(ctx context.Context, channel string, payload []byte) error
	Subscribe(ctx context.Context, channels ...string) (<-chan []byte, error)
}

// subscriberBuffer is how many events a slow subscriber may lag behind
// before it is dropped (its stream ends; the client reconnects).
const subscriberBuffer = 64

// Hub fans events published on topics out to subscribed streams. Without
// a Broker it works within one process; with one, every instance receives
// every event.
type Hub struct {
	broker Broker

	mu     sync.Mutex
	subs   map[string]map[*sub]struct{}
	relays map[string]bool
	closed bool
	stop   context.CancelFunc
	ctx    context.Context
}

type sub struct {
	ch     chan Event
	topics []string
	once   sync.Once
}

// NewHub returns a hub; broker may be nil for a single instance.
func NewHub(broker Broker) *Hub {
	ctx, stop := context.WithCancel(context.Background())
	return &Hub{broker: broker, subs: map[string]map[*sub]struct{}{}, relays: map[string]bool{}, ctx: ctx, stop: stop}
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
// Close, or when the subscriber falls too far behind.
func (h *Hub) Subscribe(ctx context.Context, topics ...string) (<-chan Event, func()) {
	s := &sub{ch: make(chan Event, subscriberBuffer), topics: topics}

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		close(s.ch)

		return s.ch, func() {}
	}

	for _, t := range topics {
		if h.subs[t] == nil {
			h.subs[t] = map[*sub]struct{}{}
		}

		h.subs[t][s] = struct{}{}
	}
	h.mu.Unlock()

	if h.broker != nil {
		h.relay(topics)
	}

	go func() {
		select {
		case <-ctx.Done():
			h.remove(s)
		case <-h.ctx.Done():
		}
	}()

	return s.ch, func() { h.remove(s) }
}

// relay forwards broker messages of topics to local subscribers for as
// long as the hub lives. One relay per topic per hub.
func (h *Hub) relay(topics []string) {
	for _, t := range topics {
		h.mu.Lock()
		started := h.relays[t]
		h.relays[t] = true
		h.mu.Unlock()

		if started {
			continue
		}

		msgs, err := h.broker.Subscribe(h.ctx, channel(t))
		if err != nil {
			continue
		}

		go func(topic string) {
			for b := range msgs {
				var ev Event
				if json.Unmarshal(b, &ev) == nil {
					h.deliver(topic, ev)
				}
			}
		}(t)
	}
}

func (h *Hub) deliver(topic string, ev Event) {
	h.mu.Lock()
	defer h.mu.Unlock()

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
		delete(h.subs[t], s)
	}

	s.once.Do(func() { close(s.ch) })
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

	for {
		select {
		case <-s.Context().Done():
			return nil
		case ev, ok := <-events:
			if !ok {
				return nil
			}

			if err := s.SendEvent(ev); err != nil {
				return nil // the client went away
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
