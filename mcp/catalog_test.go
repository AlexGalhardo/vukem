package mcp_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/mcp"
	"github.com/AlexGalhardo/vukem/mcp/mcptest"
)

type promptArgs struct {
	Language string `json:"language"        validate:"required" doc:"programming language"`
	Style    string `json:"style,omitempty"`
}

func fullServer(t *testing.T, cfg mcp.Config) *mcp.Server {
	t.Helper()

	s, _ := newServer(t, cfg)
	addEcho(s)

	mcp.AddResource(s, mcp.Resource{URI: "app://readme", Name: "readme", MIMEType: "text/plain"},
		func(context.Context) ([]mcp.ResourceContents, error) {
			return []mcp.ResourceContents{{Text: "hello"}}, nil
		})
	mcp.AddResourceTemplate(s, mcp.ResourceTemplate{URITemplate: "app://files/{path}", Name: "files"},
		func(_ context.Context, uri string, vars map[string]string) ([]mcp.ResourceContents, error) {
			if vars["path"] == "missing" {
				return nil, mcp.ErrNotFound
			}

			return []mcp.ResourceContents{{URI: uri, Text: "file:" + vars["path"]}}, nil
		},
		func(_ context.Context, _, value string, _ map[string]string) (mcp.Completion, error) {
			return mcp.Completion{Values: []string{value + "1", value + "2"}}, nil
		})
	mcp.AddPrompt(s, mcp.Prompt{Name: "review", Description: "Review code."},
		func(_ context.Context, a promptArgs) (mcp.PromptResult, error) {
			return mcp.PromptResult{Messages: []mcp.PromptMessage{
				{Role: "user", Content: mcp.Text("Review this " + a.Language + " code")},
			}}, nil
		},
		func(_ context.Context, arg, value string, _ map[string]string) (mcp.Completion, error) {
			if arg != "language" {
				return mcp.Completion{}, nil
			}

			vals := make([]string, 150)
			for i := range vals {
				vals[i] = fmt.Sprintf("%s%d", value, i)
			}

			return mcp.Completion{Values: vals}, nil
		})

	return s
}

func TestDiscover(t *testing.T) {
	t.Parallel()

	c := mcptest.NewClient(t, fullServer(t, mcp.Config{}))

	var d struct {
		ResultType        string                    `json:"resultType"`
		SupportedVersions []string                  `json:"supportedVersions"`
		Capabilities      map[string]map[string]any `json:"capabilities"`
		CacheScope        string                    `json:"cacheScope"`
	}

	if err := c.Call("server/discover", nil).Decode(&d); err != nil {
		t.Fatal(err)
	}

	if d.ResultType != "complete" || d.SupportedVersions[0] != mcp.Version || d.CacheScope != "private" {
		t.Errorf("server/discover = %+v", d)
	}

	for _, k := range []string{"tools", "resources", "prompts", "completions"} {
		if _, ok := d.Capabilities[k]; !ok {
			t.Errorf("capabilities = %v, missing %s", d.Capabilities, k)
		}
	}

	if rep := c.Call("ping", nil); rep.Error == nil || rep.Error.Code != mcp.CodeMethodNotFound {
		t.Errorf("ping = %+v, want -32601: it was removed in this revision", rep)
	}

	if rep := c.Call("logging/setLevel", map[string]any{"level": "info"}); rep.Error == nil {
		t.Error("logging/setLevel must not be served")
	}
}

func TestResourcesAndTemplates(t *testing.T) {
	t.Parallel()

	c := mcptest.NewClient(t, fullServer(t, mcp.Config{}))

	read := func(uri string) mcptest.Reply { return c.Call("resources/read", map[string]any{"uri": uri}) }

	var r struct {
		Contents []mcp.ResourceContents `json:"contents"`
		TTL      *int                   `json:"ttlMs"`
	}

	if err := read(
		"app://readme",
	).Decode(&r); err != nil || r.Contents[0].Text != "hello" || r.Contents[0].URI != "app://readme" ||
		r.TTL == nil {
		t.Errorf("read readme = %+v, %v", r, err)
	}

	if err := read("app://files/a.txt").Decode(&r); err != nil || r.Contents[0].Text != "file:a.txt" {
		t.Errorf("read template = %+v, %v", r, err)
	}

	for _, uri := range []string{"app://nothing", "app://files/missing", "app://files/../etc/passwd", "app://files/a/b", "app://files/%2e%2e", "app://files/%00"} {
		rep := read(uri)
		if rep.Error == nil || rep.Error.Code != mcp.CodeInvalidParams {
			t.Errorf("read %q = %+v, want -32602 not found", uri, rep)
		}
	}

	var l struct {
		Resources         []map[string]any `json:"resources"`
		ResourceTemplates []map[string]any `json:"resourceTemplates"`
	}

	_ = c.Call("resources/list", nil).Decode(&l)
	_ = c.Call("resources/templates/list", nil).Decode(&l)

	if len(l.Resources) != 1 || l.ResourceTemplates[0]["uriTemplate"] != "app://files/{path}" {
		t.Errorf("lists = %+v", l)
	}
}

