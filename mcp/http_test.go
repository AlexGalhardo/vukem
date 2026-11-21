package mcp_test

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/mcp"
)

const (
	resourceURL = "https://api.example.com/mcp"
	metaBlock   = `"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}`
)

type verifier struct {
	tokens map[string]*mcp.TokenInfo
	err    error
	seen   []string
}

func (v *verifier) VerifyAccessToken(_ context.Context, token, resource string) (*mcp.TokenInfo, error) {
	v.seen = append(v.seen, resource)

	if v.err != nil {
		return nil, v.err
	}

	if info, ok := v.tokens[token]; ok {
		return info, nil
	}

	return nil, mcp.ErrInvalidToken
}

func openHandler(t *testing.T, s *mcp.Server) *mcp.HTTPHandler {
	t.Helper()

	h, err := s.HTTPHandler(mcp.HTTPConfig{InsecureNoAuth: true, AllowedOrigins: []string{"https://app.example.com"}})
	if err != nil {
		t.Fatal(err)
	}

	return h
}

type req struct {
	method  string
	body    string
	headers map[string]string
}

func do(h http.Handler, r req) *httptest.ResponseRecorder {
	if r.method == "" {
		r.method = http.MethodPost
	}

	hr := httptest.NewRequest(r.method, "/mcp", strings.NewReader(r.body))
	hr.Header.Set("Content-Type", "application/json")
	hr.Header.Set("Accept", "application/json, text/event-stream")

	for k, v := range r.headers {
		if v == "" {
			hr.Header.Del(k)

			continue
		}

		hr.Header.Set(k, v)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, hr)

	return rec
}

func modernHeaders(method, name string) map[string]string {
	h := map[string]string{"MCP-Protocol-Version": "2026-07-28", "Mcp-Method": method}
	if name != "" {
		h["Mcp-Name"] = name
	}

	return h
}

