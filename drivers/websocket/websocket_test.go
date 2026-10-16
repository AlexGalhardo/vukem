package websocket_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	cws "github.com/coder/websocket"
	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/drivers/websocket"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/sse"
	"github.com/AlexGalhardo/vukem/web"
)

// waitLimit bounds how long a test waits for something that should be
// immediate; it only matters when the behaviour is broken.
const waitLimit = 5 * time.Second

func wsURL(httpURL string) string { return "ws" + strings.TrimPrefix(httpURL, "http") }

func dial(t *testing.T, url string) *cws.Conn {
	t.Helper()

	c, resp, err := cws.Dial(t.Context(), url, nil)
	if err != nil {
		t.Fatalf("Dial(%s) = %v", url, err)
	}

	if resp.Body != nil {
		_ = resp.Body.Close()
	}

	t.Cleanup(func() { _ = c.CloseNow() })

	return c
}

func echo(conn *websocket.Conn) error {
	ctx := conn.Context()

	for {
		msg, err := conn.ReadText(ctx)
		if err != nil {
			if websocket.CloseStatus(err) == websocket.StatusNormalClosure {
				return nil
			}

			return err
		}

		if err := conn.WriteText(ctx, "echo: "+msg); err != nil {
			return err
		}
	}
}

func TestHandler_EchoThroughVukemRouter(t *testing.T) {
	t.Parallel()

	r := router.New(nil)
	r.Get("/ws", websocket.Handler(echo))

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	c := dial(t, wsURL(srv.URL)+"/ws")
	ctx := t.Context()

	if err := c.Write(ctx, cws.MessageText, []byte("hi")); err != nil {
		t.Fatalf("Write = %v", err)
	}

	typ, got, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("Read = %v", err)
	}

	if typ != cws.MessageText || string(got) != "echo: hi" {
		t.Errorf("Read = %v %q, want text %q", typ, got, "echo: hi")
	}

	if err := c.Close(cws.StatusNormalClosure, ""); err != nil {
		t.Errorf("Close = %v", err)
	}
}

func TestHandle_JSONRoundTrip(t *testing.T) {
	t.Parallel()

	type msg struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}

	r := router.New(nil)
	r.Get("/ws", func(c *web.Ctx) error {
		return websocket.Handle(c, func(conn *websocket.Conn) error {
			var in msg
			if err := conn.ReadJSON(conn.Context(), &in); err != nil {
				return err
			}

			in.Count++

			return conn.WriteJSON(conn.Context(), in)
		})
	})

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	c := dial(t, wsURL(srv.URL)+"/ws")
	ctx := t.Context()

	if err := c.Write(ctx, cws.MessageText, []byte(`{"name":"go","count":1}`)); err != nil {
		t.Fatalf("Write = %v", err)
	}

	_, got, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("Read = %v", err)
	}

	if diff := cmp.Diff(`{"name":"go","count":2}`, strings.TrimSpace(string(got))); diff != "" {
		t.Errorf("WriteJSON payload mismatch (-want +got):\n%s", diff)
	}

	// The handler returned nil, so the server closes normally.
	if _, _, err := c.Read(ctx); cws.CloseStatus(err) != cws.StatusNormalClosure {
		t.Errorf("Read after handler returned = %v, want close %v", err, cws.StatusNormalClosure)
	}
}

