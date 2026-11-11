package broadcast

import (
	"cmp"
	"context"
	"slices"
	"sync"
)

// PresenceStore tracks who is on presence channels. The default lives in
// the process; a shared one (drivers/redis) makes "here" lists and
// joining/leaving global across instances. conn identifies one connection
// (a Subscription); a member is present while at least one of its
// connections is.
type PresenceStore interface {
	// Join records conn as a connection of m on channel and reports
	// whether it is m's first live connection there.
	Join(ctx context.Context, channel, conn string, m Member) (first bool, err error)
	// Leave removes conn and reports whether memberID has no live
	// connection left on channel. Leaving an unknown conn reports false.
	Leave(ctx context.Context, channel, conn, memberID string) (last bool, err error)
	// Touch keeps conn alive in a store that expires connections. A store
	// without expiry does nothing.
	Touch(ctx context.Context, channel, conn string) error
	// Members lists the members with a live connection on channel, one
	// entry per ID, sorted by ID.
	Members(ctx context.Context, channel string) ([]Member, error)
}

// NewMemoryPresence returns the in-process store, the default. With several
// instances behind a balancer each one then knows only its own members:
// "here" lists the members connected to the instance serving the client.
// Use a shared store (Config.Presence) for a cluster-wide view.
func NewMemoryPresence() PresenceStore {
	return &memoryPresence{chans: map[string]map[string]*presenceEntry{}}
}

// memoryPresence keeps, per channel, each member with the set of its
// connections: a member joins on its first connection and leaves with its
// last.
type memoryPresence struct {
	mu    sync.Mutex
	chans map[string]map[string]*presenceEntry
}

type presenceEntry struct {
	m     Member
	conns map[string]struct{}
}

func (p *memoryPresence) Join(_ context.Context, channel, conn string, m Member) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	set := p.chans[channel]
	if set == nil {
		set = map[string]*presenceEntry{}
		p.chans[channel] = set
	}

	e := set[m.ID]
	if e == nil {
		set[m.ID] = &presenceEntry{m: m, conns: map[string]struct{}{conn: {}}}
		return true, nil
	}

	e.conns[conn] = struct{}{}
	e.m = m

	return false, nil
}

func (p *memoryPresence) Leave(_ context.Context, channel, conn, memberID string) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	e := p.chans[channel][memberID]
	if e == nil {
		return false, nil
	}

	if _, ok := e.conns[conn]; !ok {
		return false, nil
	}

	delete(e.conns, conn)

	if len(e.conns) > 0 {
		return false, nil
	}

	delete(p.chans[channel], memberID)

	if len(p.chans[channel]) == 0 {
		delete(p.chans, channel)
	}

	return true, nil
}

// Touch does nothing: a connection of the memory store lives until Leave.
func (*memoryPresence) Touch(context.Context, string, string) error { return nil }

func (p *memoryPresence) Members(_ context.Context, channel string) ([]Member, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	out := make([]Member, 0, len(p.chans[channel]))
	for _, e := range p.chans[channel] {
		out = append(out, e.m)
	}

	slices.SortFunc(out, func(a, b Member) int { return cmp.Compare(a.ID, b.ID) })

	return out, nil
}