func rpcCode(t *testing.T, rec *httptest.ResponseRecorder) int {
	t.Helper()

	var m struct {
		Error *mcp.Error `json:"error"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("body %q is not JSON: %v", rec.Body.String(), err)
	}

	if m.Error == nil {
		return 0
	}

	return m.Error.Code
}

func TestHTTPCall(t *testing.T) {
	t.Parallel()

	s, _ := newServer(t, mcp.Config{})
	addEcho(s)

	h := openHandler(t, s)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"text":"hi"},` + metaBlock + `}}`
	rec := do(h, req{body: body, headers: modernHeaders("tools/call", "echo")})

	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") ||
		!strings.Contains(rec.Body.String(), `"structuredContent":{"echo":"hi"}`) {
		t.Fatalf("POST tools/call = %d %s", rec.Code, rec.Body.String())
	}

	if rec.Header().Get("Mcp-Session-Id") != "" {
		t.Error("the server must not mint session ids")
	}

	// A base64 sentinel name decodes before the comparison.
	enc := "=?base64?" + base64.StdEncoding.EncodeToString([]byte("echo")) + "?="
	if rec := do(h, req{body: body, headers: modernHeaders("tools/call", enc)}); rec.Code != 200 {
		t.Errorf("base64 Mcp-Name = %d %s", rec.Code, rec.Body.String())
	}

	// Unknown methods are a 404 with a JSON-RPC body; a notification is a 202.
	rec = do(
		h,
		req{
			body:    `{"jsonrpc":"2.0","id":2,"method":"nope",` + metaBlock[:0] + `"params":{` + metaBlock + `}}`,
			headers: modernHeaders("nope", ""),
		},
	)
	if rec.Code != 404 || rpcCode(t, rec) != mcp.CodeMethodNotFound {
		t.Errorf("unknown method = %d %s, want 404 -32601", rec.Code, rec.Body.String())
	}

	rec = do(h, req{body: `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1}}`})
	if rec.Code != 202 || rec.Body.Len() != 0 {
		t.Errorf("notification = %d %q, want an empty 202", rec.Code, rec.Body.String())
	}
}

func TestSecurity_A05_MCPOrigin(t *testing.T) {
	t.Parallel()

	s, _ := newServer(t, mcp.Config{})
	addEcho(s)

	h := openHandler(t, s)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{` + metaBlock + `}}`

	tests := []struct {
		origin string
		want   int
	}{
		{"", 200},
		{"https://app.example.com", 200},
		{"HTTPS://APP.example.com", 200},
		{"https://evil.example.com", 403},
		{"http://app.example.com", 403},
		{"null", 403},
		{"https://app.example.com.evil.com", 403},
		{"not a url", 403},
	}

	for _, tc := range tests {
		hdr := modernHeaders("tools/list", "")
		hdr["Origin"] = tc.origin

		rec := do(h, req{body: body, headers: hdr})
		if rec.Code != tc.want {
			t.Errorf("Origin %q = %d, want %d", tc.origin, rec.Code, tc.want)
		}
	}

	// The check also covers GET, before the 405.
	if rec := do(
		h,
		req{method: http.MethodGet, headers: map[string]string{"Origin": "https://evil.example.com"}},
	); rec.Code != 403 {
		t.Errorf("GET with a bad Origin = %d, want 403", rec.Code)
	}
}

func TestSecurity_A04_MCPHeaderMismatch(t *testing.T) {
	t.Parallel()

	s, _ := newServer(t, mcp.Config{})
	addEcho(s)

	h := openHandler(t, s)
	call := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"text":"x"},` + metaBlock + `}}`
	list := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{` + metaBlock + `}}`

	tests := []struct {
		name    string
		body    string
		headers map[string]string
		status  int
		code    int
	}{
		{
			"missing version header",
			call,
			map[string]string{"Mcp-Method": "tools/call", "Mcp-Name": "echo"},
			400,
			mcp.CodeHeaderMismatch,
		},
		{
			"version header differs from _meta",
			call,
			map[string]string{"MCP-Protocol-Version": "2025-06-18", "Mcp-Method": "tools/call", "Mcp-Name": "echo"},
			400,
			mcp.CodeHeaderMismatch,
		},
		{
			"missing Mcp-Method",
			call,
			map[string]string{"MCP-Protocol-Version": "2026-07-28", "Mcp-Name": "echo"},
			400,
			mcp.CodeHeaderMismatch,
		},
		{"Mcp-Method differs from the body", list, modernHeaders("tools/call", ""), 400, mcp.CodeHeaderMismatch},
		{"missing Mcp-Name", call, modernHeaders("tools/call", ""), 400, mcp.CodeHeaderMismatch},
		{"Mcp-Name differs from the body", call, modernHeaders("tools/call", "other"), 400, mcp.CodeHeaderMismatch},
		{
			"Mcp-Name with a bad base64",
			call,
			modernHeaders("tools/call", "=?base64?%%%?="),
			400,
			mcp.CodeHeaderMismatch,
		},
		{
			"unsupported version",
			strings.ReplaceAll(list, "2026-07-28", "1900-01-01"),
			map[string]string{"MCP-Protocol-Version": "1900-01-01", "Mcp-Method": "tools/list"},
			400,
			mcp.CodeUnsupportedProtocolVersion,
		},
		{
			"no _meta",
			`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
			map[string]string{"MCP-Protocol-Version": "2026-07-28", "Mcp-Method": "tools/list"},
			400,
			mcp.CodeInvalidParams,
		},
		{"valid", list, modernHeaders("tools/list", ""), 200, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := do(h, req{body: tc.body, headers: tc.headers})
			if rec.Code != tc.status || rpcCode(t, rec) != tc.code {
				t.Errorf("%s = %d %s, want %d and code %d", tc.name, rec.Code, rec.Body.String(), tc.status, tc.code)
			}
		})
	}
}

