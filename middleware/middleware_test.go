package middleware

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/AlexGalhardo/vukem/web"
)

func ok(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "ok") }

func withServices(h http.Handler, log *slog.Logger) http.Handler {
	s := &web.Services{Log: log}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(web.WithServices(r.Context(), s)))
	})
}

func do(t *testing.T, h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	t.Helper()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	return rec
}

func get(t *testing.T, target string) *http.Request {
	t.Helper()
	return httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
}

func TestSecurity_A10_RecoverHidesPanic(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))

	h := withServices(Recover()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("secret internal state")
	})), log)

	r := get(t, "/")
	r.Header.Set("Accept", "application/json")
	rec := do(t, h, r)

	if rec.Code != 500 || strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("Recover() response = %d %q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(logs.String(), "secret internal state") || !strings.Contains(logs.String(), "stack=") {
		t.Fatalf("panic not logged with stack: %q", logs.String())
	}
}

func TestRecoverRepanicsAbortHandler(t *testing.T) {
	t.Parallel()

	h := Recover()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) }))

	defer func() {
		if recover() != http.ErrAbortHandler { //nolint:errorlint // identity check
			t.Fatal("ErrAbortHandler was swallowed")
		}
	}()
	do(t, h, get(t, "/"))
}

func TestRequestID(t *testing.T) {
	t.Parallel()

	var seen string
	h := RequestID()(
		http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { seen = web.RequestID(r.Context()) }),
	)

	r := get(t, "/")
	r.Header.Set(RequestIDHeader, "abc-123")
	rec := do(t, h, r)
	if seen != "abc-123" || rec.Header().Get(RequestIDHeader) != "abc-123" {
		t.Fatalf("kept id = %q / %q", seen, rec.Header().Get(RequestIDHeader))
	}

	for _, bad := range []string{"", "has space", "inject\nline", strings.Repeat("a", 200)} {
		r := get(t, "/")
		r.Header.Set(RequestIDHeader, bad)
		rec := do(t, h, r)
		if seen == bad || len(seen) < 20 || rec.Header().Get(RequestIDHeader) != seen {
			t.Fatalf("invalid id %q not replaced (got %q)", bad, seen)
		}
	}
}

func TestLogger(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))

	h := RequestID()(Logger(log)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fail" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if r.URL.Path == "/bad" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		fmt.Fprint(w, "hello")
	})))

	for _, p := range []string{"/ok", "/fail", "/bad"} {
		do(t, h, get(t, p))
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("log lines = %d: %s", len(lines), buf.String())
	}

	want := []struct {
		level  string
		status float64
		bytes  float64
	}{{"INFO", 200, 5}, {"ERROR", 503, 0}, {"WARN", 404, 0}}

	for i, line := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatal(err)
		}
		if m["level"] != want[i].level || m["status"] != want[i].status || m["bytes"] != want[i].bytes ||
			m["request_id"] == "" {
			t.Errorf("line %d = %v", i, m)
		}
	}
}

func TestTimeout(t *testing.T) {
	t.Parallel()

	var deadline time.Time
	h := Timeout(time.Minute)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		deadline, _ = r.Context().Deadline()
	}))
	do(t, h, get(t, "/"))

	if time.Until(deadline) <= 0 || time.Until(deadline) > time.Minute {
		t.Fatalf("deadline = %v", deadline)
	}
}

