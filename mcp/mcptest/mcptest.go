// Package mcptest tests an mcp.Server with an in-memory client: it speaks the
// stdio framing over pipes, adds the per-request metadata of revision
// 2026-07-28 and answers the input_required flow.
//
//	c := mcptest.NewClient(t, srv)
//	res := c.CallTool("get_invoice", map[string]any{"id": "42"})
//	if res.IsError { t.Fatal(res.Text()) }
package mcptest

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/mcp"
)

// Reply is the answer to one request.
type Reply struct {
	Result json.RawMessage
	Error  *mcp.Error
}

// Decode unmarshals the result into v.
func (r Reply) Decode(v any) error {
	if r.Error != nil {
		return r.Error
	}

	return json.Unmarshal(r.Result, v)
}

// Notification is a server notification received by the client.
type Notification struct {
	Method string
	Params json.RawMessage
}

// ToolReply is the result of tools/call.
type ToolReply struct {
	Content           []mcp.Content   `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent"`
	IsError           bool            `json:"isError"`
	// Err is the JSON-RPC error, when the call failed at protocol level.
	Err *mcp.Error `json:"-"`
}

// Text joins the text blocks of the result.
func (r ToolReply) Text() string {
	var s strings.Builder
	for i := range r.Content {
		s.WriteString(r.Content[i].Text)
	}

	return s.String()
}

// Client is an in-memory MCP client bound to one server.
type Client struct {
	t        testing.TB
	toServer *io.PipeWriter
	cancel   context.CancelFunc
	done     chan struct{}

	mu      sync.Mutex
	nextID  int
	waiting map[int]chan Reply
	notes   []Notification
	caps    map[string]any
	elicit  func(message string) mcp.ElicitResult
}

// Option configures a Client.
type Option func(*Client)

// WithElicitation declares the elicitation capability and answers every
// request with fn, which sees the message the server shows the user.
func WithElicitation(fn func(message string) mcp.ElicitResult) Option {
	return func(c *Client) {
		c.caps["elicitation"] = map[string]any{}
		c.elicit = fn
	}
}

// NewClient connects a client to srv over pipes and closes both at the end of
// the test, failing it when the server does not stop.
func NewClient(tb testing.TB, srv *mcp.Server, opts ...Option) *Client {
	tb.Helper()

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()

	ctx, cancel := context.WithCancel(context.Background())
	c := &Client{
		t: tb, toServer: inW, cancel: cancel, done: make(chan struct{}),
		waiting: map[int]chan Reply{}, caps: map[string]any{},
	}

	for _, o := range opts {
		o(c)
	}

	go func() {
		defer close(c.done)

		_ = srv.ServeStdio(ctx, inR, outW)

		_ = outW.Close()
	}()

	go c.read(outR)

	tb.Cleanup(func() {
		_ = inW.Close()

		select {
		case <-c.done:
		case <-time.After(5 * time.Second):
			tb.Error("mcptest: the server did not stop after its input closed")
			cancel()
		}

		cancel()
		_ = outR.Close()
	})

	return c
}