func TestSecurity_A04_MCPMalformedAndOversized(t *testing.T) {
	t.Parallel()

	s, _ := newServer(t, mcp.Config{MaxMessageBytes: 1024})
	addEcho(s)

	h := openHandler(t, s)

	tests := []struct {
		name    string
		r       req
		status  int
		code    int
		noCheck bool
	}{
		{"not JSON", req{body: "{nope"}, 400, mcp.CodeParse, false},
		{"empty body", req{body: ""}, 400, mcp.CodeParse, false},
		{"batch", req{body: `[{"jsonrpc":"2.0","id":1,"method":"tools/list"}]`}, 400, mcp.CodeInvalidRequest, false},
		{"wrong jsonrpc", req{body: `{"jsonrpc":"1.0","id":1,"method":"x"}`}, 400, mcp.CodeInvalidRequest, false},
		{"no method", req{body: `{"jsonrpc":"2.0","id":1}`}, 400, mcp.CodeInvalidRequest, false},
		{"null id", req{body: `{"jsonrpc":"2.0","id":null,"method":"tools/list"}`}, 400, mcp.CodeInvalidRequest, false},
		{"object id", req{body: `{"jsonrpc":"2.0","id":{},"method":"tools/list"}`}, 400, mcp.CodeInvalidRequest, false},
		{"client response", req{body: `{"jsonrpc":"2.0","id":1,"result":{}}`}, 400, mcp.CodeInvalidRequest, false},
		{
			"oversized",
			req{
				body: `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"x":"` + strings.Repeat(
					"a",
					2000,
				) + `"}}`,
			},
			413,
			mcp.CodeInvalidRequest,
			false,
		},
		{
			"wrong content type",
			req{body: "{}", headers: map[string]string{"Content-Type": "text/plain"}},
			415,
			mcp.CodeInvalidRequest,
			false,
		},
		{
			"accept without json",
			req{body: "{}", headers: map[string]string{"Accept": "text/html"}},
			406,
			mcp.CodeInvalidRequest,
			false,
		},
		{
			"deep nesting",
			req{body: `{"jsonrpc":"2.0","id":1,"method":"x","params":` + strings.Repeat("[", 20000) + "}"},
			413,
			mcp.CodeInvalidRequest,
			false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := do(h, tc.r)
			if rec.Code != tc.status || rpcCode(t, rec) != tc.code {
				t.Errorf("%s = %d %s, want %d and code %d", tc.name, rec.Code, rec.Body.String(), tc.status, tc.code)
			}
		})
	}
}

func TestHTTPNoSessionsNoGetStream(t *testing.T) {
	t.Parallel()

	s, _ := newServer(t, mcp.Config{})
	h := openHandler(t, s)

	for _, m := range []string{http.MethodGet, http.MethodDelete, http.MethodPut} {
		rec := do(h, req{method: m})
		if rec.Code != 405 || rec.Header().Get("Allow") != "POST" {
			t.Errorf("%s = %d Allow=%q, want 405 POST", m, rec.Code, rec.Header().Get("Allow"))
		}
	}

	// A session id and Last-Event-ID from an older client are ignored.
	rec := do(h, req{
		body: `{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{` + metaBlock + `}}`,
		headers: map[string]string{
			"MCP-Protocol-Version": "2026-07-28",
			"Mcp-Method":           "server/discover",
			"Mcp-Session-Id":       "abc",
			"Last-Event-ID":        "9",
		},
	})
	if rec.Code != 200 || rec.Header().Get("Mcp-Session-Id") != "" {
		t.Errorf("server/discover with a session id = %d %v", rec.Code, rec.Header())
	}
}

func TestHTTPLegacyClient(t *testing.T) {
	t.Parallel()

	s, _ := newServer(t, mcp.Config{})
	addEcho(s)

	h := openHandler(t, s)

	rec := do(
		h,
		req{
			body: `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{}}}`,
		},
	)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"protocolVersion":"2025-06-18"`) ||
		rec.Header().Get("Mcp-Session-Id") != "" {
		t.Fatalf("initialize = %d %s", rec.Code, rec.Body.String())
	}

	call := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo","arguments":{"text":"hi"}}}`

	for _, v := range []string{"2025-06-18", ""} { // the second is a 2025-03-26 client: no header
		rec = do(h, req{body: call, headers: map[string]string{"MCP-Protocol-Version": v}})
		if rec.Code != 200 || strings.Contains(rec.Body.String(), "resultType") ||
			!strings.Contains(rec.Body.String(), `"echo":"hi"`) {
			t.Errorf("legacy tools/call with version %q = %d %s", v, rec.Code, rec.Body.String())
		}
	}
}

