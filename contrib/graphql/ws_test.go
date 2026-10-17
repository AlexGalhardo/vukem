package graphql_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/broadcast"
	"github.com/AlexGalhardo/vukem/contrib/graphql"
)

// fakeConn is an in-memory WebSocket: the test is the client.
type closeCode int

var errConnClosed = errors.New("fake: connection closed")

type fakeConn struct {
	ctx   context.Context //nolint:containedctx // test double of the connection's lifetime
	leave context.CancelFunc
	proto string
	in    chan string
	out   chan string
	done  chan struct{}

	mu     sync.Mutex
	once   sync.Once
	code   closeCode
	reason string
}

func newFakeConn(t *testing.T, proto string) *fakeConn {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())

	return &fakeConn{
		ctx: ctx, leave: cancel, proto: proto,
		in: make(chan string), out: make(chan string, 64), done: make(chan struct{}),
	}
}

func (c *fakeConn) Context() context.Context { return c.ctx }
func (c *fakeConn) Subprotocol() string      { return c.proto }

func (c *fakeConn) ReadText(ctx context.Context) (string, error) {
	select {
	case s := <-c.in:
		return s, nil
	case <-ctx.Done():
		return "", ctx.Err()
	case <-c.done:
		return "", errConnClosed
	}
}

func (c *fakeConn) WriteText(ctx context.Context, s string) error {
	select {
	case <-c.done:
		return errConnClosed
	default:
	}

	select {
	case c.out <- s:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return errConnClosed
	}
}

func (c *fakeConn) Close(code closeCode, reason string) error {
	c.once.Do(func() {
		c.mu.Lock()
		c.code, c.reason = code, reason
		c.mu.Unlock()
		close(c.done)
	})

	return nil
}

func (c *fakeConn) closed() (closeCode, string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.code, c.reason
}

type wsClient struct {
	t    *testing.T
	conn *fakeConn
	done chan struct{}
}

