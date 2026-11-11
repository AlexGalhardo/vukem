package broadcast_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AlexGalhardo/vukem/broadcast"
	"github.com/AlexGalhardo/vukem/broadcast/presencetest"
)

func TestMemoryPresenceConformance(t *testing.T) {
	t.Parallel()

	presencetest.Run(t, broadcast.NewMemoryPresence(), presencetest.Options{})
}

// recordingPresence wraps the memory store: it can fail a Join, and it
// counts Touch and Leave calls.
type recordingPresence struct {
	broadcast.PresenceStore

	mu      sync.Mutex
	failOn  string // Join on this channel fails
	touches int
	leaves  []string
}

func (r *recordingPresence) Join(ctx context.Context, channel, conn string, m broadcast.Member) (bool, error) {
	if channel == r.failOn {
		return false, errors.New("store down")
	}

	return r.PresenceStore.Join(ctx, channel, conn, m)
}

func (r *recordingPresence) Leave(ctx context.Context, channel, conn, id string) (bool, error) {
	r.mu.Lock()
	r.leaves = append(r.leaves, channel)
	r.mu.Unlock()

	return r.PresenceStore.Leave(ctx, channel, conn, id)
}

func (r *recordingPresence) Touch(ctx context.Context, channel, conn string) error {
	r.mu.Lock()
	r.touches++
	r.mu.Unlock()

	return r.PresenceStore.Touch(ctx, channel, conn)
}

func (r *recordingPresence) counts() (touches int, leaves []string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.touches, slices.Clone(r.leaves)
}

func ids(members []broadcast.Member) []string {
	out := make([]string, len(members))
	for i, m := range members {
		out[i] = m.ID
	}

	return out
}

func TestHereIsGlobalWithASharedStore(t *testing.T) {
	t.Parallel()

	broker, store := &fakeBroker{}, broadcast.NewMemoryPresence()
	one := newServer(t, broadcast.Config{Broker: broker, Presence: store})
	two := newServer(t, broadcast.Config{Broker: broker, Presence: store})

	const room = "presence-room.7"

	alice, err := one.Subscribe(withUser(t.Context(), "1"), room)
	must(t, err)

	bob, err := two.Subscribe(withUser(t.Context(), "2"), room)
	must(t, err)

	for name, sub := range map[string]*broadcast.Subscription{"instance one": alice, "instance two": bob} {
		members, err := sub.Here(t.Context(), room)
		if err != nil || !slices.Equal(ids(members), []string{"1", "2"}) {
			t.Errorf("Here on %s = %v, %v, want both members", name, ids(members), err)
		}
	}
}

func TestJoiningIsAnnouncedOncePerMemberAcrossInstances(t *testing.T) {
	t.Parallel()

	broker, store := &fakeBroker{}, broadcast.NewMemoryPresence()
	one := newServer(t, broadcast.Config{Broker: broker, Presence: store})
	two := newServer(t, broadcast.Config{Broker: broker, Presence: store})
	three := newServer(t, broadcast.Config{Broker: broker, Presence: store})

	const room = "presence-room.7"

	watcher, err := three.Subscribe(withUser(t.Context(), "9"), room)
	must(t, err)

	first, err := one.Subscribe(withUser(t.Context(), "1"), room)
	must(t, err)

	if got := recv(t, watcher); got.Event != broadcast.EventJoining {
		t.Fatalf("event = %q, want joining", got.Event)
	}

	second, err := two.Subscribe(withUser(t.Context(), "1"), room)
	must(t, err)

	noMessage(t, watcher) // the same user on another instance is not a new member

	first.Close()
	noMessage(t, watcher) // one connection is still open

	second.Close()

	if got := recv(t, watcher); got.Event != broadcast.EventLeaving {
		t.Errorf("event = %q, want leaving after the last connection", got.Event)
	}
}

func TestSecurity_A01_SubscribeFailsClosedWhenPresenceStoreFails(t *testing.T) {
	t.Parallel()

	store := &recordingPresence{PresenceStore: broadcast.NewMemoryPresence(), failOn: "presence-room.1"}
	b := newServer(t, broadcast.Config{Presence: store})

	sub, err := b.Subscribe(withUser(t.Context(), "1"), "presence-room.1")
	if err == nil || sub != nil {
		t.Fatalf("Subscribe with a failing store = %v, %v, want an error and no subscription", sub, err)
	}

	if !strings.Contains(err.Error(), "presence store") {
		t.Errorf("error = %v, want it to name the presence store", err)
	}

	// Nothing was left behind: another subscriber sees no joining event
	// and the failed member is not listed.
	watcher, err := b.Subscribe(withUser(t.Context(), "2"), "presence-room.2")
	must(t, err)

	members, err := watcher.Here(t.Context(), "presence-room.2")
	if err != nil || !slices.Equal(ids(members), []string{"2"}) {
		t.Errorf("Here = %v, %v, want only the working member", ids(members), err)
	}

	noMessage(t, watcher)
}