func TestPromptsAndCompletion(t *testing.T) {
	t.Parallel()

	c := mcptest.NewClient(t, fullServer(t, mcp.Config{}))

	var list struct {
		Prompts []struct {
			Arguments []struct {
				Name     string `json:"name"`
				Required bool   `json:"required"`
			} `json:"arguments"`
		} `json:"prompts"`
	}

	_ = c.Call("prompts/list", nil).Decode(&list)

	if got := list.Prompts[0].Arguments; len(got) != 2 || got[0].Name != "language" || !got[0].Required ||
		got[1].Required {
		t.Errorf("prompt arguments = %+v", got)
	}

	var got struct {
		Messages []mcp.PromptMessage `json:"messages"`
	}

	if err := c.Call("prompts/get", map[string]any{"name": "review", "arguments": map[string]any{"language": "go"}}).
		Decode(&got); err != nil ||
		got.Messages[0].Content.Text != "Review this go code" {
		t.Errorf("prompts/get = %+v, %v", got, err)
	}

	for _, p := range []map[string]any{
		{"name": "review"},
		{"name": "review", "arguments": map[string]any{"language": "go", "x": "y"}},
		{"name": "ghost"},
	} {
		if rep := c.Call("prompts/get", p); rep.Error == nil || rep.Error.Code != mcp.CodeInvalidParams {
			t.Errorf("prompts/get %v = %+v, want -32602", p, rep)
		}
	}

	var comp struct {
		Completion struct {
			Values  []string `json:"values"`
			HasMore bool     `json:"hasMore"`
		} `json:"completion"`
	}

	complete := func(ref map[string]any) error {
		return c.Call("completion/complete", map[string]any{"ref": ref, "argument": map[string]any{"name": "language", "value": "g"}}).
			Decode(&comp)
	}

	if err := complete(
		map[string]any{"type": "ref/prompt", "name": "review"},
	); err != nil || len(comp.Completion.Values) != 100 ||
		!comp.Completion.HasMore {
		t.Errorf(
			"completion = %d values hasMore=%v, %v; want 100 and more",
			len(comp.Completion.Values),
			comp.Completion.HasMore,
			err,
		)
	}

	if err := complete(
		map[string]any{"type": "ref/resource", "uri": "app://files/{path}"},
	); err != nil ||
		comp.Completion.Values[0] != "g1" {
		t.Errorf("template completion = %+v, %v", comp, err)
	}

	if err := complete(map[string]any{"type": "ref/prompt", "name": "ghost"}); err == nil {
		t.Error("completing an unknown prompt must fail")
	}
}

func TestPagination(t *testing.T) {
	t.Parallel()

	s, _ := newServer(t, mcp.Config{PageSize: 2})

	for _, n := range []string{"e", "a", "d", "b", "c"} {
		mcp.AddTool(s, mcp.Tool{Name: n}, func(context.Context, echoIn) (echoOut, error) { return echoOut{}, nil })
	}

	c := mcptest.NewClient(t, s)

	var names []string

	cursor := ""

	for range 5 {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}

		var p struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
			Next string `json:"nextCursor"`
		}

		if err := c.Call("tools/list", params).Decode(&p); err != nil {
			t.Fatal(err)
		}

		for _, tl := range p.Tools {
			names = append(names, tl.Name)
		}

		if cursor = p.Next; cursor == "" {
			break
		}
	}

	if diff := cmp.Diff([]string{"a", "b", "c", "d", "e"}, names); diff != "" {
		t.Errorf("paged tool names (-want +got):\n%s", diff)
	}

	if rep := c.Call(
		"tools/list",
		map[string]any{"cursor": "***"},
	); rep.Error == nil ||
		rep.Error.Code != mcp.CodeInvalidParams {
		t.Errorf("invalid cursor = %+v, want -32602", rep)
	}
}

