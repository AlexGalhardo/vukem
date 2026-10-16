package websocket_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cws "github.com/coder/websocket"
	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/broadcast"
	"github.com/AlexGalhardo/vukem/drivers/websocket"
	"github.com/AlexGalhardo/vukem/router"
)

type bcUser struct{ ID string }

type bcUserKey struct{}

func bcCurrentUser(ctx context.Context) (any, error) {
	if u, ok := ctx.Value(bcUserKey{}).(*bcUser); ok {
		return u, nil
	}

	return nil, errors.New("unauthenticated")
}

type postPublished struct {
	ID int `json:"id"`
}

func (postPublished) Channels() []string { return []string{"posts"} }

type secretChanged struct{ Owner string }

func (e secretChanged) Channels() []string { return []string{"private-user." + e.Owner} }
func (secretChanged) BroadcastAs() string  { return "secret.changed" }

// broadcastServer serves websocket.Broadcast at /ws; the X-User header
// stands in for the auth middleware.
func broadcastServer(t *testing.T, maxChannels int) (*broadcast.Server, string) {
	t.Helper()

	b := broadcast.New(broadcast.Config{User: bcCurrentUser, MaxChannels: maxChannels})
	t.Cleanup(b.Close)

	for _, err := range []error{
		b.Public("posts"),
		b.Public("posts.{id}"),
		b.Private("user.{id}", func(_ context.Context, u any, p map[string]string) (bool, error) {
			return u.(*bcUser).ID == p["id"], nil
		}),
		b.Presence("room.{id}", func(_ context.Context, u any, _ map[string]string) (broadcast.Member, bool, error) {
			return broadcast.Member{ID: u.(*bcUser).ID}, true, nil
		}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}

	r := router.New(nil)
	r.Get("/ws", websocket.Broadcast(b))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if id := req.Header.Get("X-User"); id != "" {
			req = req.WithContext(context.WithValue(req.Context(), bcUserKey{}, &bcUser{ID: id}))
		}

		r.ServeHTTP(w, req)
	}))
	t.Cleanup(srv.Close)

	return b, wsURL(srv.URL) + "/ws"
}

func dialAs(t *testing.T, url, userID string) *cws.Conn {
	t.Helper()

	h := http.Header{}
	if userID != "" {
		h.Set("X-User", userID)
	}

	c, resp, err := cws.Dial(t.Context(), url, &cws.DialOptions{HTTPHeader: h})
	if err != nil {
		t.Fatalf("Dial(%s) = %v", url, err)
	}

	if resp.Body != nil {
		_ = resp.Body.Close()
	}

	t.Cleanup(func() { _ = c.CloseNow() })

	return c
}

func sendReq(t *testing.T, c *cws.Conn, typ, channel string) {
	t.Helper()

	b, err := json.Marshal(websocket.Request{Type: typ, Channel: channel})
	if err != nil {
		t.Fatal(err)
	}

	if err := c.Write(t.Context(), cws.MessageText, b); err != nil {
		t.Fatalf("Write = %v", err)
	}
}

func readFrame(t *testing.T, c *cws.Conn) websocket.Frame {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), waitLimit)
	defer cancel()

	_, b, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("Read = %v", err)
	}

	var f websocket.Frame
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}

	return f
}

func expectFrame(t *testing.T, c *cws.Conn, want websocket.Frame) {
	t.Helper()

	if diff := cmp.Diff(want, readFrame(t, c)); diff != "" {
		t.Errorf("frame (-want +got):\n%s", diff)
	}
}

func TestBroadcast_SubscribeReceiveUnsubscribe(t *testing.T) {
	t.Parallel()

	b, url := broadcastServer(t, 0)
	c := dialAs(t, url, "1")

	sendReq(t, c, websocket.TypeSubscribe, "posts")
	expectFrame(t, c, websocket.Frame{Type: websocket.TypeSubscribed, Channel: "posts"})

	sendReq(t, c, websocket.TypeSubscribe, "private-user.1")
	expectFrame(t, c, websocket.Frame{Type: websocket.TypeSubscribed, Channel: "private-user.1"})

	if err := b.Broadcast(t.Context(), postPublished{ID: 4}); err != nil {
		t.Fatal(err)
	}

	expectFrame(t, c, websocket.Frame{
		Type: websocket.TypeEvent, Channel: "posts", Event: "postPublished", Data: json.RawMessage(`{"id":4}`),
	})

	if err := b.Broadcast(t.Context(), secretChanged{Owner: "1"}); err != nil {
		t.Fatal(err)
	}

	if f := readFrame(t, c); f.Event != "secret.changed" || f.Channel != "private-user.1" {
		t.Errorf("private frame = %+v", f)
	}

	sendReq(t, c, websocket.TypeUnsubscribe, "posts")
	expectFrame(t, c, websocket.Frame{Type: websocket.TypeUnsubscribed, Channel: "posts"})

	if err := b.Broadcast(t.Context(), postPublished{ID: 5}); err != nil {
		t.Fatal(err)
	}

	// The next frame answers the next request: nothing arrived for posts.
	sendReq(t, c, websocket.TypeSubscribe, "posts.1")
	expectFrame(t, c, websocket.Frame{Type: websocket.TypeSubscribed, Channel: "posts.1"})
}