func TestSubscribeUndoesJoinsWhenALaterJoinFails(t *testing.T) {
	t.Parallel()

	store := &recordingPresence{PresenceStore: broadcast.NewMemoryPresence(), failOn: "presence-room.2"}
	b := newServer(t, broadcast.Config{Presence: store})

	_, err := b.Subscribe(withUser(t.Context(), "1"), "presence-room.1", "presence-room.2")
	if err == nil {
		t.Fatal("Subscribe = nil error, want the failing join")
	}

	// The failing channel is left too: a Join that timed out may have written.
	if _, leaves := store.counts(); !slices.Equal(leaves, []string{"presence-room.1", "presence-room.2"}) {
		t.Errorf("Leave calls = %v, want both channels left", leaves)
	}

	members, err := store.Members(t.Context(), "presence-room.1")
	if err != nil || len(members) != 0 {
		t.Errorf("members after the undo = %v, %v, want none", members, err)
	}
}

func TestHeartbeatTouchesLiveConnections(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		store := &recordingPresence{PresenceStore: broadcast.NewMemoryPresence()}
		b := newServer(t, broadcast.Config{Presence: store, PresenceHeartbeat: time.Second})

		sub, err := b.Subscribe(withUser(t.Context(), "1"), "presence-room.1")
		must(t, err)

		time.Sleep(3500 * time.Millisecond)
		synctest.Wait()

		if n, _ := store.counts(); n != 3 {
			t.Errorf("Touch calls after 3.5s = %d, want 3", n)
		}

		sub.Close()
		time.Sleep(5 * time.Second)
		synctest.Wait()

		if n, _ := store.counts(); n != 3 {
			t.Errorf("Touch calls after Close = %d, want still 3", n)
		}
	})
}

func TestNoHeartbeatWithoutASharedStore(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		b := broadcast.New(broadcast.Config{PresenceHeartbeat: time.Second, User: currentUser})
		must(
			t,
			b.Presence(
				"room.{id}",
				func(_ context.Context, u any, _ map[string]string) (broadcast.Member, bool, error) {
					return broadcast.Member{ID: u.(*user).ID}, true, nil
				},
			),
		)

		sub, err := b.Subscribe(withUser(t.Context(), "1"), "presence-room.1")
		must(t, err)

		time.Sleep(5 * time.Second)
		synctest.Wait()

		// A leftover ticker goroutine would keep the bubble alive after Close.
		sub.Close()
		b.Close()
	})
}

func TestMembersIsADeprecatedViewOfHere(t *testing.T) {
	t.Parallel()

	b := newServer(t, broadcast.Config{})
	sub, err := b.Subscribe(withUser(t.Context(), "1"), "presence-room.1")
	must(t, err)

	here, err := sub.Here(t.Context(), "presence-room.1")
	must(t, err)

	members, ok := sub.Members("presence-room.1")
	if !ok || !slices.Equal(ids(members), ids(here)) {
		t.Errorf("Members = %v, %v, want the list Here returned (%v)", ids(members), ok, ids(here))
	}

	if _, err := sub.Here(t.Context(), "posts"); !errors.Is(err, broadcast.ErrInvalidChannel) {
		t.Errorf("Here(non-presence channel) = %v, want ErrInvalidChannel", err)
	}
}

// gatedPresence is a memory store whose calls can be held, to put a Touch
// in flight or make a Leave slow, and that logs the order of the calls.
type gatedPresence struct {
	broadcast.PresenceStore

	mu        sync.Mutex
	events    []string
	gate      chan struct{} // a Touch waits for it to close or for its context
	slowLeave bool          // a Leave takes 2ms, and its deadline is recorded
	deadlines []time.Time
	panicOn   string // a Join on this channel panics
}

func (g *gatedPresence) log(e string) {
	g.mu.Lock()
	g.events = append(g.events, e)
	g.mu.Unlock()
}

func (g *gatedPresence) snapshot() []string {
	g.mu.Lock()
	defer g.mu.Unlock()

	return slices.Clone(g.events)
}

