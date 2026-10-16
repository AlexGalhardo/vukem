package broadcast

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"

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

	events, unsub := s.hub.Subscribe(ctx, topics...)

	sub := &Subscription{
		id:       newID(),
		srv:      s,
		channels: channels,
		members:  members,
		out:      make(chan Message, 1),
		unsub:    unsub,
		done:     make(chan struct{}),
	}

	for ch, m := range members {
		if s.presence.join(ch, m) {
			sub.announce(ctx, ch, EventJoining, m)
		}
	}

	go sub.forward(events)

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
		var env envelope
		if json.Unmarshal([]byte(ev.Data), &env) != nil {
			continue
		}

		if env.Origin == sub.id {
			continue // own presence notification
		}

		select {
		case sub.out <- Message{Channel: env.Channel, Event: env.Event, Data: env.Data}:
		case <-sub.done:
			return
		}
	}
}

// Channels lists the subscribed channels.
func (sub *Subscription) Channels() []string { return slices.Clone(sub.channels) }

// Messages delivers the events; it closes when the subscription ends.
func (sub *Subscription) Messages() <-chan Message { return sub.out }

// Members lists the members present on a subscribed presence channel
// (this instance's view), or false for other channels.
func (sub *Subscription) Members(channel string) ([]Member, bool) {
	if _, ok := sub.members[channel]; !ok {
		return nil, false
	}

	return sub.srv.presence.list(channel), true
}

// Close unsubscribes and announces leaving presence channels; it is safe
// to call more than once.
func (sub *Subscription) Close() {
	sub.once.Do(func() {
		close(sub.done)
		sub.unsub()

		for ch, m := range sub.members {
			if left := sub.srv.presence.leave(ch, m.ID); left {
				ctx, cancel := context.WithTimeout(context.Background(), leaveTimeout)
				sub.announce(ctx, ch, EventLeaving, m)
				cancel()
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

// presence tracks the members of presence channels in this process; a
// member joins on its first connection and leaves with its last.
//
// shortcut: per-instance state; with a Broker, joining/leaving reach every
// instance but Members only lists local members. Upgrade to a shared
// store (Redis sets) when cluster-wide "here" lists are needed.
type presence struct {
	mu    sync.Mutex
	chans map[string]map[string]*presenceEntry
}

type presenceEntry struct {
	m Member
	n int
}

func newPresence() *presence { return &presence{chans: map[string]map[string]*presenceEntry{}} }

func (p *presence) join(channel string, m Member) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	set := p.chans[channel]
	if set == nil {
		set = map[string]*presenceEntry{}
		p.chans[channel] = set
	}

	e := set[m.ID]
	if e == nil {
		set[m.ID] = &presenceEntry{m: m, n: 1}
		return true
	}

	e.n++
	e.m = m

	return false
}

func (p *presence) leave(channel, id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	e := p.chans[channel][id]
	if e == nil {
		return false
	}

	e.n--
	if e.n > 0 {
		return false
	}

	delete(p.chans[channel], id)

	if len(p.chans[channel]) == 0 {
		delete(p.chans, channel)
	}

	return true
}

func (p *presence) list(channel string) []Member {
	p.mu.Lock()
	defer p.mu.Unlock()

	out := make([]Member, 0, len(p.chans[channel]))
	for _, e := range p.chans[channel] {
		out = append(out, e.m)
	}

	slices.SortFunc(out, func(a, b Member) int { return cmp.Compare(a.ID, b.ID) })

	return out
}