func TestHTTPStreamsProgress(t *testing.T) {
	t.Parallel()

	s, _ := newServer(t, mcp.Config{})
	mcp.AddTool(s, mcp.Tool{Name: "work"}, func(ctx context.Context, _ echoIn) (echoOut, error) {
		mcp.Progress(ctx, 1, 2, "half")

		return echoOut{Echo: "ok"}, nil
	})

	h := openHandler(t, s)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"work","arguments":{"text":"x"},"_meta":{"progressToken":7,` + metaBlock[len(`"_meta":{`):] + `}}`
	rec := do(h, req{body: body, headers: modernHeaders("tools/call", "work")})

	if ct := rec.Header().
		Get("Content-Type"); ct != "text/event-stream" ||
		rec.Header().Get("X-Accel-Buffering") != "no" {
		t.Fatalf("response = %v, want an event stream", rec.Header())
	}

	var events []string

	for line := range strings.SplitSeq(rec.Body.String(), "\n") {
		if data, ok := strings.CutPrefix(line, "data: "); ok {
			events = append(events, data)
		}
	}

	if len(events) != 2 || !strings.Contains(events[0], `"notifications/progress"`) ||
		!strings.Contains(events[0], `"progressToken":7`) ||
		!strings.Contains(events[1], `"id":1`) ||
		!strings.Contains(events[1], `"echo":"ok"`) {
		t.Errorf("events = %q, want a progress notification then the response", events)
	}
}

func TestHTTPSubscriptionsListen(t *testing.T) {
	t.Parallel()

	s, _ := newServer(t, mcp.Config{KeepAlive: 20 * time.Millisecond})
	addEcho(s)

	srv := httptest.NewServer(openHandler(t, s))
	t.Cleanup(srv.Close)

	body := `{"jsonrpc":"2.0","id":5,"method":"subscriptions/listen","params":{"notifications":{"toolsListChanged":true,"promptsListChanged":true},` + metaBlock + `}}`

	r, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	r.Header.Set("MCP-Protocol-Version", "2026-07-28")
	r.Header.Set("Mcp-Method", "subscriptions/listen")

	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { resp.Body.Close() })

	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("Content-Type = %q", resp.Header.Get("Content-Type"))
	}

	lines := make(chan string, 32)

	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}

		close(lines)
	}()

	next := func(substr string) {
		t.Helper()

		timeout := time.After(5 * time.Second)

		for {
			select {
			case l, ok := <-lines:
				if !ok {
					t.Fatalf("stream ended before %q", substr)
				}

				if strings.Contains(l, substr) {
					return
				}
			case <-timeout:
				t.Fatalf("no %q on the stream", substr)
			}
		}
	}

	next("notifications/subscriptions/acknowledged")
	s.NotifyToolsChanged()
	next(`"method":"notifications/tools/list_changed","params":{"_meta":{"io.modelcontextprotocol/subscriptionId":5}}`)

	// Idle keep-alive comments flow; a prompts change is not announced
	// because the server has no prompts.
	s.NotifyPromptsChanged()
	s.Close()
	next(`"id":5`) // the graceful completion result
}

func TestSecurity_A01_MCPAuth(t *testing.T) {
	t.Parallel()

	s, logs := newServer(t, mcp.Config{})
	addEcho(s)
	mcp.AddTool(
		s,
		mcp.Tool{Name: "admin", Scopes: []string{"admin"}},
		func(ctx context.Context, _ echoIn) (echoOut, error) {
			info, ok := mcp.TokenInfoFrom(ctx)
			if !ok {
				return echoOut{}, errors.New("no token info")
			}

			return echoOut{Echo: info.Subject}, nil
		},
	)

	v := &verifier{tokens: map[string]*mcp.TokenInfo{
		"good": {
			Subject:   "u1",
			Scopes:    []string{"read", "admin"},
			Resource:  resourceURL,
			ExpiresAt: time.Now().Add(time.Hour),
		},
		"reader": {Subject: "u2", Scopes: []string{"read"}, Resource: resourceURL},
		"other":  {Subject: "u3", Scopes: []string{"admin"}, Resource: "https://other.example.com/mcp"},
		"expired": {
			Subject:   "u4",
			Scopes:    []string{"admin"},
			Resource:  resourceURL,
			ExpiresAt: time.Now().Add(-time.Minute),
		},
		"nores": {Subject: "u5", Scopes: []string{"admin"}},
	}}

	h, err := s.HTTPHandler(mcp.HTTPConfig{
		Verifier: v, Resource: resourceURL, AuthorizationServers: []string{"https://auth.example.com"},
		ScopesSupported: []string{"read", "admin"},
	})
	if err != nil {
		t.Fatal(err)
	}

	list := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{` + metaBlock + `}}`
	call := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"admin","arguments":{"text":"x"},` + metaBlock + `}}`

	withAuth := func(method, token string) map[string]string {
		hdr := modernHeaders(method, "")
		if method == "tools/call" {
			hdr["Mcp-Name"] = "admin"
		}

		if token != "" {
			hdr["Authorization"] = "Bearer " + token
		}

		return hdr
	}

	meta := `resource_metadata="https://api.example.com/.well-known/oauth-protected-resource/mcp"`

	tests := []struct {
		name      string
		body      string
		headers   map[string]string
		status    int
		challenge string
	}{
		{"no token", list, withAuth("tools/list", ""), 401, "Bearer scope=\"read admin\", " + meta},
		{
			"unknown token",
			list,
			withAuth("tools/list", "bad"),
			401,
			"Bearer error=\"invalid_token\", scope=\"read admin\", " + meta,
		},
		{
			"wrong audience",
			list,
			withAuth("tools/list", "other"),
			401,
			"Bearer error=\"invalid_token\", scope=\"read admin\", " + meta,
		},
		{
			"no audience at all",
			list,
			withAuth("tools/list", "nores"),
			401,
			"Bearer error=\"invalid_token\", scope=\"read admin\", " + meta,
		},
		{
			"expired",
			list,
			withAuth("tools/list", "expired"),
			401,
			"Bearer error=\"invalid_token\", scope=\"read admin\", " + meta,
		},
		{
			"basic scheme",
			list,
			map[string]string{
				"MCP-Protocol-Version": "2026-07-28",
				"Mcp-Method":           "tools/list",
				"Authorization":        "Basic Zm9v",
			},
			401,
			"Bearer error=\"invalid_token\", scope=\"read admin\", " + meta,
		},
		{
			"insufficient scope",
			call,
			withAuth("tools/call", "reader"),
			403,
			"Bearer error=\"insufficient_scope\", scope=\"admin\", " + meta,
		},
		{"good", call, withAuth("tools/call", "good"), 200, ""},
	}

	for _, tc := range tests {
		rec := do(h, req{body: tc.body, headers: tc.headers})
		if rec.Code != tc.status {
			t.Errorf("%s = %d %s, want %d", tc.name, rec.Code, rec.Body.String(), tc.status)
		}

		if got := rec.Header().Get("WWW-Authenticate"); got != tc.challenge {
			t.Errorf("%s WWW-Authenticate = %q, want %q", tc.name, got, tc.challenge)
		}
	}

	if got := do(
		h,
		req{body: call, headers: withAuth("tools/call", "good")},
	).Body.String(); !strings.Contains(
		got,
		`\"echo\":\"u1\"`,
	) {
		t.Errorf("TokenInfo did not reach the tool: %s", got)
	}

	for _, r := range v.seen {
		if r != resourceURL {
			t.Errorf("verifier saw resource %q, want %q", r, resourceURL)
		}
	}

	// A reader sees only the tools its scopes allow.
	var tools struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}

	var out struct {
		Result json.RawMessage `json:"result"`
	}

	_ = json.Unmarshal(do(h, req{body: list, headers: withAuth("tools/list", "reader")}).Body.Bytes(), &out)
	_ = json.Unmarshal(out.Result, &tools)

	if len(tools.Tools) != 1 || tools.Tools[0].Name != "echo" {
		t.Errorf("tools for a reader = %+v, want only echo", tools.Tools)
	}

	// A token in the query string is refused and never reaches the verifier.
	n := len(v.seen)
	hr := httptest.NewRequest(http.MethodPost, "/mcp?access_token=good", strings.NewReader(list))
	hr.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, hr)

	if rec.Code != 400 || len(v.seen) != n {
		t.Errorf("token in the query = %d (verifier calls %d to %d), want 400 untouched", rec.Code, n, len(v.seen))
	}

	// A verifier outage fails closed without detail; the token is never logged.
	v.err = errors.New("database down at 10.0.0.5")

	rec = do(h, req{body: list, headers: withAuth("tools/list", "good")})
	if rec.Code != 503 || strings.Contains(rec.Body.String(), "10.0.0.5") {
		t.Errorf("verifier outage = %d %s, want a bare 503", rec.Code, rec.Body.String())
	}

	if strings.Contains(logs.String(), "good") && strings.Contains(logs.String(), "Bearer") {
		t.Errorf("log leaks the credential: %s", logs.String())
	}
}