func (g *gatedPresence) Join(ctx context.Context, channel, conn string, m broadcast.Member) (bool, error) {
	g.log("join " + channel)

	if channel == g.panicOn {
		panic("store exploded")
	}

	return g.PresenceStore.Join(ctx, channel, conn, m)
}

func (g *gatedPresence) Leave(ctx context.Context, channel, conn, id string) (bool, error) {
	g.log("leave " + channel)

	if g.slowLeave {
		d, _ := ctx.Deadline()

		g.mu.Lock()
		g.deadlines = append(g.deadlines, d)
		g.mu.Unlock()

		<-time.After(2 * time.Millisecond)
	}

	return g.PresenceStore.Leave(ctx, channel, conn, id)
}

func (g *gatedPresence) Touch(ctx context.Context, channel, conn string) error {
	g.log("touch-start")

	defer g.log("touch-end")

	if g.gate == nil {
		return g.PresenceStore.Touch(ctx, channel, conn)
	}

	select {
	case <-g.gate:
		return g.PresenceStore.Touch(ctx, channel, conn)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func indexOf(events []string, e string) int { return slices.Index(events, e) }

func TestCloseWaitsForAnInFlightHeartbeat(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		store := &gatedPresence{PresenceStore: broadcast.NewMemoryPresence(), gate: make(chan struct{})}
		b := newServer(t, broadcast.Config{Presence: store, PresenceHeartbeat: time.Second})

		sub, err := b.Subscribe(withUser(t.Context(), "1"), "presence-room.1")
		must(t, err)

		time.Sleep(1500 * time.Millisecond)
		synctest.Wait() // the first Touch is in flight, held by the gate

		sub.Close()
		synctest.Wait()

		events := store.snapshot()
		end, leave := indexOf(events, "touch-end"), indexOf(events, "leave presence-room.1")

		if end < 0 || leave < 0 || end > leave {
			t.Errorf("events = %v, want the in-flight Touch to finish before the Leave", events)
		}
	})
}

func TestConcurrentCloseIsSafe(t *testing.T) {
	t.Parallel()

	store := &gatedPresence{PresenceStore: broadcast.NewMemoryPresence()}
	b := newServer(t, broadcast.Config{Presence: store, PresenceHeartbeat: time.Millisecond})

	sub, err := b.Subscribe(withUser(t.Context(), "1"), "presence-room.1")
	must(t, err)

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(sub.Close)
	}

	wg.Wait()

	leaves := 0

	for _, e := range store.snapshot() {
		if e == "leave presence-room.1" {
			leaves++
		}
	}

	if leaves != 1 {
		t.Errorf("Leave ran %d times for concurrent Close calls, want once", leaves)
	}
}

func TestCloseUsesOneTimeoutForEveryLeave(t *testing.T) {
	t.Parallel()

	store := &gatedPresence{PresenceStore: broadcast.NewMemoryPresence(), slowLeave: true}
	b := newServer(t, broadcast.Config{Presence: store})

	sub, err := b.Subscribe(withUser(t.Context(), "1"), "presence-room.1", "presence-room.2", "presence-room.3")
	must(t, err)

	sub.Close()

	store.mu.Lock()
	defer store.mu.Unlock()

	if len(store.deadlines) != 3 {
		t.Fatalf("Leave calls = %d, want 3", len(store.deadlines))
	}

	// A timeout per channel would give each Leave a later deadline.
	for _, d := range store.deadlines[1:] {
		if !d.Equal(store.deadlines[0]) {
			t.Errorf("Leave deadlines = %v, want one shared deadline", store.deadlines)
			break
		}
	}
}

func TestSubscribeUndoesJoinsWhenTheStorePanics(t *testing.T) {
	t.Parallel()

	store := &gatedPresence{PresenceStore: broadcast.NewMemoryPresence(), panicOn: "presence-room.2"}
	b := newServer(t, broadcast.Config{Presence: store})

	func() {
		defer func() { _ = recover() }()

		_, _ = b.Subscribe(withUser(t.Context(), "1"), "presence-room.1", "presence-room.2")
	}()

	events := store.snapshot()
	if indexOf(events, "leave presence-room.1") < 0 || indexOf(events, "leave presence-room.2") < 0 {
		t.Errorf("events = %v, want both channels left after the store panicked", events)
	}

	members, err := store.Members(t.Context(), "presence-room.1")
	if err != nil || len(members) != 0 {
		t.Errorf("members after the panic = %v, %v, want none", members, err)
	}
}