func TestSecurity_A01_WebSocketOriginChecked(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		origin     string // "self" means the server's own origin
		opts       []websocket.Option
		wantStatus int // 0 means the upgrade succeeds
	}{
		{name: "cross origin rejected", origin: "https://evil.example", wantStatus: http.StatusForbidden},
		{name: "same origin allowed", origin: "self"},
		{name: "no origin (non-browser client) allowed", origin: ""},
		{
			name:   "allowlisted origin",
			origin: "https://app.example.com",
			opts:   []websocket.Option{websocket.AllowedOrigins("app.example.com")},
		},
		{
			name:       "allowlist does not open other origins",
			origin:     "https://evil.example",
			opts:       []websocket.Option{websocket.AllowedOrigins("app.example.com")},
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "wildcard allowlist is a configuration error",
			origin:     "https://evil.example",
			opts:       []websocket.Option{websocket.AllowedOrigins("*")},
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "malformed pattern is a configuration error",
			origin:     "self",
			opts:       []websocket.Option{websocket.AllowedOrigins("[")},
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:   "explicit insecure opt-out",
			origin: "https://evil.example",
			opts:   []websocket.Option{websocket.InsecureSkipOriginCheck()},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var ran atomic.Bool

			r := router.New(nil)
			r.Get("/ws", websocket.Handler(func(*websocket.Conn) error {
				ran.Store(true)
				return nil
			}, tt.opts...))

			srv := httptest.NewServer(r)
			t.Cleanup(srv.Close)

			header := http.Header{}
			if tt.origin == "self" {
				header.Set("Origin", srv.URL)
			} else if tt.origin != "" {
				header.Set("Origin", tt.origin)
			}

			c, resp, err := cws.Dial(t.Context(), wsURL(srv.URL)+"/ws", &cws.DialOptions{HTTPHeader: header})
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}

			if tt.wantStatus == 0 {
				if err != nil {
					t.Fatalf("Dial(origin %q) = %v, want upgrade", tt.origin, err)
				}

				_ = c.CloseNow()

				return
			}

			if err == nil {
				_ = c.CloseNow()
				t.Fatalf("Dial(origin %q) upgraded, want status %d", tt.origin, tt.wantStatus)
			}

			if resp == nil || resp.StatusCode != tt.wantStatus {
				t.Errorf("Dial(origin %q) response = %v, want status %d", tt.origin, resp, tt.wantStatus)
			}

			if ran.Load() {
				t.Error("handler ran for a rejected upgrade")
			}
		})
	}
}

func TestSecurity_A06_WebSocketReadLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts []websocket.Option
		size int
	}{
		{name: "default 32 KiB", size: websocket.DefaultReadLimit + 1},
		{name: "configured", opts: []websocket.Option{websocket.ReadLimit(16)}, size: 17},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			readErr := make(chan error, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = websocket.Serve(w, r, func(conn *websocket.Conn) error {
					_, err := conn.ReadText(conn.Context())
					readErr <- err

					return err
				}, tt.opts...)
			}))
			t.Cleanup(srv.Close)

			c := dial(t, wsURL(srv.URL))
			ctx := t.Context()

			_ = c.Write(ctx, cws.MessageText, []byte(strings.Repeat("a", tt.size)))

			if _, _, err := c.Read(ctx); cws.CloseStatus(err) != cws.StatusMessageTooBig {
				t.Errorf("client Read after oversized message = %v, want close %v", err, cws.StatusMessageTooBig)
			}

			select {
			case err := <-readErr:
				if err == nil {
					t.Error("ReadText(oversized) = nil, want error")
				}
			case <-time.After(waitLimit):
				t.Fatal("handler kept reading past the limit")
			}
		})
	}
}

func TestServe_AcceptsMessageAtReadLimit(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = websocket.Serve(w, r, func(conn *websocket.Conn) error {
			msg, err := conn.ReadText(conn.Context())
			if err != nil {
				return err
			}

			return conn.WriteText(conn.Context(), msg[:2])
		}, websocket.ReadLimit(16))
	}))
	t.Cleanup(srv.Close)

	c := dial(t, wsURL(srv.URL))

	if err := c.Write(t.Context(), cws.MessageText, []byte(strings.Repeat("b", 16))); err != nil {
		t.Fatalf("Write = %v", err)
	}

	if _, got, err := c.Read(t.Context()); err != nil || string(got) != "bb" {
		t.Errorf("Read = %q, %v, want %q", got, err, "bb")
	}
}

func TestServe_ShutdownClosesGoingAway(t *testing.T) {
	t.Parallel()

	handlerDone := make(chan error, 1)

	r := router.New(nil)
	r.Get("/ws", websocket.Handler(func(conn *websocket.Conn) error {
		err := echo(conn)
		handlerDone <- err

		return err
	}))

	srv := vukem.NewHTTPServer("127.0.0.1:0", r)
	ready := make(chan string, 1)
	srv.Ready = ready

	ctx, stop := context.WithCancel(t.Context())
	runErr := make(chan error, 1)

	go func() { runErr <- srv.Run(ctx) }()

	c := dial(t, "ws://"+<-ready+"/ws")

	if err := c.Write(t.Context(), cws.MessageText, []byte("hi")); err != nil {
		t.Fatalf("Write = %v", err)
	}

	if _, _, err := c.Read(t.Context()); err != nil {
		t.Fatalf("Read before shutdown = %v", err)
	}

	stop()

	if _, _, err := c.Read(t.Context()); cws.CloseStatus(err) != cws.StatusGoingAway {
		t.Errorf("Read after shutdown = %v, want close %v", err, cws.StatusGoingAway)
	}

	select {
	case <-handlerDone:
	case <-time.After(waitLimit):
		t.Fatal("handler kept running after shutdown")
	}

	select {
	case err := <-runErr:
		if err != nil {
			t.Errorf("Run = %v, want nil", err)
		}
	case <-time.After(waitLimit):
		t.Fatal("shutdown waited on the open WebSocket")
	}
}

