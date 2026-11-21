package mcptest_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/mcp"
	"github.com/AlexGalhardo/vukem/mcp/mcptest"
)

type echoIn struct {
	Text string `json:"text"`
}

type echoOut struct {
	Echo string `json:"echo"`
}

func newServer() *mcp.Server {
	s := mcp.NewServer(mcp.Config{
		Name: "test", Version: "1.0.0", Logger: slog.New(slog.DiscardHandler),
	})

	mcp.AddTool(s, mcp.Tool{Name: "echo", Description: "Echo the text."},
		func(_ context.Context, in echoIn) (echoOut, error) { return echoOut{Echo: in.Text}, nil })

	return s
}

func TestReplyDecode(t *testing.T) {
	t.Parallel()

	var out echoOut

	if err := (mcptest.Reply{Result: json.RawMessage(`{"echo":"hi"}`)}).Decode(&out); err != nil || out.Echo != "hi" {
		t.Errorf("Decode = %+v, %v", out, err)
	}

	want := &mcp.Error{Code: mcp.CodeInvalidRequest, Message: "no"}
	if err := (mcptest.Reply{Error: want}).Decode(&out); !errors.Is(err, want) {
		t.Errorf("Decode of an error = %v, want %v", err, want)
	}
}

func TestToolReplyText(t *testing.T) {
	t.Parallel()

	r := mcptest.ToolReply{Content: []mcp.Content{{Type: "text", Text: "a"}, {Type: "text", Text: "b"}}}
	if r.Text() != "ab" {
		t.Errorf("Text = %q, want ab", r.Text())
	}
}

func TestClientCalls(t *testing.T) {
	t.Parallel()

	c := mcptest.NewClient(t, newServer())

	var list struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}

	if err := c.Call("tools/list", nil).
		Decode(&list); err != nil || len(list.Tools) != 1 ||
		list.Tools[0].Name != "echo" {
		t.Fatalf("tools/list = %+v, %v", list, err)
	}

	id := c.Start("tools/list", map[string]any{"_meta": map[string]any{"custom/key": "kept"}})
	if rep := c.Wait(id); rep.Error != nil {
		t.Errorf("Start and Wait = %+v", rep.Error)
	}

	if res := c.CallTool("echo", map[string]any{"text": "hi"}); res.IsError || res.Text() != `{"echo":"hi"}` {
		t.Errorf("CallTool = %+v", res)
	}

	if res := c.CallTool("missing", map[string]any{}); res.Err == nil {
		t.Errorf("an unknown tool = %+v, want a protocol error", res)
	}

	if !strings.Contains(c.String(), "requests") {
		t.Errorf("String = %q", c.String())
	}
}

func TestClientAnswersElicitation(t *testing.T) {
	t.Parallel()

	s := newServer()

	mcp.AddTool(s, mcp.Tool{Name: "delete"}, func(ctx context.Context, _ echoIn) (echoOut, error) {
		ok, err := mcp.Confirm(ctx, "Delete it?")
		if err != nil {
			return echoOut{}, err
		}

		if !ok {
			return echoOut{Echo: "kept"}, nil
		}

		return echoOut{Echo: "deleted"}, nil
	})

	var asked string

	c := mcptest.NewClient(t, s, mcptest.WithElicitation(func(msg string) mcp.ElicitResult {
		asked = msg

		return mcp.ElicitResult{Action: "accept"}
	}))

	if res := c.CallTool("delete", map[string]any{"text": "x"}); res.Text() != `{"echo":"deleted"}` {
		t.Errorf("CallTool = %+v", res)
	}

	if asked != "Delete it?" {
		t.Errorf("the client was asked %q", asked)
	}
}

func TestClientCancel(t *testing.T) {
	t.Parallel()

	s := newServer()
	started, stopped := make(chan struct{}), make(chan struct{})

	mcp.AddTool(s, mcp.Tool{Name: "wait"}, func(ctx context.Context, _ echoIn) (echoOut, error) {
		close(started)
		<-ctx.Done()
		close(stopped)

		return echoOut{}, ctx.Err()
	})

	c := mcptest.NewClient(t, s)
	id := c.Start("tools/call", map[string]any{"name": "wait", "arguments": map[string]any{}})

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the tool did not start")
	}

	c.Cancel(id)

	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Cancel did not reach the tool")
	}
}

func TestClientKeepsMessagesWithoutAnID(t *testing.T) {
	t.Parallel()

	c := mcptest.NewClient(t, newServer())
	c.SendRaw("{not json")

	deadline := time.Now().Add(5 * time.Second)
	for len(c.Notifications()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the parse error of a malformed line never arrived")
		}

		time.Sleep(time.Millisecond)
	}
}