func TestProtectedResourceMetadata(t *testing.T) {
	t.Parallel()

	s, _ := newServer(t, mcp.Config{})
	h, err := s.HTTPHandler(mcp.HTTPConfig{
		Verifier: &verifier{}, Resource: resourceURL, AuthorizationServers: []string{"https://auth.example.com"},
		ScopesSupported: []string{"read"}, ResourceName: "Billing",
	})
	if err != nil {
		t.Fatal(err)
	}

	if h.MetadataPath() != "/.well-known/oauth-protected-resource/mcp" {
		t.Errorf("MetadataPath() = %q", h.MetadataPath())
	}

	rec := httptest.NewRecorder()
	h.MetadataHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, h.MetadataPath(), http.NoBody))

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}

	want := map[string]any{
		"resource": resourceURL, "authorization_servers": []any{"https://auth.example.com"},
		"bearer_methods_supported": []any{"header"}, "scopes_supported": []any{"read"}, "resource_name": "Billing",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("metadata (-want +got):\n%s", diff)
	}

	rec = httptest.NewRecorder()
	h.MetadataHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, h.MetadataPath(), http.NoBody))

	if rec.Code != 405 {
		t.Errorf("POST metadata = %d, want 405", rec.Code)
	}
}

func TestSecurity_A05_MCPWeakConfigRefused(t *testing.T) {
	t.Parallel()

	s, _ := newServer(t, mcp.Config{})

	tests := map[string]mcp.HTTPConfig{
		"no verifier, no opt-out":   {},
		"verifier without resource": {Verifier: &verifier{}, AuthorizationServers: []string{"https://a"}},
		"resource with a fragment": {
			Verifier:             &verifier{},
			Resource:             "https://a/mcp#x",
			AuthorizationServers: []string{"https://a"},
		},
		"resource not absolute": {
			Verifier:             &verifier{},
			Resource:             "/mcp",
			AuthorizationServers: []string{"https://a"},
		},
		"verifier without AS":        {Verifier: &verifier{}, Resource: resourceURL},
		"invalid allowed origin":     {InsecureNoAuth: true, AllowedOrigins: []string{"app.example.com"}},
		"null as an allowed origin":  {InsecureNoAuth: true, AllowedOrigins: []string{"null"}},
		"allowed origin with a path": {InsecureNoAuth: true, AllowedOrigins: []string{"https://a.example.com/x"}},
	}

	for name, cfg := range tests {
		if _, err := s.HTTPHandler(cfg); err == nil {
			t.Errorf("%s: HTTPHandler succeeded, want an error", name)
		}
	}
}