func TestSecurity_A06_BodyLimit(t *testing.T) {
	t.Parallel()

	h := withServices(BodyLimit(10)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := web.NewCtx(w, r, web.ServicesFrom(r.Context()))
		var v map[string]any
		if err := c.BindJSON(&v); err != nil {
			c.HandleError(err)
			return
		}
		_ = c.NoContent()
	})), slog.New(slog.DiscardHandler))

	tests := []struct {
		name   string
		body   string
		length int64
		want   int
	}{
		{name: "small", body: `{"a":1}`, length: 7, want: 204},
		{name: "declared too large", body: `{"a":"xxxxxxxxxxxxxxxxxx"}`, length: 26, want: 413},
		{name: "chunked too large", body: `{"a":"xxxxxxxxxxxxxxxxxx"}`, length: -1, want: 413},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(tt.body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Accept", "application/json")
			r.ContentLength = tt.length

			if rec := do(t, h, r); rec.Code != tt.want {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}

func TestMethodOverride(t *testing.T) {
	t.Parallel()

	var method string
	h := MethodOverride()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { method = r.Method }))

	tests := []struct {
		method, ct, override, want string
	}{
		{"POST", "application/x-www-form-urlencoded", "DELETE", "DELETE"},
		{"POST", "application/x-www-form-urlencoded", "patch", "PATCH"},
		{"POST", "application/x-www-form-urlencoded", "GET", "POST"},  // only unsafe verbs
		{"POST", "application/json", "DELETE", "POST"},                // forms only
		{"GET", "application/x-www-form-urlencoded", "DELETE", "GET"}, // POST only
		{"POST", "multipart/form-data; boundary=x", "PUT", "PUT"},     // from the query
	}

	for _, tt := range tests {
		target, body := "/", url.Values{"_method": {tt.override}}.Encode()
		if strings.HasPrefix(tt.ct, "multipart/") {
			target, body = "/?"+body, "unread"
		}

		r := httptest.NewRequestWithContext(t.Context(), tt.method, target, strings.NewReader(body))
		r.Header.Set("Content-Type", tt.ct)
		do(t, h, r)

		if method != tt.want {
			t.Errorf("%s %s _method=%s → %s, want %s", tt.method, tt.ct, tt.override, method, tt.want)
		}
	}
}

func TestJSONOnly(t *testing.T) {
	t.Parallel()

	h := withServices(JSONOnly()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, r.Header.Get("Accept"))
	})), slog.New(slog.DiscardHandler))

	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader("a=1"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if rec := do(t, h, r); rec.Code != 415 || rec.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("form body = %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}

	r = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	if rec := do(t, h, r); rec.Code != 200 || rec.Body.String() != "application/json" {
		t.Fatalf("json body = %d %q", rec.Code, rec.Body.String())
	}

	if rec := do(t, h, get(t, "/")); rec.Code != 200 {
		t.Fatalf("GET = %d", rec.Code)
	}
}

// A CORS-simple request (no preflight) can only send text/plain, form or
// multipart bodies: none of them may pass as JSON, whatever parameters
// they carry.
func TestSecurity_A05_JSONOnlyRejectsSimpleContentTypes(t *testing.T) {
	t.Parallel()

	h := withServices(JSONOnly()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "ok")
	})), slog.New(slog.DiscardHandler))

	tests := map[string]int{
		"text/plain; x=+json":                 415,
		"text/plain;charset=application/json": 415,
		"application/jsonp":                   415,
		"application/json":                    200,
		"application/json; charset=utf-8":     200,
		"application/merge-patch+json":        200,
		"APPLICATION/JSON":                    200,
	}

	for ct, want := range tests {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(`{}`))
		r.Header.Set("Content-Type", ct)

		if rec := do(t, h, r); rec.Code != want {
			t.Errorf("JSONOnly(Content-Type %q) = %d, want %d", ct, rec.Code, want)
		}
	}

	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "text/plain; x=+json")

	if web.NewCtx(httptest.NewRecorder(), r, nil).IsJSON() {
		t.Error("IsJSON(text/plain; x=+json) = true, want false")
	}
}

