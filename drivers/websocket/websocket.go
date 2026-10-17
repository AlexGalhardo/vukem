// Package websocket serves WebSocket connections for Vukem apps on top of
// github.com/coder/websocket, with secure defaults: same-origin upgrades
// only (plus an explicit allowlist), a 32 KiB read limit, ping/pong
// keepalive that drops dead peers, per-message write timeouts and a 1001
// "going away" close when the app shuts down.
//
// Inside a handler, Handle reads like c.WebSocket(fn):
//
//	r.Get("/chat", func(c *web.Ctx) error {
//		return websocket.Handle(c, func(conn *websocket.Conn) error {
//			msg, err := conn.ReadText(conn.Context())
//			...
//		})
//	})
//
// or, for a route that only upgrades, r.Get("/chat", websocket.Handler(chat)).
// [Broadcast] serves package broadcast channels over the same connection
// type.
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/frontend.md.
package websocket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/sse"
	"github.com/AlexGalhardo/vukem/web"
)

// Defaults applied when no Option overrides them.
const (
	DefaultReadLimit    = 32 << 10
	DefaultPingInterval = 30 * time.Second
	DefaultPingTimeout  = 10 * time.Second
	DefaultWriteTimeout = 10 * time.Second
)

// StatusCode is a WebSocket close status code (RFC 6455, section 7.4).
type StatusCode = websocket.StatusCode

// Close status codes used by this package.
const (
	StatusNormalClosure   = websocket.StatusNormalClosure
	StatusGoingAway       = websocket.StatusGoingAway
	StatusPolicyViolation = websocket.StatusPolicyViolation
	StatusMessageTooBig   = websocket.StatusMessageTooBig
	StatusInternalError   = websocket.StatusInternalError
)

// ErrNotText is returned by ReadText and ReadJSON for binary messages.
var ErrNotText = errors.New("websocket: binary message, want text")

// CloseStatus returns the close code carried by err, or -1 when err did not
// come from a close frame.
func CloseStatus(err error) StatusCode { return websocket.CloseStatus(err) }

// Option configures an upgrade.
type Option func(*options)

type options struct {
	origins      []string
	insecure     bool
	subprotocols []string
	readLimit    int64
	pingInterval time.Duration
	pingTimeout  time.Duration
	writeTimeout time.Duration
}

// AllowedOrigins authorizes cross-origin upgrades from browsers whose
// Origin host matches one of patterns (path.Match syntax, case-insensitive;
// a pattern with "://" matches "scheme://host"). The request's own host is
// always allowed. "*" is refused: use InsecureSkipOriginCheck to say so.
func AllowedOrigins(patterns ...string) Option {
	return func(o *options) { o.origins = append(o.origins, patterns...) }
}

// InsecureSkipOriginCheck accepts upgrades from any origin, which lets any
// website open a socket with the user's cookies (cross-site WebSocket
// hijacking). Only for endpoints that authenticate without cookies.
func InsecureSkipOriginCheck() Option { return func(o *options) { o.insecure = true } }

// Subprotocols lists the subprotocols the server negotiates, in preference
// order; Conn.Subprotocol reports the chosen one ("" when none matched).
func Subprotocols(protocols ...string) Option {
	return func(o *options) { o.subprotocols = append(o.subprotocols, protocols...) }
}

// ReadLimit caps the size of one incoming message in bytes; a bigger
// message closes the connection with StatusMessageTooBig.
func ReadLimit(n int64) Option { return func(o *options) { o.readLimit = n } }

// PingInterval sets how often the server pings the client (zero or
// negative disables keepalive).
func PingInterval(d time.Duration) Option { return func(o *options) { o.pingInterval = d } }

// PingTimeout sets how long a ping waits for its pong before the peer is
// considered dead and the connection is dropped.
func PingTimeout(d time.Duration) Option { return func(o *options) { o.pingTimeout = d } }

// WriteTimeout bounds every write; a write that times out closes the
// connection.
func WriteTimeout(d time.Duration) Option { return func(o *options) { o.writeTimeout = d } }

func newOptions(opts []Option) (options, error) {
	o := options{
		readLimit:    DefaultReadLimit,
		pingInterval: DefaultPingInterval,
		pingTimeout:  DefaultPingTimeout,
		writeTimeout: DefaultWriteTimeout,
	}
	for _, opt := range opts {
		opt(&o)
	}

	for _, p := range o.origins {
		if p == "*" {
			return o, errors.New(`websocket: AllowedOrigins("*") is refused; use InsecureSkipOriginCheck`)
		}

		if _, err := path.Match(p, ""); err != nil {
			return o, fmt.Errorf("websocket: origin pattern %q: %w", p, err)
		}
	}

	if o.readLimit <= 0 {
		return o, fmt.Errorf("websocket: read limit %d must be positive", o.readLimit)
	}

	if o.pingInterval > 0 && o.pingTimeout <= 0 || o.writeTimeout <= 0 {
		return o, errors.New("websocket: ping and write timeouts must be positive")
	}

	return o, nil
}

// Handler returns a route handler that upgrades the request and runs fn.
func Handler(fn func(*Conn) error, opts ...Option) web.HandlerFunc {
	return func(c *web.Ctx) error { return Handle(c, fn, opts...) }
}

// Handle upgrades the request of c and runs fn with the connection; it is
// the Vukem counterpart of c.SSE for WebSockets. A refused upgrade (bad
// origin, not a WebSocket request) returns an HTTP error with the status
// already sent to the client.
func Handle(c *web.Ctx, fn func(*Conn) error, opts ...Option) error {
	err := Serve(c.Writer(), c.Request(), fn, opts...)
	if errors.Is(err, errUpgrade) {
		return errs.Wrap(c.Writer().Status(), err)
	}

	return err
}