func TestSecurity_A01_MCPScopedToolsShut(t *testing.T) {
	t.Parallel()

	s, _ := newServer(t, mcp.Config{})
	mcp.AddTool(s, mcp.Tool{Name: "admin", Scopes: []string{"admin"}}, func(context.Context, echoIn) (echoOut, error) {
		return echoOut{Echo: "ran"}, nil
	})

	h := openHandler(t, s)
	call := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"admin","arguments":{"text":"x"},` + metaBlock + `}}`

	rec := do(h, req{body: call, headers: modernHeaders("tools/call", "admin")})
	if strings.Contains(rec.Body.String(), "ran") || rec.Code == 200 {
		t.Errorf("scoped tool without authorization = %d %s, want refused", rec.Code, rec.Body.String())
	}
}

func FuzzHTTPHandler(f *testing.F) {
	s := mcp.NewServer(mcp.Config{Name: "fuzz", Version: "1"})
	mcp.AddTool(
		s,
		mcp.Tool{Name: "echo"},
		func(_ context.Context, in echoIn) (echoOut, error) { return echoOut{Echo: in.Text}, nil },
	)

	h, err := s.HTTPHandler(mcp.HTTPConfig{InsecureNoAuth: true})
	if err != nil {
		f.Fatal(err)
	}

	f.Add(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"text":"x"}}}`,
		"tools/call",
		"echo",
	)
	f.Add(`{"jsonrpc":"2.0","id":"a","method":"initialize","params":{"protocolVersion":"2025-06-18"}}`, "", "")
	f.Add(`[`, "x", "y")
	f.Add(
		`{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"a"}}`,
		"resources/read",
		"=?base64?YQ==?=",
	)

	f.Fuzz(func(t *testing.T, body, method, name string) {
		hdr := map[string]string{"MCP-Protocol-Version": "2026-07-28"}

		if strings.ContainsAny(method, "\r\n\x00") || strings.ContainsAny(name, "\r\n\x00") {
			t.Skip()
		}

		hdr["Mcp-Method"], hdr["Mcp-Name"] = method, name

		rec := do(h, req{body: body, headers: hdr})

		switch rec.Code {
		case 200, 202, 400, 404, 413, 415, 406:
		default:
			t.Fatalf("status %d for %q", rec.Code, body)
		}

		if rec.Code != 202 {
			b, _ := io.ReadAll(rec.Body)
			if !json.Valid(b) {
				t.Fatalf("body %q is not JSON for %q", b, body)
			}
		}
	})
}
