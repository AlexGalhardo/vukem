package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/mcp"
	"github.com/AlexGalhardo/vukem/mcp/mcptest"
)

type echoIn struct {
	Text string `json:"text" validate:"required,max=10" doc:"text to echo"`
}

type echoOut struct {
	Echo string `json:"echo"`
}

// logBuf is a goroutine-safe log sink.
type logBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.b.Write(p)
}

func (l *logBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.b.String()
}

func newServer(t *testing.T, cfg mcp.Config) (*mcp.Server, *logBuf) {
	t.Helper()

	logs := &logBuf{}
	cfg.Name, cfg.Version = "test", "1.2.3"
	cfg.Logger = slog.New(slog.NewTextHandler(logs, nil))

	return mcp.NewServer(cfg), logs
}

func addEcho(s *mcp.Server) {
	mcp.AddTool(s, mcp.Tool{
		Name: "echo", Description: "Echo the text.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: new(true)},
	}, func(_ context.Context, in echoIn) (echoOut, error) {
		return echoOut{Echo: in.Text}, nil
	})
}

func TestToolsListAndCall(t *testing.T) {
	t.Parallel()

	s, _ := newServer(t, mcp.Config{})
	addEcho(s)
	c := mcptest.NewClient(t, s)

	var list struct {
		ResultType string `json:"resultType"`
		Tools      []struct {
			Name         string         `json:"name"`
			InputSchema  map[string]any `json:"inputSchema"`
			OutputSchema map[string]any `json:"outputSchema"`
			Annotations  map[string]any `json:"annotations"`
		} `json:"tools"`
		Meta map[string]any `json:"_meta"`
	}

	if err := c.Call("tools/list", nil).Decode(&list); err != nil {
		t.Fatal(err)
	}

	if list.ResultType != "complete" || len(list.Tools) != 1 || list.Tools[0].Name != "echo" {
		t.Fatalf("tools/list = %+v, want one complete tool echo", list)
	}

	in := list.Tools[0].InputSchema
	if in["type"] != "object" || in["additionalProperties"] != false {
		t.Errorf("inputSchema = %v, want a closed object", in)
	}

	text := in["properties"].(map[string]any)["text"].(map[string]any)
	if text["description"] != "text to echo" || text["maxLength"] != float64(10) {
		t.Errorf("text schema = %v, want doc and maxLength from the tags", text)
	}

	if list.Tools[0].OutputSchema == nil || list.Tools[0].Annotations["readOnlyHint"] != true {
		t.Errorf("tool = %+v, want an output schema and annotations", list.Tools[0])
	}

	if list.Meta["io.modelcontextprotocol/serverInfo"] == nil {
		t.Errorf("_meta = %v, want serverInfo", list.Meta)
	}

	res := c.CallTool("echo", map[string]any{"text": "hi"})
	if res.IsError || res.Text() != `{"echo":"hi"}` || string(res.StructuredContent) != `{"echo":"hi"}` {
		t.Errorf("CallTool = %+v, want structured content and its JSON as text", res)
	}
}

func TestToolInputErrors(t *testing.T) {
	t.Parallel()

	s, _ := newServer(t, mcp.Config{})
	addEcho(s)
	c := mcptest.NewClient(t, s)

	tests := []struct {
		name    string
		args    any
		toolErr bool
		code    int
	}{
		{"too long is a tool error", map[string]any{"text": strings.Repeat("x", 11)}, true, 0},
		{"missing is a tool error", map[string]any{}, true, 0},
		{"unknown field is a tool error", map[string]any{"text": "a", "extra": 1}, true, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			res := c.CallTool("echo", tc.args)
			if res.Err != nil || !res.IsError {
				t.Errorf("CallTool(%v) = %+v, want isError", tc.args, res)
			}
		})
	}

	if rep := c.Call(
		"tools/call",
		map[string]any{"name": "nope"},
	); rep.Error == nil ||
		rep.Error.Code != mcp.CodeInvalidParams {
		t.Errorf("unknown tool = %+v, want -32602", rep)
	}
}

