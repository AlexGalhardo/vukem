package broadcast_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/broadcast"
	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/events"
	"github.com/AlexGalhardo/vukem/queue"
)

const waitLimit = 5 * time.Second

type user struct{ ID string }

type userKey struct{}

func withUser(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}

	return context.WithValue(ctx, userKey{}, &user{ID: id})
}

func currentUser(ctx context.Context) (any, error) {
	u, ok := ctx.Value(userKey{}).(*user)
	if !ok {
		return nil, errors.New("unauthenticated")
	}

	return u, nil
}

// fakeAuthorizer allows ability "view" on post IDs listed in allowed.
type fakeAuthorizer struct{ allowed map[string]bool }

func (f fakeAuthorizer) Authorize(_ context.Context, ability string, subject any) error {
	if id, ok := subject.(string); ok && ability == "view" && f.allowed[id] {
		return nil
	}

	return errs.New(http.StatusNotFound, "Not Found.")
}

func newServer(t *testing.T, cfg broadcast.Config) *broadcast.Server {
	t.Helper()

	if cfg.User == nil {
		cfg.User = currentUser
	}

	if cfg.Authorizer == nil {
		cfg.Authorizer = fakeAuthorizer{allowed: map[string]bool{"1": true}}
	}

	b := broadcast.New(cfg)
	t.Cleanup(b.Close)

	must(t, b.Public("posts"))
	must(t, b.Public("posts.{id}"))
	must(t, b.Private("user.{id}", func(_ context.Context, u any, p map[string]string) (bool, error) {
		return u.(*user).ID == p["id"], nil
	}))
	must(t, b.Private("broken", func(context.Context, any, map[string]string) (bool, error) {
		return true, errors.New("db down")
	}))
	must(t, b.PrivateGate("post.{id}", "view", func(_ context.Context, p map[string]string) (any, error) {
		return p["id"], nil
	}))
	must(
		t,
		b.Presence("room.{id}", func(_ context.Context, u any, _ map[string]string) (broadcast.Member, bool, error) {
			id := u.(*user).ID
			return broadcast.Member{ID: id, Info: map[string]string{"name": "user " + id}}, true, nil
		}),
	)

	return b
}

func must(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatal(err)
	}
}

type PostPublished struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
}

func (PostPublished) Channels() []string { return []string{"posts"} }

type secretUpdated struct{ Owner, Secret string }

func (e secretUpdated) Channels() []string { return []string{"private-user." + e.Owner} }
func (secretUpdated) BroadcastAs() string  { return "secret.updated" }
func (e secretUpdated) BroadcastWith() any { return map[string]string{"owner": e.Owner} }

func recv(t *testing.T, sub *broadcast.Subscription) broadcast.Message {
	t.Helper()

	select {
	case m, ok := <-sub.Messages():
		if !ok {
			t.Fatal("subscription closed")
		}

		return m
	case <-time.After(waitLimit):
		t.Fatal("no message")
	}

	return broadcast.Message{}
}

func noMessage(t *testing.T, sub *broadcast.Subscription) {
	t.Helper()

	select {
	case m := <-sub.Messages():
		t.Fatalf("unexpected message %+v", m)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestRegister_InvalidPatterns(t *testing.T) {
	t.Parallel()

	b := broadcast.New(broadcast.Config{})
	t.Cleanup(b.Close)

	for _, p := range []string{"", "private-user.{id}", "user.{}", "user.{id}.{id}", "a..b", "a,b", "a b", strings.Repeat("x", 129)} {
		if err := b.Public(p); err == nil {
			t.Errorf("Public(%q) = nil, want error", p)
		}
	}

	if err := b.Private("x", nil); err == nil {
		t.Error("Private with nil rule = nil, want error")
	}

	if err := b.PrivateGate("x", "", nil); err == nil {
		t.Error("PrivateGate without ability = nil, want error")
	}
}

func TestAuthorize(t *testing.T) {
	t.Parallel()

	b := newServer(t, broadcast.Config{})

	tests := []struct {
		name       string
		user       string
		channel    string
		wantStatus int // 0 means allowed
		wantMember broadcast.Member
	}{
		{name: "declared public", channel: "posts"},
		{name: "public with parameter", channel: "posts.9"},
		{name: "undeclared public", channel: "orders.1", wantStatus: 403},
		{name: "private own", user: "1", channel: "private-user.1"},
		{name: "private other user", user: "1", channel: "private-user.2", wantStatus: 403},
		{name: "private unauthenticated", channel: "private-user.1", wantStatus: 403},
		{name: "private rule error", user: "1", channel: "private-broken", wantStatus: 403},
		{name: "private undeclared", user: "1", channel: "private-orders", wantStatus: 403},
		{name: "gate allows", user: "1", channel: "private-post.1"},
		{name: "gate denies (404 hidden as 403)", user: "1", channel: "private-post.2", wantStatus: 403},
		{
			name: "presence member", user: "7", channel: "presence-room.1",
			wantMember: broadcast.Member{ID: "7", Info: map[string]string{"name": "user 7"}},
		},
		{name: "presence unauthenticated", channel: "presence-room.1", wantStatus: 403},
		{name: "empty private name", user: "1", channel: "private-", wantStatus: 400},
		{name: "empty parameter", user: "", channel: "private-user.", wantStatus: 400},
		{name: "empty public segment", channel: "posts.", wantStatus: 400},
		{name: "double dot", channel: "posts..9", wantStatus: 400},
		{name: "newline injection", channel: "posts\nevent: x", wantStatus: 400},
		{name: "comma", channel: "posts,private-user.2", wantStatus: 400},
		{name: "too long", channel: strings.Repeat("p", 129), wantStatus: 400},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m, err := b.Authorize(withUser(t.Context(), tt.user), tt.channel)
			if tt.wantStatus == 0 {
				if err != nil {
					t.Fatalf("Authorize = %v, want nil", err)
				}

				if diff := cmp.Diff(tt.wantMember, m); diff != "" {
					t.Errorf("member (-want +got):\n%s", diff)
				}

				return
			}

			if got := errs.Status(err); got != tt.wantStatus {
				t.Errorf("Authorize status = %d (%v), want %d", got, err, tt.wantStatus)
			}
		})
	}
}