type wsMsg struct {
	ID      string          `json:"id,omitempty"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// dial runs ServeWS on a fake connection; the test must run inside a
// synctest bubble, which also proves no goroutine outlives the session.
func dial(t *testing.T, s *graphql.Schema, cfg graphql.WSConfig, proto string) *wsClient {
	t.Helper()

	c := &wsClient{t: t, conn: newFakeConn(t, proto), done: make(chan struct{})}

	go func() {
		defer close(c.done)

		if err := graphql.ServeWS(s, c.conn, cfg); err != nil {
			t.Errorf("ServeWS() = %v, want nil", err)
		}
	}()

	t.Cleanup(func() {
		c.conn.leave()
		<-c.done
	})

	return c
}

func (c *wsClient) send(raw string) {
	c.t.Helper()

	select {
	case c.conn.in <- raw:
	case <-c.conn.done:
		c.t.Fatalf("send %s: connection closed", raw)
	}
}

func (c *wsClient) sendJSON(v any) {
	c.t.Helper()

	b, err := json.Marshal(v)
	if err != nil {
		c.t.Fatal(err)
	}

	c.send(string(b))
}

func (c *wsClient) recv() wsMsg {
	c.t.Helper()

	var s string

	select {
	case s = <-c.conn.out:
	case <-c.conn.done:
		code, reason := c.conn.closed()
		c.t.Fatalf("recv: connection closed %d %q", code, reason)
	}

	var m wsMsg
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		c.t.Fatalf("recv %s: %v", s, err)
	}

	return m
}

// init performs connection_init / connection_ack.
func (c *wsClient) init(payload map[string]any) {
	c.t.Helper()
	c.sendJSON(map[string]any{"type": "connection_init", "payload": payload})

	if m := c.recv(); m.Type != "connection_ack" {
		c.t.Fatalf("after connection_init got %+v, want connection_ack", m)
	}
}

func (c *wsClient) subscribe(id, query string) {
	c.t.Helper()
	c.sendJSON(map[string]any{"id": id, "type": "subscribe", "payload": map[string]any{"query": query}})
}

// wantClosed waits for the server to close with code.
func (c *wsClient) wantClosed(code closeCode) {
	c.t.Helper()
	<-c.done

	if got, reason := c.conn.closed(); got != code {
		c.t.Errorf("close code = %d (%q), want %d", got, reason, code)
	}
}

func (c *wsClient) wantNext(id, data string) {
	c.t.Helper()

	m := c.recv()
	if m.Type != "next" || m.ID != id {
		c.t.Fatalf("got %+v, want next for %s", m, id)
	}

	var resp struct {
		Data   json.RawMessage `json:"data"`
		Errors []any           `json:"errors"`
	}
	if err := json.Unmarshal(m.Payload, &resp); err != nil {
		c.t.Fatal(err)
	}

	if string(resp.Data) != data || len(resp.Errors) > 0 {
		c.t.Fatalf("next payload = %s, want data %s", m.Payload, data)
	}
}

func (c *wsClient) wantComplete(id string) {
	c.t.Helper()

	if m := c.recv(); m.Type != "complete" || m.ID != id {
		c.t.Fatalf("got %+v, want complete for %s", m, id)
	}
}

// errorCodes returns the extensions.code of every error in an "error"
// message payload or a "next" payload's errors.
func errorCodes(t *testing.T, m wsMsg) []string {
	t.Helper()

	type gerr struct {
		Extensions map[string]any `json:"extensions"`
	}

	var errs []gerr

	if m.Type == "next" {
		var resp struct {
			Errors []gerr `json:"errors"`
		}
		if err := json.Unmarshal(m.Payload, &resp); err != nil {
			t.Fatal(err)
		}

		errs = resp.Errors
	} else if err := json.Unmarshal(m.Payload, &errs); err != nil {
		t.Fatalf("error payload %s: %v", m.Payload, err)
	}

	codes := make([]string, len(errs))
	for i, e := range errs {
		codes[i], _ = e.Extensions["code"].(string)
	}

	return codes
}

const subSDL = `
type Query { hello: String! }
type Mutation { createPost(title: String!): Post! }
type Subscription {
  countdown(from: Int!): Int!
  postCreated: Post!
  adminFeed: Post!
  broken: Int!
}
type Post { id: ID! title: String! }
`

type wsPost struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type userKey struct{}

// subApp builds a broadcast server whose "private-admin" channel only the
// "admin" user may join, and a schema whose mutation publishes on "posts".
func subApp(t *testing.T, opts ...graphql.Option) (*graphql.Schema, *broadcast.Server) {
	t.Helper()

	b := broadcast.New(broadcast.Config{User: func(ctx context.Context) (any, error) {
		u, ok := ctx.Value(userKey{}).(string)
		if !ok {
			return nil, errors.New("no user")
		}

		return u, nil
	}})
	t.Cleanup(b.Close)

	if err := b.Public("posts"); err != nil {
		t.Fatal(err)
	}

	if err := b.Private("admin", func(_ context.Context, u any, _ map[string]string) (bool, error) {
		return u == "admin", nil
	}); err != nil {
		t.Fatal(err)
	}

	type countArgs struct {
		From int `json:"from"`
	}

	type titleArgs struct {
		Title string `json:"title"`
	}

	chans := func(names ...string) func(context.Context, graphql.NoArgs) ([]string, error) {
		return func(context.Context, graphql.NoArgs) ([]string, error) { return names, nil }
	}

	opts = append([]graphql.Option{
		graphql.Query("hello", func(context.Context, graphql.NoArgs) (string, error) { return "world", nil }),
		graphql.Mutation("createPost", func(ctx context.Context, a titleArgs) (wsPost, error) {
			p := wsPost{ID: "1", Title: a.Title}
			return p, b.Publish(ctx, "posts", "post.created", p)
		}),
		graphql.Subscription("countdown", func(ctx context.Context, a countArgs) (<-chan int, error) {
			ch := make(chan int)

			go func() {
				defer close(ch)

				for i := a.From; i > 0; i-- {
					select {
					case ch <- i:
					case <-ctx.Done():
						return
					}
				}
			}()

			return ch, nil
		}),
		graphql.Subscription(
			"postCreated",
			graphql.FromBroadcast[graphql.NoArgs, wsPost](b, chans("posts"), "post.created"),
		),
		graphql.Subscription("adminFeed", graphql.FromBroadcast[graphql.NoArgs, wsPost](b, chans("private-admin"))),
		graphql.Subscription("broken", func(context.Context, graphql.NoArgs) (<-chan int, error) {
			return nil, errors.New("database password is hunter2")
		}),
	}, opts...)

	s, err := graphql.New(subSDL, opts...)
	if err != nil {
		t.Fatal(err)
	}

	return s, b
}

func withUser(u string) func(context.Context, map[string]any) (context.Context, error) {
	return func(ctx context.Context, _ map[string]any) (context.Context, error) {
		return context.WithValue(ctx, userKey{}, u), nil
	}
}

func TestServeWSHandshake(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		s, _ := subApp(t)
		c := dial(t, s, graphql.WSConfig{}, graphql.WSProtocol)

		c.send(`{"type":"ping"}`)

		if m := c.recv(); m.Type != "pong" {
			t.Fatalf("ping before init got %+v, want pong", m)
		}

		c.init(nil)
		c.send(`{"type":"ping","payload":{"x":1}}`)

		if m := c.recv(); m.Type != "pong" {
			t.Fatalf("ping got %+v, want pong", m)
		}

		c.send(`{"type":"pong"}`) // unsolicited pongs are ignored
		c.subscribe("1", `{ hello }`)
		c.wantNext("1", `{"hello":"world"}`)
		c.wantComplete("1")
	})
}

func TestServeWSCloseCodes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		proto string
		cfg   graphql.WSConfig
		run   func(c *wsClient)
		want  closeCode
	}{
		{"wrong subprotocol", "graphql-ws", graphql.WSConfig{}, func(*wsClient) {}, graphql.CloseSubprotocol},
		{"init timeout", graphql.WSProtocol, graphql.WSConfig{}, func(*wsClient) {}, graphql.CloseInitTimeout},
		{"subscribe before ack", graphql.WSProtocol, graphql.WSConfig{}, func(c *wsClient) {
			c.subscribe("1", `{ hello }`)
		}, graphql.CloseUnauthorized},
		{"second connection_init", graphql.WSProtocol, graphql.WSConfig{}, func(c *wsClient) {
			c.init(nil)
			c.send(`{"type":"connection_init"}`)
		}, graphql.CloseTooManyInitRequest},
		{"invalid JSON", graphql.WSProtocol, graphql.WSConfig{}, func(c *wsClient) {
			c.send(`{"type":`)
		}, graphql.CloseBadRequest},
		{"unknown type", graphql.WSProtocol, graphql.WSConfig{}, func(c *wsClient) {
			c.init(nil)
			c.send(`{"type":"start","id":"1"}`)
		}, graphql.CloseBadRequest},
		{"subscribe without id", graphql.WSProtocol, graphql.WSConfig{}, func(c *wsClient) {
			c.init(nil)
			c.send(`{"type":"subscribe","payload":{"query":"{ hello }"}}`)
		}, graphql.CloseBadRequest},
		{"init payload not an object", graphql.WSProtocol, graphql.WSConfig{}, func(c *wsClient) {
			c.send(`{"type":"connection_init","payload":[1]}`)
		}, graphql.CloseBadRequest},
		{"duplicate subscriber", graphql.WSProtocol, graphql.WSConfig{}, func(c *wsClient) {
			c.init(nil)
			c.subscribe("1", `subscription { postCreated { id } }`)
			c.subscribe("1", `subscription { postCreated { id } }`)
		}, graphql.CloseSubscriberExists},
		{"OnConnect refuses", graphql.WSProtocol, graphql.WSConfig{
			OnConnect: func(context.Context, map[string]any) (context.Context, error) {
				return nil, errors.New("bad token")
			},
		}, func(c *wsClient) {
			c.sendJSON(map[string]any{"type": "connection_init", "payload": map[string]any{"token": "x"}})
		}, graphql.CloseForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				s, _ := subApp(t)
				c := dial(t, s, tt.cfg, tt.proto)
				tt.run(c)
				c.wantClosed(tt.want)
			})
		})
	}
}

func TestServeWSInitTimeoutIsConfigurable(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		s, _ := subApp(t)
		start := time.Now()
		c := dial(t, s, graphql.WSConfig{InitTimeout: 2 * time.Second}, graphql.WSProtocol)
		c.wantClosed(graphql.CloseInitTimeout)

		if got := time.Since(start); got != 2*time.Second {
			t.Errorf("closed after %v, want 2s", got)
		}
	})
}

func TestServeWSSubscriptionStream(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		s, _ := subApp(t)
		c := dial(t, s, graphql.WSConfig{}, graphql.WSProtocol)
		c.init(nil)

		c.subscribe("c", `subscription Count($n: Int!) { countdown(from: $n) }`)

		if m := c.recv(); m.Type != "error" {
			t.Fatalf("missing variable got %+v, want error", m)
		} else if diff := cmp.Diff([]string{graphql.CodeBadUserInput}, errorCodes(t, m)); diff != "" {
			t.Fatalf("error codes (-want +got):\n%s", diff)
		}

		c.sendJSON(map[string]any{"id": "c", "type": "subscribe", "payload": map[string]any{
			"query": `subscription Count($n: Int!) { countdown(from: $n) }`, "variables": map[string]any{"n": 3},
		}})

		for _, n := range []int{3, 2, 1} {
			c.wantNext("c", fmt.Sprintf(`{"countdown":%d}`, n))
		}

		c.wantComplete("c")
	})
}

func TestServeWSRequestErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		cfg   graphql.WSConfig
		opts  []graphql.Option
		query string
		want  string
	}{
		{"parse", graphql.WSConfig{}, nil, `subscription {`, graphql.CodeParseFailed},
		{
			"validation",
			graphql.WSConfig{},
			nil,
			`subscription { nope }`,
			graphql.CodeValidationFailed,
		},
		{
			"two root fields",
			graphql.WSConfig{},
			nil,
			`subscription { countdown(from: 1) postCreated { id } }`, graphql.CodeValidationFailed,
		},
		{
			"introspection off by default",
			graphql.WSConfig{},
			nil,
			`{ __schema { queryType { name } } }`, graphql.CodeValidationFailed,
		},
		{
			"limits",
			graphql.WSConfig{},
			[]graphql.Option{graphql.WithLimits(graphql.Limits{MaxAliases: 1})},
			`subscription { postCreated { a: id b: id } }`, graphql.CodeLimitExceeded,
		},
		{
			"allowlist",
			graphql.WSConfig{Persisted: &graphql.PersistedQueries{AllowlistOnly: true}},
			nil,
			`subscription { countdown(from: 1) }`, graphql.CodePersistedRequired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				s, _ := subApp(t, tt.opts...)
				c := dial(t, s, tt.cfg, graphql.WSProtocol)
				c.init(nil)
				c.subscribe("1", tt.query)

				m := c.recv()
				if m.Type != "error" || m.ID != "1" {
					t.Fatalf("got %+v, want error for 1", m)
				}

				if diff := cmp.Diff([]string{tt.want}, errorCodes(t, m)); diff != "" {
					t.Errorf("error codes (-want +got):\n%s", diff)
				}

				// The id is free again after an error (no 4409).
				c.subscribe("1", tt.query)

				if m := c.recv(); m.Type != "error" || m.ID != "1" {
					t.Fatalf("resubscribe got %+v, want error for 1", m)
				}
			})
		})
	}
}

func TestServeWSIntrospectionOptIn(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		s, _ := subApp(t)
		c := dial(t, s, graphql.WSConfig{Introspection: true}, graphql.WSProtocol)
		c.init(nil)
		c.subscribe("1", `{ __schema { subscriptionType { name } } }`)
		c.wantNext("1", `{"__schema":{"subscriptionType":{"name":"Subscription"}}}`)
		c.wantComplete("1")
	})
}

func TestServeWSMutationPublishesToSubscribers(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		s, _ := subApp(t)
		sub := dial(t, s, graphql.WSConfig{}, graphql.WSProtocol)
		sub.init(nil)
		sub.subscribe("s", `subscription { postCreated { id title } }`)
		synctest.Wait() // the broadcast subscription is in place

		pub := dial(t, s, graphql.WSConfig{}, graphql.WSProtocol)
		pub.init(nil)
		pub.subscribe("m", `mutation { createPost(title: "Hello") { id } }`)
		pub.wantNext("m", `{"createPost":{"id":"1"}}`)
		pub.wantComplete("m")

		sub.wantNext("s", `{"postCreated":{"id":"1","title":"Hello"}}`)

		resp := s.Execute(t.Context(), graphql.Request{Query: `mutation { createPost(title: "Again") { id } }`})
		if len(resp.Errors) > 0 {
			t.Fatalf("Execute(createPost) errors = %v", resp.Errors)
		}

		sub.wantNext("s", `{"postCreated":{"id":"1","title":"Again"}}`)
	})
}

func TestServeWSCompleteStopsSubscription(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		s, b := subApp(t)
		c := dial(t, s, graphql.WSConfig{}, graphql.WSProtocol)
		c.init(nil)
		c.subscribe("s", `subscription { postCreated { id } }`)
		synctest.Wait()

		c.send(`{"id":"s","type":"complete"}`)
		synctest.Wait()

		if err := b.Publish(t.Context(), "posts", "post.created", wsPost{ID: "9"}); err != nil {
			t.Fatal(err)
		}

		synctest.Wait()

		// Nothing was sent for s: the next message answers the ping.
		c.send(`{"type":"ping"}`)

		if m := c.recv(); m.Type != "pong" {
			t.Fatalf("after complete got %+v, want pong", m)
		}

		// The id can be reused once completed.
		c.subscribe("s", `subscription { countdown(from: 1) }`)
		c.wantNext("s", `{"countdown":1}`)
		c.wantComplete("s")
	})
}

func TestSecurity_A06_GraphQLSubscriptionsBounded(t *testing.T) {
	t.Parallel()

	t.Run("init timeout", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			s, _ := subApp(t)
			start := time.Now()
			c := dial(t, s, graphql.WSConfig{}, graphql.WSProtocol)
			c.wantClosed(graphql.CloseInitTimeout)

			if got := time.Since(start); got != graphql.DefaultWSInitTimeout {
				t.Errorf("idle socket closed after %v, want %v", got, graphql.DefaultWSInitTimeout)
			}
		})
	})

	t.Run("operations per connection", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			s, _ := subApp(t)
			c := dial(t, s, graphql.WSConfig{}, graphql.WSProtocol)
			c.init(nil)

			over := strconv.Itoa(graphql.DefaultWSMaxSubscriptions)
			for i := range graphql.DefaultWSMaxSubscriptions + 1 {
				c.subscribe(strconv.Itoa(i), `subscription { postCreated { id } }`)
			}

			m := c.recv()
			if m.Type != "error" || m.ID != over {
				t.Fatalf("got %+v, want error for %s", m, over)
			}

			if diff := cmp.Diff([]string{graphql.CodeTooManyRequests}, errorCodes(t, m)); diff != "" {
				t.Errorf("error codes (-want +got):\n%s", diff)
			}
		})
	})
}

func TestServeWSSourceStreamErrorIsMasked(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		s, _ := subApp(t)
		c := dial(t, s, graphql.WSConfig{}, graphql.WSProtocol)
		c.init(nil)
		c.subscribe("1", `subscription { broken }`)

		m := c.recv()
		if m.Type != "next" {
			t.Fatalf("got %+v, want next with errors", m)
		}

		want := `{"errors":[{"message":"Internal server error","locations":[{"line":1,"column":16}],` +
			`"path":["broken"],"extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`
		if string(m.Payload) != want {
			t.Errorf("payload = %s, want %s", m.Payload, want)
		}

		c.wantComplete("1")
	})
}

func TestSecurity_A01_GraphQLSubscriptionsAuthorized(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		connect func(context.Context, map[string]any) (context.Context, error)
		forbid  bool
	}{
		{"anonymous", nil, true},
		{"other user", withUser("bob"), true},
		{"admin", withUser("admin"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				s, b := subApp(t)
				c := dial(t, s, graphql.WSConfig{OnConnect: tt.connect}, graphql.WSProtocol)
				c.init(nil)
				c.subscribe("a", `subscription { adminFeed { title } }`)

				if !tt.forbid {
					synctest.Wait()

					if err := b.Publish(t.Context(), "private-admin", "post.created", wsPost{Title: "x"}); err != nil {
						t.Fatal(err)
					}

					c.wantNext("a", `{"adminFeed":{"title":"x"}}`)

					return
				}

				m := c.recv()
				if m.Type != "next" {
					t.Fatalf("got %+v, want next with errors", m)
				}

				if diff := cmp.Diff([]string{graphql.CodeForbidden}, errorCodes(t, m)); diff != "" {
					t.Errorf("error codes (-want +got):\n%s", diff)
				}

				c.wantComplete("a")
			})
		})
	}
}