func TestSecurity_A05_MCPToolErrorsHidden(t *testing.T) {
	t.Parallel()

	s, logs := newServer(t, mcp.Config{})
	secret := "/etc/secret/db-password=hunter2"

	mcp.AddTool(s, mcp.Tool{Name: "boom"}, func(context.Context, echoIn) (echoOut, error) { panic(secret) })
	mcp.AddTool(s, mcp.Tool{Name: "fail"}, func(context.Context, echoIn) (echoOut, error) {
		return echoOut{}, errors.New("pq: password authentication failed " + secret)
	})
	mcp.AddTool(s, mcp.Tool{Name: "soft"}, func(context.Context, echoIn) (echoOut, error) {
		return echoOut{}, mcp.ToolErrorf("the invoice %s is closed", "42")
	})

	c := mcptest.NewClient(t, s)

	for _, name := range []string{"boom", "fail"} {
		rep := c.Call("tools/call", map[string]any{"name": name, "arguments": map[string]any{"text": "x"}})
		if rep.Error == nil || rep.Error.Code != mcp.CodeInternal || rep.Error.Message != "Internal error" {
			t.Fatalf("%s = %+v, want a bare -32603", name, rep)
		}

		if strings.Contains(string(rep.Result), "hunter2") || rep.Error.Data != nil {
			t.Errorf("%s leaked detail: %+v", name, rep)
		}
	}

	if got := logs.String(); !strings.Contains(got, "hunter2") {
		t.Errorf("the server log = %q, want the cause logged", got)
	}

	if res := c.CallTool(
		"soft",
		map[string]any{"text": "x"},
	); !res.IsError ||
		res.Text() != "the invoice 42 is closed" {
		t.Errorf("ToolError = %+v, want it shown to the model", res)
	}

	if res := c.CallTool("echo", nil); res.Err == nil {
		t.Error("the server must keep serving after a panic")
	}
}

func TestCancellationStopsTheTool(t *testing.T) {
	t.Parallel()

	s, _ := newServer(t, mcp.Config{})
	started, exited := make(chan struct{}, 8), make(chan struct{}, 8)

	mcp.AddTool(s, mcp.Tool{Name: "wait"}, func(ctx context.Context, _ echoIn) (echoOut, error) {
		started <- struct{}{}

		<-ctx.Done()

		exited <- struct{}{}

		return echoOut{}, ctx.Err()
	})

	c := mcptest.NewClient(t, s)
	id := c.Start("tools/call", map[string]any{"name": "wait", "arguments": map[string]any{"text": "x"}})

	<-started
	c.Cancel(id)

	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("the tool context was not cancelled")
	}

	// The cancelled request gets no response, and the server still answers.
	if rep := c.Call("tools/list", nil); rep.Error != nil {
		t.Errorf("tools/list after cancel = %+v", rep)
	}
}

func TestCancellationDoesNotLeakGoroutines(t *testing.T) {
	before := runtime.NumGoroutine()

	t.Run("session", func(t *testing.T) {
		s, _ := newServer(t, mcp.Config{ShutdownGrace: 50 * time.Millisecond})
		started := make(chan struct{}, 64)

		mcp.AddTool(s, mcp.Tool{Name: "wait"}, func(ctx context.Context, _ echoIn) (echoOut, error) {
			started <- struct{}{}

			<-ctx.Done()

			return echoOut{}, ctx.Err()
		})

		c := mcptest.NewClient(t, s)

		for range 20 {
			id := c.Start("tools/call", map[string]any{"name": "wait", "arguments": map[string]any{"text": "x"}})
			<-started
			c.Cancel(id)
		}
		// Five more are still running when the input closes.
		for range 5 {
			c.Start("tools/call", map[string]any{"name": "wait", "arguments": map[string]any{"text": "x"}})
			<-started
		}
	})

	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if after := runtime.NumGoroutine(); after > before {
		buf := make([]byte, 1<<16)
		t.Errorf("goroutines = %d, want at most %d\n%s", after, before, buf[:runtime.Stack(buf, true)])
	}
}