// errUpgrade marks errors from a refused upgrade (the response is sent).
var errUpgrade = errors.New("websocket: upgrade refused")

// Serve upgrades r and runs fn until it returns, the client goes away, the
// peer stops answering pings or the app shuts down; Conn.Context is then
// done. The connection is closed when Serve returns: StatusNormalClosure
// when fn returns nil, StatusInternalError when it fails, StatusGoingAway
// on shutdown.
func Serve(w http.ResponseWriter, r *http.Request, fn func(*Conn) error, opts ...Option) error {
	o, err := newOptions(opts)
	if err != nil {
		// Fail closed: a misconfigured endpoint never upgrades.
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return err
	}

	// net/http clears the server's read/write deadlines on hijack; the
	// socket enforces its own timeouts (ping, write) instead.
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns:     o.origins,
		InsecureSkipVerify: o.insecure,
		Subprotocols:       o.subprotocols,
	})
	if err != nil {
		return fmt.Errorf("%w: %w", errUpgrade, err)
	}

	ws.SetReadLimit(o.readLimit)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	conn := &Conn{ws: ws, ctx: ctx, writeTimeout: o.writeTimeout}

	var wg sync.WaitGroup
	defer wg.Wait()

	if stop := sse.ShutdownSignal(r.Context()); stop != nil {
		wg.Go(func() {
			select {
			case <-stop:
				conn.close(StatusGoingAway, "server shutting down")
				cancel()
			case <-ctx.Done():
			}
		})
	}

	if o.pingInterval > 0 {
		wg.Go(func() { conn.keepalive(ctx, cancel, o.pingInterval, o.pingTimeout) })
	}

	err = fn(conn)
	if err != nil {
		conn.close(StatusInternalError, "")
	} else {
		conn.close(StatusNormalClosure, "")
	}

	cancel()

	return err
}

// Conn is an accepted WebSocket connection. Reads and writes take a
// context (usually Context()); one reader and one writer may run
// concurrently.
type Conn struct {
	ws           *websocket.Conn
	ctx          context.Context //nolint:containedctx // the connection's lifetime, exposed by Context
	writeTimeout time.Duration
	closeOnce    sync.Once
}

// Context is done when the client goes away, the handler returns, the
// peer stops answering pings or the app shuts down.
func (c *Conn) Context() context.Context { return c.ctx }

// Subprotocol returns the negotiated subprotocol ("" when none).
func (c *Conn) Subprotocol() string { return c.ws.Subprotocol() }

// ReadText reads one text message.
func (c *Conn) ReadText(ctx context.Context) (string, error) {
	b, err := c.readText(ctx)
	return string(b), err
}

// ReadJSON reads one text message and decodes it as JSON into v.
func (c *Conn) ReadJSON(ctx context.Context, v any) error {
	b, err := c.readText(ctx)
	if err != nil {
		return err
	}

	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("websocket: decode JSON message: %w", err)
	}

	return nil
}

func (c *Conn) readText(ctx context.Context) ([]byte, error) {
	typ, b, err := c.ws.Read(ctx)
	if err != nil {
		return nil, err
	}

	if typ != websocket.MessageText {
		return nil, ErrNotText
	}

	return b, nil
}

// WriteText sends s as one text message within the write timeout.
func (c *Conn) WriteText(ctx context.Context, s string) error {
	return c.write(ctx, []byte(s))
}

// WriteJSON sends v encoded as JSON in one text message within the write
// timeout.
func (c *Conn) WriteJSON(ctx context.Context, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("websocket: encode JSON message: %w", err)
	}

	return c.write(ctx, b)
}

func (c *Conn) write(ctx context.Context, b []byte) error {
	ctx, cancel := context.WithTimeout(ctx, c.writeTimeout)
	defer cancel()

	return c.ws.Write(ctx, websocket.MessageText, b)
}

// CloseRead discards incoming messages for handlers that only write, so
// pongs and close frames are still processed; the returned context is done
// when the connection closes. A data message from the client closes the
// connection with StatusPolicyViolation.
func (c *Conn) CloseRead(ctx context.Context) context.Context { return c.ws.CloseRead(ctx) }

// Close performs the closing handshake with code and reason. Closing again
// (or after Serve closed the connection) is a no-op.
func (c *Conn) Close(code StatusCode, reason string) error {
	var err error

	c.closeOnce.Do(func() { err = c.ws.Close(code, reason) })

	return err
}

// Unwrap returns the underlying coder/websocket connection for features
// this wrapper does not cover (binary messages, streaming readers).
func (c *Conn) Unwrap() *websocket.Conn { return c.ws }

func (c *Conn) close(code StatusCode, reason string) { _ = c.Close(code, reason) }

// keepalive pings the peer every interval; a missing pong within timeout
// drops the connection. Pongs only arrive while something reads the
// connection (ReadText, ReadJSON or CloseRead).
func (c *Conn) keepalive(ctx context.Context, cancel context.CancelFunc, interval, timeout time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}

		pctx, pcancel := context.WithTimeout(ctx, timeout)
		err := c.ws.Ping(pctx)

		pcancel()

		if err != nil {
			if ctx.Err() == nil {
				c.closeOnce.Do(func() { _ = c.ws.CloseNow() })
				cancel()
			}

			return
		}
	}
}