func TestPrivateGate_NoAuthorizerDenies(t *testing.T) {
	t.Parallel()

	b := broadcast.New(broadcast.Config{User: currentUser})
	t.Cleanup(b.Close)
	must(t, b.PrivateGate("admin", "admin", nil))

	if _, err := b.Authorize(withUser(t.Context(), "1"), "private-admin"); errs.Status(err) != 403 {
		t.Errorf("Authorize = %v, want 403", err)
	}
}

func TestBroadcast_MemoryFanOut(t *testing.T) {
	t.Parallel()

	b := newServer(t, broadcast.Config{})
	ctx := withUser(t.Context(), "1")

	a, err := b.Subscribe(ctx, "posts")
	must(t, err)
	c, err := b.Subscribe(ctx, "posts", "private-user.1")
	must(t, err)

	must(t, b.Broadcast(ctx, PostPublished{ID: 3, Title: "Hi"}))

	want := broadcast.Message{Channel: "posts", Event: "PostPublished", Data: json.RawMessage(`{"id":3,"title":"Hi"}`)}
	for _, sub := range []*broadcast.Subscription{a, c} {
		if diff := cmp.Diff(want, recv(t, sub)); diff != "" {
			t.Errorf("message (-want +got):\n%s", diff)
		}
	}

	must(t, b.Broadcast(ctx, secretUpdated{Owner: "1", Secret: "s3cr3t"}))

	want = broadcast.Message{Channel: "private-user.1", Event: "secret.updated", Data: json.RawMessage(`{"owner":"1"}`)}
	if diff := cmp.Diff(want, recv(t, c)); diff != "" {
		t.Errorf("custom name and payload (-want +got):\n%s", diff)
	}

	noMessage(t, a)
}

func TestBroadcast_InvalidEvents(t *testing.T) {
	t.Parallel()

	b := newServer(t, broadcast.Config{})
	ctx := t.Context()

	if err := b.Publish(ctx, "posts", "presence:joining", nil); err == nil {
		t.Error("reserved event name accepted")
	}

	if err := b.Publish(ctx, "posts\n", "x", nil); err == nil {
		t.Error("invalid channel accepted")
	}

	for _, ch := range []string{"private-user.", "presence-room.", "posts.", ".posts", "a..b", "private-."} {
		if err := b.Publish(ctx, ch, "x", nil); err == nil {
			t.Errorf("Publish(%q) accepted a channel with an empty segment", ch)
		}
	}

	if err := b.Broadcast(ctx, noChannels{}); err == nil {
		t.Error("event without channels accepted")
	}
}

type noChannels struct{}

func (noChannels) Channels() []string { return nil }

// fakeBroker is an in-memory sse.Broker shared by several servers, like a
// Redis server shared by several instances.
type fakeBroker struct {
	mu   sync.Mutex
	subs map[string][]chan []byte
}

func (f *fakeBroker) Publish(_ context.Context, channel string, payload []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, ch := range f.subs[channel] {
		ch <- payload
	}

	return nil
}