func TestSecurity_A01_CORS(t *testing.T) {
	t.Parallel()

	if _, err := CORS(CORSConfig{AllowedOrigins: []string{"*"}, AllowCredentials: true}); err == nil {
		t.Fatal("CORS(* + credentials) error = nil, want boot error")
	}

	mw, err := CORS(CORSConfig{
		AllowedOrigins:   []string{"https://app.example.com"},
		AllowCredentials: true,
		ExposedHeaders:   []string{"X-Total-Count"},
		MaxAge:           10 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}

	h := mw(http.HandlerFunc(ok))

	r := get(t, "/")
	r.Header.Set("Origin", "https://app.example.com")
	rec := do(t, h, r)
	if rec.Header().Get("Access-Control-Allow-Origin") != "https://app.example.com" ||
		rec.Header().Get("Access-Control-Allow-Credentials") != "true" ||
		rec.Header().Get("Access-Control-Expose-Headers") != "X-Total-Count" || rec.Body.String() != "ok" {
		t.Fatalf("allowed origin headers = %v", rec.Header())
	}

	r = httptest.NewRequestWithContext(t.Context(), http.MethodOptions, "/", http.NoBody)
	r.Header.Set("Origin", "https://app.example.com")
	r.Header.Set("Access-Control-Request-Method", "PUT")
	rec = do(t, h, r)
	if rec.Code != 204 || !strings.Contains(rec.Header().Get("Access-Control-Allow-Methods"), "PUT") ||
		rec.Header().Get("Access-Control-Max-Age") != "600" || rec.Body.Len() != 0 {
		t.Fatalf("preflight = %d %v", rec.Code, rec.Header())
	}

	r = get(t, "/")
	r.Header.Set("Origin", "https://evil.com")
	rec = do(t, h, r)
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("disallowed origin got CORS headers")
	}

	if rec := do(
		t,
		h,
		get(t, "/"),
	); rec.Header().Get("Access-Control-Allow-Origin") != "" ||
		rec.Header().Get("Vary") != "Origin" {
		t.Fatal("same-origin request got CORS headers")
	}

	star, _ := CORS(CORSConfig{AllowedOrigins: []string{"*"}})
	r = get(t, "/")
	r.Header.Set("Origin", "https://any.com")
	if rec := do(t, star(http.HandlerFunc(ok)), r); rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("wildcard origin")
	}
}

func TestSecurity_A01_StaticRejectsTraversal(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	write := func(name, content string) {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("app.css", "body{}")
	write("build/app-3f9a1c2bde.js", "js")
	write(".env", "SECRET=1")
	write("sub/.hidden", "x")
	write("sub/index.html", "dir index")

	secret := filepath.Join(filepath.Dir(dir), "outside.txt")
	_ = os.WriteFile(secret, []byte("outside"), 0o600)
	t.Cleanup(func() { _ = os.Remove(secret) })

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })

	h := Static(root, StaticConfig{})(http.NotFoundHandler())

	tests := []struct {
		path, wantBody string
		wantStatus     int
		wantCache      string
	}{
		{path: "/app.css", wantBody: "body{}", wantStatus: 200, wantCache: "public, max-age=0, must-revalidate"},
		{
			path:       "/build/app-3f9a1c2bde.js",
			wantBody:   "js",
			wantStatus: 200,
			wantCache:  "public, max-age=31536000, immutable",
		},
		{path: "/.env", wantStatus: 404},
		{path: "/sub/.hidden", wantStatus: 404},
		{path: "/sub/", wantStatus: 404},
		{path: "/sub", wantStatus: 404},
		{path: "/../outside.txt", wantStatus: 404},
		{path: "/missing.css", wantStatus: 404},
		{path: "/", wantStatus: 404},
	}

	for _, tt := range tests {
		r := get(t, "http://x"+tt.path)
		r.URL.Path = tt.path // keep raw ".." segments
		rec := do(t, h, r)
		if rec.Code != tt.wantStatus || (tt.wantBody != "" && rec.Body.String() != tt.wantBody) {
			t.Errorf("GET %s = %d %q, want %d", tt.path, rec.Code, rec.Body.String(), tt.wantStatus)
		}
		if tt.wantCache != "" && rec.Header().Get("Cache-Control") != tt.wantCache {
			t.Errorf("GET %s Cache-Control = %q", tt.path, rec.Header().Get("Cache-Control"))
		}
	}

	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/app.css", http.NoBody)
	if rec := do(t, h, r); rec.Code != 404 {
		t.Error("POST served a static file")
	}
}

func TestStaticFSWithPrefix(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{"logo.svg": {Data: []byte("<svg/>")}}
	h := StaticFS(fsys, StaticConfig{Prefix: "/assets"})(http.NotFoundHandler())

	if rec := do(t, h, get(t, "/assets/logo.svg")); rec.Code != 200 || rec.Body.String() != "<svg/>" {
		t.Fatalf("prefixed = %d", rec.Code)
	}
	if rec := do(t, h, get(t, "/logo.svg")); rec.Code != 404 {
		t.Fatalf("unprefixed = %d", rec.Code)
	}
}

