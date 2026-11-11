package broadcast

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/AlexGalhardo/vukem/sse"
)

// Subscription receives the events of authorized channels until Close,
// ctx cancellation, Server.Close, or until it falls too far behind (then
// Messages closes and the client should reconnect).
type Subscription struct {
	id       string
	srv      *Server
	channels []string
	members  map[string]Member // presence channel → own member
	out      chan Message
	unsub    func()
	done     chan struct{}
	once     sync.Once

	// attempted lists the presence channels a Join was started on, including
	// one that failed: a store that timed out may have written anyway.
	attempted []string
	// hbCancel and hbDone stop the heartbeat and wait for it (nil without a
	// shared store).
	hbCancel context.CancelFunc
	hbDone   chan struct{}
}

// Subscribe authorizes every channel (all or nothing) and subscribes to
// them; presence channels announce the member to the others. ctx carries
// the user (usually the request context) and bounds the subscription.
func (s *Server) Subscribe(ctx context.Context, channels ...string) (*Subscription, error) {
	channels = dedupe(channels)
	if len(channels) == 0 {
		return nil, ErrInvalidChannel
	}

	if len(channels) > s.cfg.MaxChannels {
		return nil, ErrTooManyChannels
	}

	members := map[string]Member{}

	for _, ch := range channels {
		m, err := s.Authorize(ctx, ch)
		if err != nil {
			return nil, err
		}

		if k, _ := kindOf(ch); k == kindPresence {
			members[ch] = m
		}
	}

	topics := make([]string, len(channels))
	for i, ch := range channels {
		topics[i] = topic(ch)
	}

	events, unsub, err := s.hub.TrySubscribe(ctx, topics...)
	if err != nil {
		return nil, fmt.Errorf("broadcast: subscribe: %w", err)
	}

	sub := &Subscription{
		id:       newID(),
		srv:      s,
		channels: channels,
		members:  members,
		out:      make(chan Message, 1),
		unsub:    unsub,
		done:     make(chan struct{}),
	}

	// Whatever way Subscribe fails, a store error or a panic in the store,
	// leave the presence channels and the hub as they were.
	ok := false

	defer func() {
		if !ok {
			sub.leave(sub.attempted)
			unsub()
		}
	}()

	if err := sub.join(ctx); err != nil {
		return nil, fmt.Errorf("broadcast: presence store: %w", err)
	}

	ok = true

	go sub.forward(events)

	if s.cfg.Presence != nil && len(members) > 0 {
		sub.startHeartbeat(ctx)
	}

	return sub, nil
}

func dedupe(channels []string) []string {
	out := make([]string, 0, len(channels))

	for _, ch := range channels {
		if ch = strings.TrimSpace(ch); ch != "" && !slices.Contains(out, ch) {
			out = append(out, ch)
		}
	}

	return out
}

func (sub *Subscription) forward(events <-chan sse.Event) {
	defer close(sub.out)
	defer sub.Close()

	for ev := range events {
		env, ok := sub.srv.decodeEnvelope(ev.Data)
		if !ok {
			continue
		}

		if env.Origin == sub.id {
			continue // own presence notification
		}

		select {
		case sub.out <- Message{Channel: env.Channel, Event: env.Event, Data: slices.Clone(env.Data)}:
		case <-sub.done:
			return
		}
	}
}

// Channels lists the subscribed channels.
func (sub *Subscription) Channels() []string { return slices.Clone(sub.channels) }

// Messages delivers the events; it closes when the subscription ends.
func (sub *Subscription) Messages() <-chan Message { return sub.out }

// Here lists the members present on a subscribed presence channel. With a
// shared Config.Presence the list covers every instance. It returns
// ErrInvalidChannel for a channel this subscription has no presence on.
func (sub *Subscription) Here(ctx context.Context, channel string) ([]Member, error) {
	if _, ok := sub.members[channel]; !ok {
		return nil, ErrInvalidChannel
	}

	members, err := sub.srv.presence.Members(ctx, channel)
	if err != nil {
		return nil, fmt.Errorf("broadcast: presence store: %w", err)
	}

	if members == nil {
		members = []Member{}
	}

	return members, nil
}

