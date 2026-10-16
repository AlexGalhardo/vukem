package vukem

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

type rpcReply struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type mcpToolResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError"`
}

// mcpSession runs boost:mcp over the given JSON-RPC lines and returns the
// replies keyed by id.
func mcpSession(t *testing.T, docsURL string, lines ...string) map[int]rpcReply {
	t.Helper()

	var out bytes.Buffer

	dsn := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "boost.sqlite"))
	app := newTestApp(t,
		WithSource(testSource(map[string]string{"DATABASE_URL": dsn, "APP_URL": "https://shop.test"})),
		WithStdin(strings.NewReader(strings.Join(lines, "\n")+"\n")), WithStdout(&out),
		WithRoutes(func(r *router.Router, _ *App) error {
			r.Get("/posts/{id}", func(*web.Ctx) error { return nil }).Name("posts.show")
			return nil
		}))

	db, err := app.DB()
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.ExecContext(
		t.Context(),
		"CREATE TABLE posts (id INTEGER PRIMARY KEY, title TEXT NOT NULL); INSERT INTO posts (title) VALUES ('Hello')",
	); err != nil {
		t.Fatal(err)
	}

	srv := newBoostServer(app)
	if docsURL != "" {
		srv.docsURL = docsURL
	}

	if err := srv.serve(t.Context()); err != nil {
		t.Fatalf("serve() error = %v", err)
	}

	replies := map[int]rpcReply{}

	sc := bufio.NewScanner(&out)
	for sc.Scan() {
		var r rpcReply
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatalf("stdout line %q is not JSON-RPC: %v", sc.Text(), err)
		}

		replies[r.ID] = r
	}

	return replies
}

func call(id int, tool string, args map[string]any) string {
	b, _ := json.Marshal(map[string]any{ //nolint:errchkjson // test literals always encode
		"jsonrpc": "2.0", "id": id, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": args},
	})

	return string(b)
}

func toolText(t *testing.T, r rpcReply) (string, bool) {
	t.Helper()

	if r.Error != nil {
		t.Fatalf("rpc error %d %s", r.Error.Code, r.Error.Message)
	}

	var tr mcpToolResult
	if err := json.Unmarshal(r.Result, &tr); err != nil || len(tr.Content) != 1 {
		t.Fatalf("tool result %s: %v", r.Result, err)
	}

	return tr.Content[0].Text, tr.IsError
}

func TestBoostMCPProtocol(t *testing.T) {
	t.Parallel()

	replies := mcpSession(
		t,
		"",
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":4,"method":"nope"}`,
		`not json`,
		call(5, "missing_tool", map[string]any{}),
	)

	var init struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
		Capabilities map[string]any `json:"capabilities"`
	}
	if err := json.Unmarshal(replies[1].Result, &init); err != nil || init.ProtocolVersion != "2025-06-18" ||
		init.ServerInfo.Name != "vukem-boost" || init.Capabilities["tools"] == nil {
		t.Errorf("initialize = %s (%v)", replies[1].Result, err)
	}

	var list struct {
		Tools []struct {
			Name        string         `json:"name"`
			InputSchema map[string]any `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(replies[2].Result, &list); err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, tool := range list.Tools {
		names = append(names, tool.Name)
		if tool.InputSchema["type"] != "object" {
			t.Errorf("tool %s inputSchema = %v", tool.Name, tool.InputSchema)
		}
	}

	want := []string{
		"application_info", "database_query", "database_schema", "get_absolute_url",
		"list_commands", "list_routes", "search_docs",
	}
	if diff := cmp.Diff(want, names); diff != "" {
		t.Errorf("tools mismatch (-want +got):\n%s", diff)
	}

	if string(replies[3].Result) != "{}" {
		t.Errorf("ping = %s", replies[3].Result)
	}

	if replies[4].Error == nil || replies[4].Error.Code != -32601 {
		t.Errorf("unknown method = %+v, want -32601", replies[4].Error)
	}

	if replies[0].Error == nil || replies[0].Error.Code != -32700 {
		t.Errorf("parse error reply = %+v, want -32700", replies[0].Error)
	}

	if replies[5].Error == nil || replies[5].Error.Code != -32602 {
		t.Errorf("unknown tool = %+v, want -32602", replies[5].Error)
	}
}

func TestBoostTools(t *testing.T) {
	t.Parallel()

	docs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("# Vukem documentation\n\n---\n\n# Routing\n\nNamed routes with r.Get.\n\n" +
			"---\n\n# Queues\n\nTyped jobs and workers.\n"))
	}))
	t.Cleanup(docs.Close)

	replies := mcpSession(t, docs.URL,
		call(1, "application_info", map[string]any{}),
		call(2, "list_routes", map[string]any{"path": "posts"}),
		call(3, "database_schema", map[string]any{}),
		call(4, "database_query", map[string]any{"query": "SELECT title FROM posts"}),
		call(5, "get_absolute_url", map[string]any{"name": "posts.show", "params": map[string]any{"id": "7"}}),
		call(6, "search_docs", map[string]any{"query": "typed jobs"}),
		call(7, "list_commands", map[string]any{}),
	)

	checks := []struct {
		id   int
		want []string
	}{
		{1, []string{`"environment": "test"`, `"database": "sqlite"`, `"go":`}},
		{2, []string{`"path": "/posts/{id}"`, `"name": "posts.show"`}},
		{3, []string{`"posts"`, `"title"`, `"TEXT"`}},
		{4, []string{`"title": "Hello"`}},
		{5, []string{"https://shop.test/posts/7"}},
		{6, []string{"# Queues", "Typed jobs"}},
		{7, []string{"route:list", "migrate"}},
	}

	for _, c := range checks {
		text, isErr := toolText(t, replies[c.id])
		for _, w := range c.want {
			if isErr || !strings.Contains(text, w) {
				t.Errorf("tool %d = %q (error %v), want it to contain %q", c.id, text, isErr, w)
			}
		}
	}

	if text, _ := toolText(t, replies[2]); strings.Contains(text, `"/up"`) {
		t.Errorf("list_routes path filter kept /up: %s", text)
	}

	if text, _ := toolText(t, replies[6]); strings.Contains(text, "Named routes") {
		t.Errorf("search_docs returned an unrelated section: %s", text)
	}
}

func TestSecurity_A05_BoostQueryIsReadOnly(t *testing.T) {
	t.Parallel()

	replies := mcpSession(t, "",
		call(1, "database_query", map[string]any{"query": "DELETE FROM posts"}),
		call(2, "database_query", map[string]any{"query": "SELECT 1; DROP TABLE posts"}),
		call(3, "database_query", map[string]any{"query": "WITH x AS (DELETE FROM posts RETURNING *) SELECT * FROM x"}),
		call(4, "database_query", map[string]any{"query": "SELECT COUNT(*) AS n FROM posts"}),
	)

	for id := 1; id <= 3; id++ {
		if text, isErr := toolText(t, replies[id]); !isErr {
			t.Errorf("write query %d accepted: %s", id, text)
		}
	}

	if text, _ := toolText(t, replies[4]); !strings.Contains(text, `"n": 1`) {
		t.Errorf("posts were modified: %s", text)
	}
}
