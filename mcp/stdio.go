package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// lineWriter writes one JSON message per line under a lock.
type lineWriter struct {
	mu  sync.Mutex
	w   io.Writer
	err error
}

func (l *lineWriter) send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.err != nil {
		return l.err
	}

	// json.Marshal escapes newlines and U+2028, so b is one line.
	if _, l.err = l.w.Write(append(b, '\n')); l.err != nil {
		return l.err
	}

	return nil
}

func (l *lineWriter) failure() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.err
}

// readLine returns the next line of at most max bytes. A longer line is
// drained and reported as tooBig, so one oversized message neither exhausts
// memory nor desynchronizes the stream.
func readLine(r *bufio.Reader, maxBytes int64) (line []byte, tooBig bool, err error) {
	for {
		chunk, rerr := r.ReadSlice('\n')

		if !tooBig {
			if int64(len(line)+len(chunk)) > maxBytes+1 {
				tooBig, line = true, nil
			} else {
				line = append(line, chunk...)
			}
		}

		if errors.Is(rerr, bufio.ErrBufferFull) {
			continue
		}

		if rerr != nil && (len(line) == 0 && !tooBig) {
			return nil, false, rerr
		}

		return bytes.TrimRight(line, "\r\n"), tooBig, nil
	}
}

func idKey(id json.RawMessage) string { return string(id) }

func validID(id json.RawMessage) bool {
	if len(id) == 0 {
		return false
	}

	switch c := id[0]; {
	case c == '"', c == '-', c >= '0' && c <= '9':
		return true
	default:
		return false
	}
}

// ServeStdio serves MCP over newline-delimited JSON-RPC: it reads messages
// from in and writes only protocol messages to out. Log elsewhere: with a
// Vukem command, pass the app stdin and stdout and keep Config.Logger on
// stderr or a file. It returns nil when in reaches EOF, after letting the
// requests still running finish (Config.ShutdownGrace) and cancelling the
// rest, and an error when out can no longer be written or ctx ends.
//
// Requests run concurrently, up to Config.MaxInFlight; notifications/cancelled
// cancels the context of the matching request, whose response is then
// dropped. Tool functions must watch their context: ServeStdio waits for
// them.
func (s *Server) ServeStdio(ctx context.Context, in io.Reader, out io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	token, _ := TokenInfoFrom(ctx)
	conn := &stdioConn{
		s: s, ctx: ctx, cancel: cancel, w: &lineWriter{w: out}, token: token,
		inflight: map[string]context.CancelFunc{}, sem: make(chan struct{}, s.cfg.MaxInFlight),
	}

	defer conn.wg.Wait()
	defer cancel()

	return conn.run(bufio.NewReaderSize(in, 64<<10))
}

// stdioConn is the state of one ServeStdio call.
type stdioConn struct {
	s      *Server
	ctx    context.Context //nolint:containedctx // lifetime of the connection, owned by ServeStdio
	cancel context.CancelFunc
	w      *lineWriter
	token  *TokenInfo
	sem    chan struct{}
	wg     sync.WaitGroup

	mu       sync.Mutex
	inflight map[string]context.CancelFunc
	legacy   string // revision negotiated by initialize, if any
}

func (c *stdioConn) reply(id, res json.RawMessage, e *Error) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}

	if err := c.w.send(response{JSONRPC: "2.0", ID: id, Result: res, Error: e}); err != nil {
		c.cancel()
	}
}

func (c *stdioConn) run(r *bufio.Reader) error {
	for {
		line, tooBig, err := readLine(r, c.s.cfg.MaxMessageBytes)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) {
				c.s.drain(&c.wg, c.cancel)

				return c.w.failure()
			}

			if c.ctx.Err() != nil {
				return c.ctx.Err()
			}

			return fmt.Errorf("mcp: read: %w", err)
		}

		if c.ctx.Err() != nil {
			return c.ctx.Err()
		}

		if tooBig {
			c.reply(nil, nil, &Error{Code: CodeInvalidRequest, Message: "Message too large"})

			continue
		}

		if line = bytes.TrimSpace(line); len(line) > 0 {
			c.dispatch(line)
		}
	}
}