func TestRegistrationPanics(t *testing.T) {
	t.Parallel()

	s, _ := newServer(t, mcp.Config{})
	addEcho(s)

	noop := func(context.Context, echoIn) (echoOut, error) { return echoOut{}, nil }

	tests := map[string]func(){
		"duplicate tool": func() { mcp.AddTool(s, mcp.Tool{Name: "echo"}, noop) },
		"bad tool name":  func() { mcp.AddTool(s, mcp.Tool{Name: "has space"}, noop) },
		"non-struct input": func() {
			mcp.AddTool(
				s,
				mcp.Tool{Name: "x"},
				func(context.Context, string) (echoOut, error) { return echoOut{}, nil },
			)
		},
		"bad template": func() {
			mcp.AddResourceTemplate(s, mcp.ResourceTemplate{URITemplate: "app://static", Name: "n"}, nil, nil)
		},
		"non-string prompt argument": func() {
			mcp.AddPrompt(s, mcp.Prompt{Name: "p"}, func(context.Context, struct{ N int }) (mcp.PromptResult, error) {
				return mcp.PromptResult{}, nil
			}, nil)
		},
	}

	for name, fn := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			defer func() {
				if recover() == nil {
					t.Errorf("%s did not panic", name)
				}
			}()

			fn()
		})
	}
}

// rawSession runs ServeStdio over a script of input lines and returns what it
// wrote to stdout.
func rawSession(t *testing.T, s *mcp.Server, input string) []string {
	t.Helper()

	var out bytes.Buffer

	if err := s.ServeStdio(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatalf("ServeStdio = %v", err)
	}

	var lines []string

	sc := bufio.NewScanner(&out)
	sc.Buffer(nil, 1<<24)

	for sc.Scan() {
		lines = append(lines, sc.Text())
	}

	// Requests run concurrently: order the answers by id (null ids first, in
	// arrival order) so a script reads top to bottom.
	sort.SliceStable(lines, func(i, j int) bool { return lineID(lines[i]) < lineID(lines[j]) })

	return lines
}

func lineID(line string) float64 {
	var m struct {
		ID *float64 `json:"id"`
	}

	_ = json.Unmarshal([]byte(line), &m)

	if m.ID == nil {
		return -1
	}

	return *m.ID
}

func TestLegacyInitializeOverStdio(t *testing.T) {
	t.Parallel()

	s := fullServer(t, mcp.Config{})
	got := rawSession(t, s, strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"old","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"text":"hi"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`,
		"",
	}, "\n"))

	if len(got) != 4 {
		t.Fatalf("got %d responses, want 4: %q", len(got), got)
	}

	var init struct {
		Result struct {
			ProtocolVersion string         `json:"protocolVersion"`
			Capabilities    map[string]any `json:"capabilities"`
		} `json:"result"`
	}

	_ = json.Unmarshal([]byte(got[0]), &init)

	if init.Result.ProtocolVersion != "2025-06-18" || init.Result.Capabilities["tools"] == nil {
		t.Errorf("initialize = %s", got[0])
	}

	if !strings.Contains(got[1], `"result":{}`) {
		t.Errorf("legacy ping = %s, want an empty result", got[1])
	}

	if strings.Contains(got[2], "resultType") || !strings.Contains(got[2], `"structuredContent"`) {
		t.Errorf("legacy tools/call = %s, want no resultType", got[2])
	}

	if !strings.Contains(got[3], `"protocolVersion":"2025-11-25"`) {
		t.Errorf("unknown version = %s, want the server's latest legacy one", got[3])
	}
}