func TestSecurity_A02_SecureHeaders(t *testing.T) {
	t.Parallel()

	var nonce string
	h := SecureHeaders(SecureHeadersConfig{})(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		nonce = CSPNonce(r.Context())
	}))

	rec := do(t, h, get(t, "/"))
	want := map[string]string{
		"X-Content-Type-Options":     "nosniff",
		"X-Frame-Options":            "DENY",
		"Referrer-Policy":            "strict-origin-when-cross-origin",
		"Cross-Origin-Opener-Policy": "same-origin",
		"Permissions-Policy":         "camera=(), microphone=(), geolocation=()",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}

	csp := rec.Header().Get("Content-Security-Policy")
	if nonce == "" || !strings.Contains(csp, "'nonce-"+nonce+"'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("CSP = %q nonce = %q", csp, nonce)
	}
	if rec.Header().Get("Strict-Transport-Security") != "" {
		t.Error("HSTS sent over plain HTTP")
	}

	first := nonce
	do(t, h, get(t, "/"))
	if nonce == first {
		t.Error("nonce reused across requests")
	}

	r := get(t, "/")
	r.TLS = &tls.ConnectionState{}
	if got := do(t, h, r).Header().Get("Strict-Transport-Security"); !strings.HasPrefix(got, "max-age=63072000") {
		t.Errorf("HSTS over TLS = %q", got)
	}

	proxied := SecureHeaders(
		SecureHeadersConfig{TrustForwardedProto: true, CSP: "-", FrameOptions: "-", HSTSMaxAge: time.Hour},
	)(
		http.HandlerFunc(ok),
	)
	r = get(t, "/")
	r.Header.Set("X-Forwarded-Proto", "https")
	rec = do(t, proxied, r)
	if rec.Header().Get("Strict-Transport-Security") != "max-age=3600; includeSubDomains" ||
		rec.Header().Get("Content-Security-Policy") != "" || rec.Header().Get("X-Frame-Options") != "" {
		t.Errorf("custom config headers = %v", rec.Header())
	}

	if CSPNonce(context.Background()) != "" {
		t.Error("CSPNonce without middleware")
	}
}

func TestSecurity_A02_ForwardedProtoOnlyFromTrustedProxies(t *testing.T) {
	t.Parallel()

	_, proxies, _ := net.ParseCIDR("10.0.0.0/8")
	h := SecureHeaders(SecureHeadersConfig{TrustForwardedProto: true, TrustedProxies: []*net.IPNet{proxies}})(
		http.HandlerFunc(ok),
	)

	tests := []struct {
		name     string
		peer     string
		wantHSTS bool
	}{
		{name: "trusted proxy", peer: "10.0.0.1:1234", wantHSTS: true},
		{name: "spoofed by a client", peer: "198.51.100.7:1234"},
		{name: "unparsable peer", peer: "garbage"},
	}

	// A second line may come from the client, whichever one Get returns.
	r := get(t, "/")
	r.RemoteAddr = "10.0.0.1:1234"
	r.Header.Add("X-Forwarded-Proto", "https")
	r.Header.Add("X-Forwarded-Proto", "http")

	if got := do(t, h, r).Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("HSTS with two X-Forwarded-Proto lines = %q, want none", got)
	}

	if err := (SecureHeadersConfig{TrustForwardedProto: true}).Validate(); err == nil {
		t.Error("Validate(TrustForwardedProto without TrustedProxies) = nil, want an error")
	}

	if err := (SecureHeadersConfig{TrustForwardedProto: true, TrustedProxies: []*net.IPNet{proxies}}).Validate(); err != nil {
		t.Errorf("Validate(TrustForwardedProto with TrustedProxies) = %v, want nil", err)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := get(t, "/")
			r.RemoteAddr = tt.peer
			r.Header.Set("X-Forwarded-Proto", "https")

			if got := do(t, h, r).Header().Get("Strict-Transport-Security") != ""; got != tt.wantHSTS {
				t.Fatalf("HSTS sent = %v, want %v", got, tt.wantHSTS)
			}
		})
	}
}