func TestServe_ShutdownSignalFromContext(t *testing.T) {
	t.Parallel()

	stop := make(chan struct{})
	handlerDone := make(chan struct{})

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = websocket.Serve(w, r, func(conn *websocket.Conn) error {
			<-conn.Context().Done()
			close(handlerDone)

			return nil
		})
	}))
	srv.Config.BaseContext = func(_ net.Listener) context.Context {
		return sse.WithShutdown(context.Background(), stop)
	}
	srv.Start()
	t.Cleanup(srv.Close)

	c := dial(t, wsURL(srv.URL))
	close(stop)

	if _, _, err := c.Read(t.Context()); cws.CloseStatus(err) != cws.StatusGoingAway {
		t.Errorf("Read after shutdown = %v, want close %v", err, cws.StatusGoingAway)
	}

	select {
	case <-handlerDone:
	case <-time.After(waitLimit):
		t.Fatal("Conn.Context not done after shutdown")
	}
}

func TestServe_PingClosesDeadPeer(t *testing.T) {
	t.Parallel()

	handlerDone := make(chan error, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = websocket.Serve(w, r, func(conn *websocket.Conn) error {
			_, err := conn.ReadText(conn.Context())
			handlerDone <- err

			return err
		}, websocket.PingInterval(20*time.Millisecond), websocket.PingTimeout(50*time.Millisecond))
	}))
	t.Cleanup(srv.Close)

	// The client never reads, so it never answers pings: a dead peer.
	_ = dial(t, wsURL(srv.URL))

	select {
	case err := <-handlerDone:
		if err == nil {
			t.Error("ReadText on a dead peer = nil, want error")
		}
	case <-time.After(waitLimit):
		t.Fatal("dead peer kept the connection open")
	}
}

func TestServe_PingKeepsLivePeer(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = websocket.Serve(w, r, func(conn *websocket.Conn) error {
			ctx := conn.CloseRead(conn.Context())
			tick := time.NewTicker(30 * time.Millisecond)
			defer tick.Stop()

			for i := range 3 {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-tick.C:
				}

				if err := conn.WriteText(ctx, string(rune('a'+i))); err != nil {
					return err
				}
			}

			return nil
		}, websocket.PingInterval(5*time.Millisecond), websocket.PingTimeout(time.Second))
	}))
	t.Cleanup(srv.Close)

	c := dial(t, wsURL(srv.URL))

	var got []string

	for range 3 {
		_, b, err := c.Read(t.Context())
		if err != nil {
			t.Fatalf("Read = %v (after %v)", err, got)
		}

		got = append(got, string(b))
	}

	if diff := cmp.Diff([]string{"a", "b", "c"}, got); diff != "" {
		t.Errorf("messages mismatch (-want +got):\n%s", diff)
	}
}

func TestServe_HandlerErrorClosesInternalError(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")
	got := make(chan error, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- websocket.Serve(w, r, func(*websocket.Conn) error { return boom })
	}))
	t.Cleanup(srv.Close)

	c := dial(t, wsURL(srv.URL))

	if _, _, err := c.Read(t.Context()); cws.CloseStatus(err) != cws.StatusInternalError {
		t.Errorf("Read = %v, want close %v", err, cws.StatusInternalError)
	}

	if err := <-got; !errors.Is(err, boom) {
		t.Errorf("Serve = %v, want %v", err, boom)
	}
}

func TestServe_OutlivesServerTimeouts(t *testing.T) {
	t.Parallel()

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = websocket.Serve(w, r, echo)
	}))
	srv.Config.ReadTimeout = 50 * time.Millisecond
	srv.Config.WriteTimeout = 50 * time.Millisecond
	srv.Start()
	t.Cleanup(srv.Close)

	c := dial(t, wsURL(srv.URL))

	// Outlive the server's timeouts before talking.
	<-time.After(150 * time.Millisecond)

	if err := c.Write(t.Context(), cws.MessageText, []byte("late")); err != nil {
		t.Fatalf("Write = %v", err)
	}

	if _, got, err := c.Read(t.Context()); err != nil || string(got) != "echo: late" {
		t.Errorf("Read = %q, %v, want %q", got, err, "echo: late")
	}
}