// dispatch validates one line and starts the request it carries.
func (c *stdioConn) dispatch(line []byte) {
	var msg message
	if err := json.Unmarshal(line, &msg); err != nil {
		c.reply(nil, nil, &Error{Code: CodeParse, Message: "Parse error"})

		return
	}

	switch {
	case msg.JSONRPC != "2.0" || (msg.Method == "" && len(msg.Result) == 0 && len(msg.Error) == 0):
		c.reply(safeID(msg.ID), nil, &Error{Code: CodeInvalidRequest, Message: "Invalid request"})
	case msg.Method == "":
		// A response: this server never sends requests.
	case len(msg.ID) == 0:
		if msg.Method == "notifications/cancelled" {
			c.cancelRequest(msg.Params)
		}
	case !validID(msg.ID):
		c.reply(nil, nil, &Error{Code: CodeInvalidRequest, Message: "Invalid request id"})
	default:
		c.start(msg)
	}
}

func (c *stdioConn) cancelRequest(params json.RawMessage) {
	var p struct {
		RequestID json.RawMessage `json:"requestId"`
	}

	_ = json.Unmarshal(params, &p)

	c.mu.Lock()
	defer c.mu.Unlock()

	if stop, ok := c.inflight[idKey(p.RequestID)]; ok {
		stop()
	}
}

func (c *stdioConn) start(msg message) {
	select {
	case c.sem <- struct{}{}:
	default:
		c.reply(msg.ID, nil, &Error{Code: CodeInternal, Message: "Server busy"})

		return
	}

	key := idKey(msg.ID)
	reqCtx, stop := context.WithCancel(c.ctx)

	c.mu.Lock()

	if _, dup := c.inflight[key]; dup {
		c.mu.Unlock()
		stop()

		<-c.sem

		c.reply(msg.ID, nil, &Error{Code: CodeInvalidRequest, Message: "Duplicate request id"})

		return
	}

	c.inflight[key] = stop
	fallback := c.legacy
	c.mu.Unlock()

	c.wg.Add(1)

	finish := func() {
		defer func() {
			c.mu.Lock()
			delete(c.inflight, key)
			c.mu.Unlock()
			stop()

			<-c.sem
			c.wg.Done()
		}()

		c.serve(reqCtx, msg, fallback)
	}

	// initialize runs inline: it fixes the revision of the requests that
	// follow, so they must not start before it ends.
	if msg.Method == "initialize" {
		finish()

		return
	}

	go finish()
}

func (c *stdioConn) serve(reqCtx context.Context, msg message, fallback string) {
	cl := &call{msg: msg, token: c.token}
	cl.notify = func(method string, params any) {
		if reqCtx.Err() != nil {
			return
		}

		if err := c.w.send(notification{JSONRPC: "2.0", Method: method, Params: params}); err != nil {
			c.cancel()
		}
	}

	if rerr := c.s.resolve(cl, "", fallback); rerr != nil {
		if reqCtx.Err() == nil {
			c.reply(msg.ID, nil, rerr)
		}

		return
	}

	res, rerr := c.s.handle(reqCtx, cl)

	if msg.Method == "initialize" && rerr == nil {
		c.mu.Lock()
		c.legacy = cl.version
		c.mu.Unlock()
	}

	if reqCtx.Err() != nil {
		return // cancelled: no further messages for this request
	}

	c.reply(msg.ID, res, rerr)
}

// drain waits for the running requests after the input closed, and cancels
// them once the grace period is over.
func (s *Server) drain(wg *sync.WaitGroup, cancel context.CancelFunc) {
	idle := make(chan struct{})

	go func() {
		wg.Wait()
		close(idle)
	}()

	select {
	case <-idle:
	case <-time.After(s.cfg.ShutdownGrace):
		cancel()
		<-idle
	}
}

// safeID returns id when it is a valid request id, else null.
func safeID(id json.RawMessage) json.RawMessage {
	if validID(id) {
		return id
	}

	return nil
}