// Members lists the members present on a subscribed presence channel, or
// false for other channels.
//
// Prefer Here, which takes a context and reports store errors: Members waits
// at most 5 seconds and returns an empty list when a shared store fails. It
// becomes deprecated once drivers/websocket moves to Here (it still builds
// against v0.40.0, which has no Here).
func (sub *Subscription) Members(channel string) ([]Member, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), leaveTimeout)
	defer cancel()

	members, err := sub.Here(ctx, channel)
	if errors.Is(err, ErrInvalidChannel) {
		return nil, false
	}

	if err != nil {
		return []Member{}, true
	}

	return members, true
}

// join enters every presence channel, then announces the members that were
// first on theirs. The caller undoes the joins if it fails.
func (sub *Subscription) join(ctx context.Context) error {
	var firsts []string

	for _, ch := range sub.channels {
		m, ok := sub.members[ch]
		if !ok {
			continue
		}

		sub.attempted = append(sub.attempted, ch)

		first, err := sub.srv.presence.Join(ctx, ch, sub.id, m)
		if err != nil {
			return err
		}

		if first {
			firsts = append(firsts, ch)
		}
	}

	// The request context may end right after Subscribe returns; the
	// announcement has its own bounded one.
	actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), leaveTimeout)
	defer cancel()

	for _, ch := range firsts {
		sub.announce(actx, ch, EventJoining, sub.members[ch])
	}

	return nil
}

// leave removes this connection from channels without announcing it, under
// one timeout for all of them.
func (sub *Subscription) leave(channels []string) {
	ctx, cancel := context.WithTimeout(context.Background(), leaveTimeout)
	defer cancel()

	for _, ch := range channels {
		_, _ = sub.srv.presence.Leave(ctx, ch, sub.id, sub.members[ch].ID)
	}
}

// startHeartbeat keeps this connection alive in a store that expires them
// until Close, which cancels it and waits so that no Touch can run after the
// Leave.
func (sub *Subscription) startHeartbeat(ctx context.Context) {
	var hbCtx context.Context

	hbCtx, sub.hbCancel = context.WithCancel(context.WithoutCancel(ctx))
	sub.hbDone = make(chan struct{})

	go func() {
		defer close(sub.hbDone)

		sub.heartbeat(hbCtx)
	}()
}

// heartbeat touches every presence channel each tick. A failed Touch is
// retried at the next tick.
func (sub *Subscription) heartbeat(ctx context.Context) {
	ticker := time.NewTicker(sub.srv.cfg.PresenceHeartbeat)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tctx, cancel := context.WithTimeout(ctx, leaveTimeout)

			for ch := range sub.members {
				_ = sub.srv.presence.Touch(tctx, ch, sub.id)
			}

			cancel()
		}
	}
}

// Close unsubscribes and announces leaving presence channels; it is safe
// to call more than once. It stops the heartbeat first and waits for it, so a
// Touch cannot run after the Leave.
func (sub *Subscription) Close() {
	sub.once.Do(func() {
		close(sub.done)
		sub.unsub()

		if sub.hbCancel != nil {
			sub.hbCancel()
			<-sub.hbDone
		}

		// The request context is gone by now: leave with a context of our
		// own, one timeout for every channel. A failed leave is not
		// announced; a store that expires connections drops the member by
		// itself.
		ctx, cancel := context.WithTimeout(context.Background(), leaveTimeout)
		defer cancel()

		for ch, m := range sub.members {
			if last, err := sub.srv.presence.Leave(ctx, ch, sub.id, m.ID); err == nil && last {
				sub.announce(ctx, ch, EventLeaving, m)
			}
		}
	})
}

func (sub *Subscription) announce(ctx context.Context, channel, event string, m Member) {
	b, err := json.Marshal(m)
	if err != nil {
		return
	}

	// A failed presence notification must not fail the subscription.
	_ = sub.srv.publish(ctx, channel, event, b, sub.id)
}