func TestProgress(t *testing.T) {
	t.Parallel()

	s, _ := newServer(t, mcp.Config{})
	mcp.AddTool(s, mcp.Tool{Name: "work"}, func(ctx context.Context, _ echoIn) (echoOut, error) {
		mcp.Progress(ctx, 1, 2, "half")
		mcp.Progress(ctx, 2, 2, "done")

		return echoOut{Echo: "ok"}, nil
	})

	c := mcptest.NewClient(t, s)
	c.Call("tools/call", map[string]any{
		"name": "work", "arguments": map[string]any{"text": "x"},
		"_meta": map[string]any{"progressToken": "tok"},
	})

	var got []string

	for _, n := range c.Notifications() {
		if n.Method != "notifications/progress" {
			continue
		}

		var p struct {
			Token    string  `json:"progressToken"`
			Progress float64 `json:"progress"`
		}

		_ = json.Unmarshal(n.Params, &p)
		got = append(got, p.Token+":"+string(rune('0'+int(p.Progress))))
	}

	if diff := cmp.Diff([]string{"tok:1", "tok:2"}, got); diff != "" {
		t.Errorf("progress notifications (-want +got):\n%s", diff)
	}

	// No token, no notifications.
	n := len(c.Notifications())
	c.Call("tools/call", map[string]any{"name": "work", "arguments": map[string]any{"text": "x"}})

	if len(c.Notifications()) != n {
		t.Error("progress was sent without a progressToken")
	}
}

type confirmIn struct {
	ID string `json:"id" validate:"required"`
}

func TestConfirmationFlow(t *testing.T) {
	t.Parallel()

	s, _ := newServer(t, mcp.Config{})
	runs := 0

	mcp.AddTool(s, mcp.Tool{Name: "delete"}, func(ctx context.Context, in confirmIn) (echoOut, error) {
		ok, err := mcp.Confirm(ctx, "Delete "+in.ID+"?")
		if err != nil {
			return echoOut{}, err
		}

		if !ok {
			return echoOut{Echo: "kept"}, nil
		}

		runs++

		return echoOut{Echo: "deleted"}, nil
	})

	accept := mcptest.WithElicitation(func(string) mcp.ElicitResult { return mcp.ElicitResult{Action: "accept"} })
	decline := mcptest.WithElicitation(func(msg string) mcp.ElicitResult {
		if msg != "Delete 7?" {
			t.Errorf("elicitation message = %q", msg)
		}

		return mcp.ElicitResult{Action: "decline"}
	})

	if res := mcptest.NewClient(t, s, accept).
		CallTool("delete", map[string]any{"id": "7"}); res.Text() != `{"echo":"deleted"}` {
		t.Errorf("accepted = %+v, want deleted", res)
	}

	if res := mcptest.NewClient(t, s, decline).
		CallTool("delete", map[string]any{"id": "7"}); res.Text() != `{"echo":"kept"}` {
		t.Errorf("declined = %+v, want kept", res)
	}

	if runs != 1 {
		t.Errorf("the action ran %d times, want 1", runs)
	}

	// A client that cannot be asked gets MissingRequiredClientCapability.
	rep := mcptest.NewClient(t, s).
		Call("tools/call", map[string]any{"name": "delete", "arguments": map[string]any{"id": "7"}})
	if rep.Error == nil || rep.Error.Code != mcp.CodeMissingClientCapability {
		t.Errorf("no capability = %+v, want -32021", rep)
	}
}