func (f *fakeBroker) Subscribe(ctx context.Context, channels ...string) (<-chan []byte, error) {
	ch := make(chan []byte, 64)

	f.mu.Lock()
	if f.subs == nil {
		f.subs = map[string][]chan []byte{}
	}

	for _, c := range channels {
		f.subs[c] = append(f.subs[c], ch)
	}
	f.mu.Unlock()

	go func() {
		<-ctx.Done()
		f.mu.Lock()
		defer f.mu.Unlock()

		for _, c := range channels {
			for i, s := range f.subs[c] {
				if s == ch {
					f.subs[c] = append(f.subs[c][:i], f.subs[c][i+1:]...)
					break
				}
			}
		}

		close(ch)
	}()

	return ch, nil
}

func TestBroadcast_BrokerFanOutAcrossInstances(t *testing.T) {
	t.Parallel()

	broker := &fakeBroker{}
	one := newServer(t, broadcast.Config{Broker: broker})
	two := newServer(t, broadcast.Config{Broker: broker})

	sub, err := two.Subscribe(t.Context(), "posts")
	must(t, err)

	must(t, one.Broadcast(t.Context(), PostPublished{ID: 1}))

	if got := recv(t, sub); got.Event != "PostPublished" || got.Channel != "posts" {
		t.Errorf("message = %+v", got)
	}
}

func TestPresence_JoinLeave(t *testing.T) {
	t.Parallel()

	broker := &fakeBroker{}
	one := newServer(t, broadcast.Config{Broker: broker})
	two := newServer(t, broadcast.Config{Broker: broker})

	const room = "presence-room.7"

	alice, err := one.Subscribe(withUser(t.Context(), "1"), room)
	must(t, err)

	bob, err := two.Subscribe(withUser(t.Context(), "2"), room)
	must(t, err)

	got := recv(t, alice)
	want := broadcast.Message{
		Channel: room,
		Event:   broadcast.EventJoining,
		Data:    json.RawMessage(`{"id":"2","info":{"name":"user 2"}}`),
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("joining (-want +got):\n%s", diff)
	}

	// A second connection of the same user is not a new member.
	bob2, err := two.Subscribe(withUser(t.Context(), "2"), room)
	must(t, err)

	members, ok := bob2.Members(room)
	if !ok || len(members) != 1 || members[0].ID != "2" {
		t.Errorf("Members on instance two = %+v, %v; want only the local member 2", members, ok)
	}

	if _, ok := bob.Members("posts"); ok {
		t.Error("Members reported a non-presence channel")
	}

	bob2.Close()
	noMessage(t, alice)
	noMessage(t, bob) // no own join echoed

	bob.Close()

	want.Event = broadcast.EventLeaving
	if diff := cmp.Diff(want, recv(t, alice)); diff != "" {
		t.Errorf("leaving (-want +got):\n%s", diff)
	}
}

func TestSubscribe_ContextCancelLeaves(t *testing.T) {
	t.Parallel()

	b := newServer(t, broadcast.Config{})

	watcher, err := b.Subscribe(withUser(t.Context(), "1"), "presence-room.1")
	must(t, err)

	ctx, cancel := context.WithCancel(withUser(t.Context(), "2"))

	_, err = b.Subscribe(ctx, "presence-room.1")
	must(t, err)

	if got := recv(t, watcher); got.Event != broadcast.EventJoining {
		t.Fatalf("event = %q, want joining", got.Event)
	}

	cancel()

	if got := recv(t, watcher); got.Event != broadcast.EventLeaving {
		t.Errorf("event = %q, want leaving", got.Event)
	}
}

func TestSubscribe_Limits(t *testing.T) {
	t.Parallel()

	b := newServer(t, broadcast.Config{MaxChannels: 2})

	if _, err := b.Subscribe(t.Context(), "posts.1", "posts.2", "posts.3"); errs.Status(err) != 400 {
		t.Errorf("over the limit = %v, want 400", err)
	}

	if _, err := b.Subscribe(t.Context(), "posts.1", "posts.1", " posts.2 "); err != nil {
		t.Errorf("duplicates count once: %v", err)
	}

	if _, err := b.Subscribe(t.Context()); errs.Status(err) != 400 {
		t.Errorf("no channels = %v, want 400", err)
	}
}

func TestListener_BroadcastsFromEventsBus(t *testing.T) {
	t.Parallel()

	b := newServer(t, broadcast.Config{})
	bus := events.New(nil, 1)
	bus.Register(broadcast.Listener[PostPublished](b))

	sub, err := b.Subscribe(t.Context(), "posts")
	must(t, err)

	must(t, bus.Dispatch(t.Context(), PostPublished{ID: 5}))

	if got := recv(t, sub); string(got.Data) != `{"id":5,"title":""}` {
		t.Errorf("data = %s", got.Data)
	}
}