func TestGzip(t *testing.T) {
	t.Parallel()

	big := strings.Repeat("hello world ", 200)

	h := Gzip()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/big":
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, big)
		case "/small":
			fmt.Fprint(w, "tiny")
		case "/png":
			w.Header().Set("Content-Type", "image/png")
			fmt.Fprint(w, big)
		case "/stream":
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, "chunk")
			_ = http.NewResponseController(w).Flush()
			fmt.Fprint(w, big)
		case "/empty":
			w.WriteHeader(http.StatusNoContent)
		}
	}))

	req := func(path string, gz bool) *httptest.ResponseRecorder {
		r := get(t, path)
		if gz {
			r.Header.Set("Accept-Encoding", "gzip, br")
		}
		return do(t, h, r)
	}

	rec := req("/big", true)
	if rec.Header().Get("Content-Encoding") != "gzip" ||
		!strings.Contains(rec.Header().Get("Vary"), "Accept-Encoding") {
		t.Fatalf("big not gzipped: %v", rec.Header())
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := io.ReadAll(zr); string(b) != big {
		t.Fatal("gzip roundtrip mismatch")
	}

	for _, tt := range []struct {
		path string
		gz   bool
	}{{"/big", false}, {"/small", true}, {"/png", true}} {
		rec := req(tt.path, tt.gz)
		if rec.Header().Get("Content-Encoding") != "" {
			t.Errorf("%s gz=%v compressed unexpectedly", tt.path, tt.gz)
		}
	}

	rec = req("/stream", true)
	if rec.Header().Get("Content-Encoding") != "" || !rec.Flushed {
		t.Errorf("stream: encoding=%q flushed=%v", rec.Header().Get("Content-Encoding"), rec.Flushed)
	}

	if rec := req("/empty", true); rec.Code != 204 || rec.Header().Get("Content-Encoding") != "" {
		t.Errorf("204 = %d %v", rec.Code, rec.Header())
	}
}

func TestSecurity_A09_LoggerNeverLogsCredentials(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	h := Logger(slog.New(slog.NewTextHandler(&buf, nil)))(http.HandlerFunc(ok))

	r := get(t, "/private?token=abc")
	r.Header.Set("Authorization", "Bearer s3cr3t")
	r.AddCookie(&http.Cookie{Name: "vukem_session", Value: "c00kie"})
	do(t, h, r)

	for _, leaked := range []string{"s3cr3t", "c00kie", "token=abc"} {
		if strings.Contains(buf.String(), leaked) {
			t.Fatalf("log leaked %q: %s", leaked, buf.String())
		}
	}
}

func TestSecureHeadersDevServer(t *testing.T) {
	t.Parallel()

	dev := ""
	h := SecureHeaders(SecureHeadersConfig{DevServer: func() string { return dev }})(http.HandlerFunc(ok))

	csp := func() string {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody))

		return rec.Header().Get("Content-Security-Policy")
	}

	if got := csp(); strings.Contains(got, "localhost") {
		t.Errorf("CSP without dev server = %q", got)
	}

	dev = "http://localhost:5173"
	got := csp()

	for _, want := range []string{
		"script-src 'self' 'nonce-", "'nonce-",
		"script-src 'self' 'nonce-", "http://localhost:5173",
		"connect-src 'self' http://localhost:5173 ws://localhost:5173",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("CSP = %q, want it to contain %q", got, want)
		}
	}

	for _, dir := range []string{"script-src", "style-src", "img-src", "font-src"} {
		if !strings.Contains(strings.Split(strings.SplitAfter(got, dir)[1], ";")[0], "http://localhost:5173") {
			t.Errorf("%s lacks the dev server: %q", dir, got)
		}
	}

	dev = `http://x; script-src *`
	if got := csp(); strings.Contains(got, "script-src *") {
		t.Errorf("unsafe dev origin injected: %q", got)
	}
}

func panickingHandler(http.ResponseWriter, *http.Request) { panic("boom") }

func TestRecoverDebugPageShowsPanicSite(t *testing.T) {
	t.Parallel()

	s := &web.Services{Log: slog.New(slog.DiscardHandler), Debug: true}
	h := Recover()(http.HandlerFunc(panickingHandler))

	r := httptest.NewRequestWithContext(web.WithServices(t.Context(), s), http.MethodGet, "/", http.NoBody)
	r.Header.Set("Accept", "text/html")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusInternalServerError ||
		!strings.Contains(rec.Body.String(), "middleware.panickingHandler") {
		t.Errorf("debug page for a panic = %d, want the panicking function in:\n%.2000s", rec.Code, rec.Body.String())
	}
}