func TestBroadcast_PresenceHereJoiningLeaving(t *testing.T) {
	t.Parallel()

	_, url := broadcastServer(t, 0)
	alice := dialAs(t, url, "a")
	bob := dialAs(t, url, "b")

	const room = "presence-room.1"

	sendReq(t, alice, websocket.TypeSubscribe, room)
	expectFrame(t, alice, websocket.Frame{Type: websocket.TypeSubscribed, Channel: room})
	expectFrame(
		t,
		alice,
		websocket.Frame{Type: websocket.TypeHere, Channel: room, Data: json.RawMessage(`[{"id":"a"}]`)},
	)

	sendReq(t, bob, websocket.TypeSubscribe, room)
	expectFrame(t, bob, websocket.Frame{Type: websocket.TypeSubscribed, Channel: room})
	expectFrame(t, bob, websocket.Frame{
		Type: websocket.TypeHere, Channel: room, Data: json.RawMessage(`[{"id":"a"},{"id":"b"}]`),
	})
	expectFrame(
		t,
		alice,
		websocket.Frame{Type: websocket.TypeJoining, Channel: room, Data: json.RawMessage(`{"id":"b"}`)},
	)

	_ = bob.Close(cws.StatusNormalClosure, "")

	expectFrame(
		t,
		alice,
		websocket.Frame{Type: websocket.TypeLeaving, Channel: room, Data: json.RawMessage(`{"id":"b"}`)},
	)
}

func TestBroadcast_ProtocolErrors(t *testing.T) {
	t.Parallel()

	_, url := broadcastServer(t, 1)
	c := dialAs(t, url, "1")
	ctx := t.Context()

	if err := c.Write(ctx, cws.MessageText, []byte("{not json")); err != nil {
		t.Fatal(err)
	}

	expectFrame(t, c, websocket.Frame{Type: websocket.TypeError, Status: 400, Message: "Invalid message."})

	sendReq(t, c, "publish", "posts")
	expectFrame(t, c, websocket.Frame{Type: websocket.TypeError, Status: 400, Message: "Unknown message type."})

	sendReq(t, c, websocket.TypeSubscribe, "posts")
	expectFrame(t, c, websocket.Frame{Type: websocket.TypeSubscribed, Channel: "posts"})

	sendReq(t, c, websocket.TypeSubscribe, "posts") // idempotent
	expectFrame(t, c, websocket.Frame{Type: websocket.TypeSubscribed, Channel: "posts"})

	sendReq(t, c, websocket.TypeSubscribe, "posts.2")
	expectFrame(t, c, websocket.Frame{
		Type: websocket.TypeError, Channel: "posts.2", Status: 400, Message: "Too many channels.",
	})
}

func TestSecurity_A01_BroadcastPrivateChannelsAuthorized(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		user    string
		channel string
		want    websocket.Frame
	}{
		{
			name:    "unauthenticated private",
			channel: "private-user.1",
			want: websocket.Frame{
				Type:    websocket.TypeError,
				Channel: "private-user.1",
				Status:  403,
				Message: "This channel is unauthorized.",
			},
		},
		{
			name:    "unauthenticated presence",
			channel: "presence-room.1",
			want: websocket.Frame{
				Type:    websocket.TypeError,
				Channel: "presence-room.1",
				Status:  403,
				Message: "This channel is unauthorized.",
			},
		},
		{
			name:    "another user's private channel",
			user:    "1",
			channel: "private-user.2",
			want: websocket.Frame{
				Type:    websocket.TypeError,
				Channel: "private-user.2",
				Status:  403,
				Message: "This channel is unauthorized.",
			},
		},
		{
			name:    "undeclared public channel",
			channel: "orders",
			want: websocket.Frame{
				Type:    websocket.TypeError,
				Channel: "orders",
				Status:  403,
				Message: "This channel is unauthorized.",
			},
		},
		{
			name: "injected name is rejected and not echoed", channel: "posts\",\"type\":\"event",
			want: websocket.Frame{Type: websocket.TypeError, Status: 400, Message: "Invalid channel name."},
		},
		{
			name: "overlong name", channel: strings.Repeat("p", 129),
			want: websocket.Frame{Type: websocket.TypeError, Status: 400, Message: "Invalid channel name."},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			b, url := broadcastServer(t, 0)
			c := dialAs(t, url, tt.user)

			sendReq(t, c, websocket.TypeSubscribe, tt.channel)
			expectFrame(t, c, tt.want)

			// Nothing published on the refused channel reaches the client.
			if err := b.Broadcast(t.Context(), secretChanged{Owner: "2"}); err != nil {
				t.Fatal(err)
			}

			if err := b.Publish(t.Context(), "presence-room.1", "chat", "hi"); err != nil {
				t.Fatal(err)
			}

			sendReq(t, c, websocket.TypeSubscribe, "posts")
			expectFrame(t, c, websocket.Frame{Type: websocket.TypeSubscribed, Channel: "posts"})
		})
	}
}

func TestBroadcast_ServerCloseEndsSocket(t *testing.T) {
	t.Parallel()

	b, url := broadcastServer(t, 0)
	c := dialAs(t, url, "1")

	sendReq(t, c, websocket.TypeSubscribe, "posts")
	expectFrame(t, c, websocket.Frame{Type: websocket.TypeSubscribed, Channel: "posts"})

	b.Close()

	ctx, cancel := context.WithTimeout(t.Context(), waitLimit)
	defer cancel()

	if _, _, err := c.Read(ctx); websocket.CloseStatus(err) != websocket.StatusGoingAway {
		t.Errorf("Read after server Close = %v, want going away", err)
	}
}