func TestQueued_BroadcastsFromQueueJob(t *testing.T) {
	t.Parallel()

	b := newServer(t, broadcast.Config{})
	q := queue.New(queue.Sync{}, nil)
	bus := events.New(nil, 1)
	bus.Register(broadcast.Queued[PostPublished](b, "broadcast.post_published"))
	must(t, q.Register(bus.Jobs()...))
	bus.SetQueue(func() (events.Enqueuer, error) { return q, nil })

	sub, err := b.Subscribe(t.Context(), "posts")
	must(t, err)

	must(t, bus.Dispatch(t.Context(), PostPublished{ID: 6, Title: "q"}))

	if got := recv(t, sub); string(got.Data) != `{"id":6,"title":"q"}` {
		t.Errorf("data = %s", got.Data)
	}
}

// sseServer serves b.SSEHandler with the user taken from the X-User header.
func sseServer(t *testing.T, b *broadcast.Server) *httptest.Server {
	t.Helper()

	h := b.SSEHandler()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(withUser(r.Context(), r.Header.Get("X-User"))))
	}))
	t.Cleanup(srv.Close)

	return srv
}

func openSSE(t *testing.T, srv *httptest.Server, userID string, channels ...string) *http.Response {
	t.Helper()

	req, err := http.NewRequestWithContext(
		t.Context(), http.MethodGet,
		srv.URL+"/broadcast/sse?channels="+url.QueryEscape(strings.Join(channels, ",")), http.NoBody,
	)
	must(t, err)

	req.Header.Set("X-User", userID)

	resp, err := srv.Client().Do(req)
	must(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })

	return resp
}

// nextEvent reads the next SSE event (skipping comments).
func nextEvent(t *testing.T, r *bufio.Reader) (string, broadcast.Message) {
	t.Helper()

	var name string

	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("read stream: %v", err)
		}

		line = strings.TrimRight(line, "\n")

		switch {
		case strings.HasPrefix(line, "event: "):
			name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			var m broadcast.Message
			must(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &m))

			return name, m
		}
	}
}

func TestSSE_EndToEnd(t *testing.T) {
	t.Parallel()

	b := newServer(t, broadcast.Config{})
	srv := sseServer(t, b)

	resp := openSSE(t, srv, "1", "posts", "private-user.1", "presence-room.3")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("status %d, content type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}

	r := bufio.NewReader(resp.Body)

	name, here := nextEvent(t, r)
	if name != broadcast.EventHere || here.Channel != "presence-room.3" ||
		string(here.Data) != `[{"id":"1","info":{"name":"user 1"}}]` {
		t.Errorf("here = %s %+v", name, here)
	}

	must(t, b.Broadcast(t.Context(), secretUpdated{Owner: "2"})) // another user's channel
	must(t, b.Broadcast(t.Context(), secretUpdated{Owner: "1"}))
	must(t, b.Broadcast(t.Context(), PostPublished{ID: 8, Title: "t"}))

	name, m := nextEvent(t, r)
	if name != "secret.updated" || m.Channel != "private-user.1" {
		t.Errorf("first event = %s %+v, want own secret.updated", name, m)
	}

	name, m = nextEvent(t, r)
	if name != "PostPublished" || string(m.Data) != `{"id":8,"title":"t"}` {
		t.Errorf("second event = %s %+v", name, m)
	}
}

func TestSecurity_A01_BroadcastPrivateChannelsAuthorized(t *testing.T) {
	t.Parallel()

	b := newServer(t, broadcast.Config{})
	srv := sseServer(t, b)

	tests := []struct {
		name     string
		user     string
		channels []string
		want     int
	}{
		{name: "unauthenticated private", channels: []string{"private-user.1"}, want: 403},
		{name: "unauthenticated presence", channels: []string{"presence-room.1"}, want: 403},
		{name: "another user's private channel", user: "1", channels: []string{"private-user.2"}, want: 403},
		{
			name:     "one forbidden channel refuses all",
			user:     "1",
			channels: []string{"posts", "private-user.2"},
			want:     403,
		},
		{name: "gate denial", user: "1", channels: []string{"private-post.2"}, want: 403},
		{name: "undeclared public channel", channels: []string{"orders"}, want: 403},
		{name: "injected newline", channels: []string{"posts\r\nevent: x"}, want: 400},
		{name: "injected space", channels: []string{"posts x"}, want: 400},
		{name: "overlong name", channels: []string{strings.Repeat("a", 200)}, want: 400},
		{name: "no channels", want: 400},
		{name: "empty segment", user: "1", channels: []string{"private-user."}, want: 400},
		{name: "own private channel", user: "1", channels: []string{"private-user.1"}, want: 200},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			resp := openSSE(t, srv, tt.user, tt.channels...)
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != tt.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.want)
			}

			if tt.want != 200 && strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
				t.Error("refused subscription started a stream")
			}
		})
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"?channels=posts", http.NoBody)
	must(t, err)

	resp, err := srv.Client().Do(req)
	must(t, err)
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST status = %d, want 405", resp.StatusCode)
	}
}
