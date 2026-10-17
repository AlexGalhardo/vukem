package graphql

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// WSProtocol is the WebSocket subprotocol of the graphql-ws protocol
// (github.com/enisdenjo/graphql-ws, PROTOCOL.md) that ServeWS speaks.
const WSProtocol = "graphql-transport-ws"

// Defaults of WSConfig.
const (
	DefaultWSInitTimeout      = 10 * time.Second
	DefaultWSMaxSubscriptions = 32
)

// Close codes of the graphql-ws protocol.
const (
	CloseBadRequest         = 4400
	CloseUnauthorized       = 4401
	CloseForbidden          = 4403
	CloseSubprotocol        = 4406
	CloseInitTimeout        = 4408
	CloseSubscriberExists   = 4409
	CloseTooManyInitRequest = 4429
	CloseInternalError      = 4500
)

// WSConn is the WebSocket connection ServeWS runs on. C is the close code
// type of the WebSocket library, so drivers/websocket's *Conn satisfies it
// as is; contrib/graphql does not depend on any WebSocket library.
type WSConn[C ~int] interface {
	// Context is done when the connection ends.
	Context() context.Context
	// Subprotocol is the negotiated subprotocol.
	Subprotocol() string
	// ReadText reads one text message.
	ReadText(ctx context.Context) (string, error)
	// WriteText writes one text message.
	WriteText(ctx context.Context, s string) error
	// Close sends a close frame with code and reason.
	Close(code C, reason string) error
}

// WSConfig tunes ServeWS.
type WSConfig struct {
	// OnConnect authenticates connection_init: it gets the upgrade
	// request's context and the init payload, and returns the context
	// every operation of the connection runs in (put the user there). An
	// error closes the socket with 4403 Forbidden. Nil accepts every
	// connection with the upgrade request's context (cookie sessions).
	OnConnect func(ctx context.Context, payload map[string]any) (context.Context, error)
	// InitTimeout bounds the wait for connection_init (default 10s);
	// the socket is closed with 4408 when it passes.
	InitTimeout time.Duration
	// MaxSubscriptions caps the operations running at once on one
	// connection (default 32); one more gets a TOO_MANY_REQUESTS error.
	MaxSubscriptions int
	// Persisted applies persisted queries as Handler does; share the
	// value with HandlerConfig so allowlist mode covers both transports.
	Persisted *PersistedQueries
	// Introspection allows __schema and __type (rejected by default:
	// tools introspect over HTTP).
	Introspection bool
}

// errClosed ends the read loop after the server closed the socket.
var errClosed = errors.New("graphql-ws: closed")

// ServeWS runs the graphql-ws protocol (subprotocol "graphql-transport-ws")
// on conn until the client leaves or breaks the protocol (the socket is
// then closed with the protocol's 44xx code). Protocol violations are
// answered on the socket, so it returns nil; the error result lets it be
// a WebSocket handler's return value. It is a function, not a method, so
// C can be inferred from conn:
//
//	r.Get("/graphql/ws", websocket.Handler(func(c *websocket.Conn) error {
//		return graphql.ServeWS(schema, c, graphql.WSConfig{OnConnect: auth})
//	}, websocket.Subprotocols(graphql.WSProtocol)))
//
// drivers/websocket checks the Origin before the upgrade (same origin by
// default) and caps message sizes; ServeWS adds the connection_init
// handshake and its timeout, the OnConnect hook, the per-connection
// operation limit and the schema's limits and validation on every
// document. Queries and mutations sent over the socket get one "next"
// then "complete".
func ServeWS[C ~int](s *Schema, conn WSConn[C], cfg WSConfig) error {
	if cfg.InitTimeout <= 0 {
		cfg.InitTimeout = DefaultWSInitTimeout
	}

	if cfg.MaxSubscriptions <= 0 {
		cfg.MaxSubscriptions = DefaultWSMaxSubscriptions
	}

	if cfg.Persisted != nil && cfg.Persisted.Store == nil {
		p := *cfg.Persisted
		if p.AllowlistOnly {
			p.Store = NewAllowlist()
		} else {
			p.Store = NewMemoryStore(0)
		}

		cfg.Persisted = &p
	}

	ctx, cancel := context.WithCancel(conn.Context())

	w := &wsSession[C]{
		s: s, conn: conn, cfg: cfg, ctx: ctx, cancel: cancel,
		acked: make(chan struct{}), ops: map[string]*wsOp{},
	}

	defer w.wg.Wait()
	defer w.stopAll()
	defer cancel()

	if conn.Subprotocol() != WSProtocol {
		w.close(CloseSubprotocol, "Subprotocol not acceptable")
		return nil
	}

	w.wg.Go(w.initTimeout)
	w.loop()

	return nil
}