func TestSecurity_A04_MCPStdioRobustness(t *testing.T) {
	t.Parallel()

	s := fullServer(t, mcp.Config{MaxMessageBytes: 100 << 10})
	big := `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"echo","arguments":{"text":"` + strings.Repeat(
		"a",
		200<<10,
	) + `"}}}`
	deep := strings.Repeat("[", 20000) + strings.Repeat("]", 20000)

	input := strings.Join([]string{
		big,
		`{not json`,
		`[1,2]`,
		`{"jsonrpc":"1.0","id":1,"method":"x"}`,
		`{"jsonrpc":"2.0","id":null,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":{"a":1},"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/list","params":[1]}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"1900-01-01","io.modelcontextprotocol/clientCapabilities":{}}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"echo","arguments":` + deep + `}}`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`,
		"",
	}, "\n")

	got := rawSession(t, s, input)

	for _, line := range got {
		if !json.Valid([]byte(line)) || strings.Contains(line, "\n") {
			t.Fatalf("stdout carried something that is not a JSON message: %q", line)
		}
	}

	var codes []int

	for _, line := range got {
		var m struct {
			Error *mcp.Error `json:"error"`
		}

		_ = json.Unmarshal([]byte(line), &m)

		if m.Error == nil {
			codes = append(codes, 0)

			continue
		}

		codes = append(codes, m.Error.Code)
	}

	slices.Sort(codes)

	want := []int{
		mcp.CodeParse,
		mcp.CodeParse,
		mcp.CodeParse, // not JSON, an array, nesting past the decoder limit
		mcp.CodeUnsupportedProtocolVersion,
		mcp.CodeInvalidRequest,
		mcp.CodeInvalidRequest,
		mcp.CodeInvalidRequest,
		mcp.CodeInvalidRequest, // oversized, jsonrpc 1.0, null id, object id
		mcp.CodeInvalidParams,
		mcp.CodeInvalidParams, // params is an array; no clientCapabilities
		0,                     // the last, valid request
	}
	slices.Sort(want)

	if diff := cmp.Diff(want, codes); diff != "" {
		t.Errorf("error codes (-want +got):\n%s\n%s", diff, strings.Join(got, "\n"))
	}
}

func TestStdioWritesNothingButMessages(t *testing.T) {
	t.Parallel()

	s := fullServer(t, mcp.Config{})
	pr, pw := io.Pipe()

	var out bytes.Buffer

	done := make(chan error, 1)

	go func() { done <- s.ServeStdio(context.Background(), pr, &out) }()

	_, _ = io.WriteString(
		pw,
		`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`+"\n",
	)
	_ = pw.Close()

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ServeStdio did not return at EOF")
	}

	if lines := strings.Count(out.String(), "\n"); lines != 1 || !json.Valid(bytes.TrimSpace(out.Bytes())) {
		t.Errorf("stdout = %q, want exactly one JSON line", out.String())
	}
}

// VF-111: a handler that returns an *mcp.Error, wrapped or not, answers
// resources/read with that typed JSON-RPC error, as prompts already do. An
// untyped error stays a bare -32603 and ErrNotFound a -32602.
func TestResourceReadHonorsTypedError(t *testing.T) {
	t.Parallel()

	const codeLimit = -32001

	s, _ := newServer(t, mcp.Config{})
	limited := &mcp.Error{Code: codeLimit, Message: "usage limit reached", Data: map[string]any{"limit": 5}}

	mcp.AddResource(s, mcp.Resource{URI: "app://limited", Name: "limited"},
		func(context.Context) ([]mcp.ResourceContents, error) { return nil, limited })
	mcp.AddResource(s, mcp.Resource{URI: "app://wrapped", Name: "wrapped"},
		func(context.Context) ([]mcp.ResourceContents, error) { return nil, fmt.Errorf("read: %w", limited) })
	mcp.AddResource(s, mcp.Resource{URI: "app://plain", Name: "plain"},
		func(context.Context) ([]mcp.ResourceContents, error) { return nil, errors.New("pq: secret") })
	mcp.AddResourceTemplate(s, mcp.ResourceTemplate{URITemplate: "app://t/{id}", Name: "t"},
		func(context.Context, string, map[string]string) ([]mcp.ResourceContents, error) { return nil, limited },
		nil)

	c := mcptest.NewClient(t, s)

	for _, uri := range []string{"app://limited", "app://wrapped", "app://t/1"} {
		rep := c.Call("resources/read", map[string]any{"uri": uri})
		if rep.Error == nil || rep.Error.Code != codeLimit || rep.Error.Message != "usage limit reached" {
			t.Fatalf("%s = %+v, want the typed error", uri, rep)
		}

		data, _ := rep.Error.Data.(map[string]any)
		if data["limit"] != float64(5) {
			t.Errorf("%s data = %v, want the limit", uri, rep.Error.Data)
		}
	}

	rep := c.Call("resources/read", map[string]any{"uri": "app://plain"})
	if rep.Error == nil || rep.Error.Code != mcp.CodeInternal || strings.Contains(string(rep.Result), "secret") {
		t.Errorf("untyped error = %+v, want a bare -32603", rep)
	}
}