func (c *Client) read(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(nil, 16<<20)

	for sc.Scan() {
		var m struct {
			ID     *int            `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
			Error  *mcp.Error      `json:"error"`
		}

		if json.Unmarshal(sc.Bytes(), &m) != nil {
			c.t.Errorf("mcptest: server wrote a line that is not JSON: %q", sc.Text())

			continue
		}

		c.mu.Lock()

		if m.ID == nil {
			c.notes = append(c.notes, Notification{Method: m.Method, Params: m.Params})
			c.mu.Unlock()

			continue
		}

		ch := c.waiting[*m.ID]
		c.mu.Unlock()

		if ch != nil {
			ch <- Reply{Result: m.Result, Error: m.Error}
		}
	}
}

// Start sends a request and returns its id without waiting; pair it with Wait.
func (c *Client) Start(method string, params map[string]any) int {
	c.t.Helper()

	c.mu.Lock()
	c.nextID++
	id := c.nextID
	c.waiting[id] = make(chan Reply, 1)
	c.mu.Unlock()

	p := maps.Clone(params)
	if p == nil {
		p = map[string]any{}
	}

	meta, _ := p["_meta"].(map[string]any)

	m := map[string]any{
		"io.modelcontextprotocol/protocolVersion":    mcp.Version,
		"io.modelcontextprotocol/clientCapabilities": c.caps,
		"io.modelcontextprotocol/clientInfo":         map[string]any{"name": "mcptest", "version": "0"},
	}
	maps.Copy(m, meta)

	p["_meta"] = m

	c.Send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": p})

	return id
}

// Wait returns the reply of a request started with Start, failing the test
// after five seconds.
func (c *Client) Wait(id int) Reply {
	c.t.Helper()

	c.mu.Lock()
	ch := c.waiting[id]
	c.mu.Unlock()

	select {
	case r := <-ch:
		c.mu.Lock()
		delete(c.waiting, id)
		c.mu.Unlock()

		return r
	case <-time.After(5 * time.Second):
		c.t.Fatalf("mcptest: no reply to request %d", id)

		return Reply{}
	}
}

// Call sends a request and waits for its reply.
func (c *Client) Call(method string, params map[string]any) Reply {
	c.t.Helper()

	return c.Wait(c.Start(method, params))
}

// Send writes a raw message (a map or struct, marshalled to one line).
func (c *Client) Send(msg any) {
	c.t.Helper()

	b, err := json.Marshal(msg)
	if err != nil {
		c.t.Fatalf("mcptest: marshal: %v", err)
	}

	c.SendRaw(string(b))
}

// SendRaw writes one line as is, for malformed-input tests.
func (c *Client) SendRaw(line string) {
	c.t.Helper()

	if _, err := io.WriteString(c.toServer, line+"\n"); err != nil {
		c.t.Fatalf("mcptest: write: %v", err)
	}
}

// Cancel sends notifications/cancelled for a request.
func (c *Client) Cancel(id int) {
	c.t.Helper()

	c.Send(map[string]any{
		"jsonrpc": "2.0", "method": "notifications/cancelled",
		"params": map[string]any{"requestId": id, "reason": "test"},
	})
}

// Notifications returns the notifications received so far.
func (c *Client) Notifications() []Notification {
	c.mu.Lock()
	defer c.mu.Unlock()

	return append([]Notification(nil), c.notes...)
}

// CallTool calls a tool and, when the server asks for input, answers with the
// WithElicitation function and retries (at most five rounds).
func (c *Client) CallTool(name string, args any) ToolReply {
	c.t.Helper()

	params := map[string]any{"name": name, "arguments": args}

	for range 5 {
		rep := c.Call("tools/call", params)
		if rep.Error != nil {
			return ToolReply{Err: rep.Error}
		}

		var head struct {
			ResultType    string                     `json:"resultType"`
			InputRequests map[string]json.RawMessage `json:"inputRequests"`
			RequestState  string                     `json:"requestState"`
		}

		if err := json.Unmarshal(rep.Result, &head); err != nil {
			c.t.Fatalf("mcptest: tools/call result: %v", err)
		}

		if head.ResultType != "input_required" {
			var out ToolReply
			if err := json.Unmarshal(rep.Result, &out); err != nil {
				c.t.Fatalf("mcptest: tools/call result: %v", err)
			}

			return out
		}

		if c.elicit == nil {
			c.t.Fatalf("mcptest: the server asked for input but the client has no WithElicitation")
		}

		answers := map[string]any{}

		for key, raw := range head.InputRequests {
			var rq struct {
				Params struct {
					Message string `json:"message"`
				} `json:"params"`
			}

			_ = json.Unmarshal(raw, &rq)
			answers[key] = c.elicit(rq.Params.Message)
		}

		params = map[string]any{
			"name": name, "arguments": args, "inputResponses": answers, "requestState": head.RequestState,
		}
	}

	c.t.Fatal("mcptest: too many input_required rounds")

	return ToolReply{}
}

// String describes the client for failure messages.
func (c *Client) String() string { return fmt.Sprintf("mcptest.Client(%d requests)", c.nextID) }