type wsSession[C ~int] struct {
	s      *Schema
	conn   WSConn[C]
	cfg    WSConfig
	ctx    context.Context //nolint:containedctx // the connection's lifetime, scoped to ServeWS
	cancel context.CancelFunc

	opCtx     context.Context //nolint:containedctx // OnConnect's context, set once before acked closes
	acked     chan struct{}
	initiated bool // owned by the read loop

	writeMu sync.Mutex
	wg      sync.WaitGroup

	mu        sync.Mutex
	ops       map[string]*wsOp
	closeOnce sync.Once
}

type wsOp struct {
	cancel context.CancelFunc
}

// wsMessage is a graphql-ws message, both directions.
type wsMessage struct {
	ID      string          `json:"id,omitempty"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

func (w *wsSession[C]) initTimeout() {
	t := time.NewTimer(w.cfg.InitTimeout)
	defer t.Stop()

	select {
	case <-t.C:
		w.close(CloseInitTimeout, "Connection initialisation timeout")
	case <-w.acked:
	case <-w.ctx.Done():
	}
}

// close sends the close frame once and ends the session.
func (w *wsSession[C]) close(code int, reason string) {
	w.closeOnce.Do(func() {
		_ = w.conn.Close(C(code), reason)
		w.cancel()
	})
}

// fail closes the socket and stops the read loop.
func (w *wsSession[C]) fail(code int, reason string) error {
	w.close(code, reason)
	return errClosed
}

func (w *wsSession[C]) loop() {
	for {
		text, err := w.conn.ReadText(w.ctx)
		if err != nil {
			return // the client left or the session ended
		}

		var m wsMessage
		if json.Unmarshal([]byte(text), &m) != nil || m.Type == "" {
			w.close(CloseBadRequest, "Invalid message received")
			return
		}

		if w.handle(m) != nil {
			return
		}
	}
}

func (w *wsSession[C]) handle(m wsMessage) error {
	switch m.Type {
	case "connection_init":
		return w.connectionInit(m)
	case "ping":
		w.send(wsMessage{Type: "pong"})
	case "pong":
	case "subscribe":
		return w.subscribe(m)
	case "complete":
		if m.ID == "" {
			return w.fail(CloseBadRequest, "Invalid message received")
		}

		w.stop(m.ID)
	default:
		return w.fail(CloseBadRequest, "Invalid message received")
	}

	return nil
}

func (w *wsSession[C]) connectionInit(m wsMessage) error {
	if w.initiated {
		return w.fail(CloseTooManyInitRequest, "Too many initialisation requests")
	}

	w.initiated = true

	var payload map[string]any
	if len(m.Payload) > 0 && string(m.Payload) != "null" {
		if json.Unmarshal(m.Payload, &payload) != nil {
			return w.fail(CloseBadRequest, "Invalid message received")
		}
	}

	ctx := w.ctx

	if w.cfg.OnConnect != nil {
		c, err := w.cfg.OnConnect(ctx, payload)
		if err != nil || c == nil {
			w.s.log.InfoContext(ctx, "graphql: websocket connection refused", "error", err)
			return w.fail(CloseForbidden, "Forbidden")
		}

		ctx = c
	}

	w.opCtx = ctx
	close(w.acked)
	w.send(wsMessage{Type: "connection_ack"})

	return nil
}

func (w *wsSession[C]) acknowledged() bool {
	select {
	case <-w.acked:
		return true
	default:
		return false
	}
}

func (w *wsSession[C]) subscribe(m wsMessage) error {
	if !w.acknowledged() {
		return w.fail(CloseUnauthorized, "Unauthorized")
	}

	var req Request
	if m.ID == "" || json.Unmarshal(m.Payload, &req) != nil {
		return w.fail(CloseBadRequest, "Invalid message received")
	}

	w.mu.Lock()

	if _, dup := w.ops[m.ID]; dup {
		w.mu.Unlock()
		return w.fail(CloseSubscriberExists, "Subscriber for "+m.ID+" already exists")
	}

	if len(w.ops) >= w.cfg.MaxSubscriptions {
		w.mu.Unlock()
		w.sendErrors(m.ID, []*Error{requestError(CodeTooManyRequests, "Too many subscriptions on this connection.")})

		return nil
	}

	// Operations end with the session even when OnConnect returned a
	// context not derived from the connection's.
	ctx, cancel := context.WithCancel(w.opCtx)
	op := &wsOp{cancel: cancel}
	w.ops[m.ID] = op
	w.mu.Unlock()

	w.wg.Go(func() {
		defer cancel()
		w.run(ctx, m.ID, op, req)
	})

	return nil
}

func (w *wsSession[C]) run(ctx context.Context, id string, op *wsOp, req Request) {
	// A client "complete" removes the op first; the server then sends
	// nothing more for this id. The id is freed before the terminal
	// message goes out, so a client reusing it as soon as it reads
	// "error" or "complete" is not closed with 4409.
	release := func() bool {
		w.mu.Lock()
		defer w.mu.Unlock()

		mine := w.ops[id] == op
		if mine {
			delete(w.ops, id)
		}

		return mine
	}

	finish := func(sendComplete bool) {
		if release() && sendComplete && w.ctx.Err() == nil {
			w.send(wsMessage{ID: id, Type: "complete"})
		}
	}

	if gerr := w.cfg.Persisted.resolve(ctx, &req); gerr != nil {
		if release() {
			w.sendErrors(id, []*Error{gerr})
		}

		return
	}

	p, errs := w.s.prepare(req, w.cfg.Introspection)
	if errs != nil {
		if release() {
			w.sendErrors(id, errs)
		}

		return
	}

	stream, resp := w.s.subscribe(ctx, p)
	if resp != nil {
		if ctx.Err() == nil {
			w.next(id, resp)
		}

		finish(true)

		return
	}

	for r := range stream {
		if ctx.Err() != nil {
			break
		}

		w.next(id, r)
	}

	finish(true)
}

func (w *wsSession[C]) next(id string, resp *Response) {
	b, err := json.Marshal(resp)
	if err != nil {
		w.s.log.Error("graphql: encode subscription response", "error", err)
		w.close(CloseInternalError, "Internal server error")

		return
	}

	w.send(wsMessage{ID: id, Type: "next", Payload: b})
}

func (w *wsSession[C]) sendErrors(id string, errs []*Error) {
	b, err := json.Marshal(errs)
	if err != nil {
		w.close(CloseInternalError, "Internal server error")
		return
	}

	w.send(wsMessage{ID: id, Type: "error", Payload: b})
}

func (w *wsSession[C]) send(m wsMessage) {
	b, err := json.Marshal(m)
	if err != nil {
		return
	}

	w.writeMu.Lock()
	defer w.writeMu.Unlock()

	if w.ctx.Err() != nil {
		return
	}

	if err := w.conn.WriteText(w.ctx, string(b)); err != nil && !errors.Is(err, context.Canceled) {
		w.cancel() // a failed or timed-out write ends the session
	}
}

// stop cancels the operation id after a client "complete".
func (w *wsSession[C]) stop(id string) {
	w.mu.Lock()
	op := w.ops[id]
	delete(w.ops, id)
	w.mu.Unlock()

	if op != nil {
		op.cancel()
	}
}

func (w *wsSession[C]) stopAll() {
	w.mu.Lock()
	defer w.mu.Unlock()

	for id, op := range w.ops {
		op.cancel()
		delete(w.ops, id)
	}
}