func TestSecurity_A01_MCPRequestStateBound(t *testing.T) {
	t.Parallel()

	s, _ := newServer(t, mcp.Config{})
	ran := 0

	mcp.AddTool(s, mcp.Tool{Name: "delete"}, func(ctx context.Context, in confirmIn) (echoOut, error) {
		ok, err := mcp.Confirm(ctx, "Delete "+in.ID+"?")
		if err != nil {
			return echoOut{}, err
		}

		if ok {
			ran++
		}

		return echoOut{Echo: in.ID}, nil
	})

	c := mcptest.NewClient(
		t,
		s,
		mcptest.WithElicitation(func(string) mcp.ElicitResult { return mcp.ElicitResult{Action: "accept"} }),
	)
	answers := map[string]any{"confirm": map[string]any{"action": "accept"}}

	first := c.Call("tools/call", map[string]any{"name": "delete", "arguments": map[string]any{"id": "1"}})

	var head struct {
		RequestState string `json:"requestState"`
		ResultType   string `json:"resultType"`
	}

	if err := first.Decode(&head); err != nil || head.ResultType != "input_required" || head.RequestState == "" {
		t.Fatalf("first call = %+v, want input_required with a requestState", first)
	}

	tests := []struct {
		name   string
		params map[string]any
		want   string // "reask", "invalid" or "done"
	}{
		{
			"answers without state are ignored",
			map[string]any{"name": "delete", "arguments": map[string]any{"id": "1"}, "inputResponses": answers},
			"reask",
		},
		{
			"state from other arguments",
			map[string]any{
				"name":           "delete",
				"arguments":      map[string]any{"id": "2"},
				"inputResponses": answers,
				"requestState":   head.RequestState,
			},
			"invalid",
		},
		{
			"tampered state",
			map[string]any{
				"name":           "delete",
				"arguments":      map[string]any{"id": "1"},
				"inputResponses": answers,
				"requestState":   head.RequestState + "x",
			},
			"invalid",
		},
		{
			"garbage state",
			map[string]any{
				"name":           "delete",
				"arguments":      map[string]any{"id": "1"},
				"inputResponses": answers,
				"requestState":   "a.b",
			},
			"invalid",
		},
		{
			"matching state",
			map[string]any{
				"name":           "delete",
				"arguments":      map[string]any{"id": "1"},
				"inputResponses": answers,
				"requestState":   head.RequestState,
			},
			"done",
		},
	}

	for _, tc := range tests {
		func() {
			rep := c.Call("tools/call", tc.params)

			var h struct {
				ResultType string `json:"resultType"`
			}

			_ = json.Unmarshal(rep.Result, &h)

			got := "done"

			switch {
			case rep.Error != nil && rep.Error.Code == mcp.CodeInvalidParams:
				got = "invalid"
			case h.ResultType == "input_required":
				got = "reask"
			}

			if got != tc.want {
				t.Errorf("%s = %s (%+v), want %s", tc.name, got, rep, tc.want)
			}
		}()
	}

	if ran != 1 {
		t.Errorf("the confirmed action ran %d times, want 1", ran)
	}
}

func TestServerStateKeyCrossesInstances(t *testing.T) {
	t.Parallel()

	build := func(key string) *mcp.Server {
		s, _ := newServer(t, mcp.Config{StateKey: []byte(key)})
		mcp.AddTool(s, mcp.Tool{Name: "delete"}, func(ctx context.Context, _ confirmIn) (echoOut, error) {
			ok, err := mcp.Confirm(ctx, "sure?")

			return echoOut{Echo: map[bool]string{true: "yes", false: "no"}[ok]}, err
		})

		return s
	}

	call := func(s *mcp.Server, state string) mcptest.Reply {
		c := mcptest.NewClient(
			t,
			s,
			mcptest.WithElicitation(func(string) mcp.ElicitResult { return mcp.ElicitResult{Action: "accept"} }),
		)
		p := map[string]any{"name": "delete", "arguments": map[string]any{"id": "1"}}

		if state != "" {
			p["requestState"] = state
			p["inputResponses"] = map[string]any{"confirm": map[string]any{"action": "accept"}}
		}

		return c.Call("tools/call", p)
	}

	var head struct {
		RequestState string `json:"requestState"`
	}

	if err := call(build("k1"), "").Decode(&head); err != nil {
		t.Fatal(err)
	}

	if rep := call(build("k1"), head.RequestState); rep.Error != nil {
		t.Errorf("same key = %+v, want accepted", rep)
	}

	if rep := call(build("k2"), head.RequestState); rep.Error == nil {
		t.Error("a state signed with another key must be refused")
	}
}
