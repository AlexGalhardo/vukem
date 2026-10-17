package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/errs"
)

func newCtx(t *testing.T, r *http.Request, s *Services) (*Ctx, *httptest.ResponseRecorder) {
	t.Helper()

	if s == nil {
		s = &Services{Log: slog.New(slog.DiscardHandler)}
	}

	rec := httptest.NewRecorder()

	return NewCtx(rec, r, s), rec
}

func TestRequestHelpers(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/posts/42?page=3&draft=true&bad=x", http.NoBody)
	r.SetPathValue("id", "42")
	r.SetPathValue("slug", "abc")
	r.Header.Set("X-Test", "yes")
	r.AddCookie(&http.Cookie{Name: "c", Value: "v"})

	c, _ := newCtx(t, r, nil)

	if c.Param("id") != "42" || c.Query("page") != "3" || c.Header("X-Test") != "yes" || c.Cookie("c") != "v" {
		t.Fatal("basic accessors wrong")
	}
	if c.Cookie("missing") != "" || c.Method() != "GET" || c.Path() != "/posts/42" {
		t.Fatal("cookie/method/path wrong")
	}
	if n, err := c.ParamInt("id"); err != nil || n != 42 {
		t.Fatalf("ParamInt(id) = %d, %v", n, err)
	}
	if _, err := c.ParamInt("slug"); !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("ParamInt(slug) error = %v, want ErrNotFound", err)
	}
	if c.QueryInt("page", 1) != 3 || c.QueryInt("bad", 7) != 7 || !c.QueryBool("draft") {
		t.Fatal("query helpers wrong")
	}
	if c.Context() != r.Context() || c.Request() != r || c.Services() == nil || c.Log() == nil {
		t.Fatal("ctx accessors wrong")
	}

	c.SetRouteName("posts.show")
	if c.RouteName() != "posts.show" {
		t.Fatal("route name")
	}
}

func TestContentNegotiation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		accept, ct, hx string
		wantJSON       bool
		isJSON         bool
		isHTMX         bool
	}{
		{accept: "application/json", wantJSON: true},
		{accept: "application/problem+json", wantJSON: true},
		{accept: "text/html,application/json", wantJSON: false},
		{ct: "application/json", wantJSON: true, isJSON: true},
		{accept: "text/html", ct: "application/json", isJSON: true},
		{ct: "application/vnd.api+json", wantJSON: true, isJSON: true},
		{hx: "true", isHTMX: true},
	}

	for _, tt := range tests {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", http.NoBody)
		r.Header.Set("Accept", tt.accept)
		r.Header.Set("Content-Type", tt.ct)
		r.Header.Set("HX-Request", tt.hx)

		c, _ := newCtx(t, r, nil)
		if c.WantsJSON() != tt.wantJSON || c.IsJSON() != tt.isJSON || c.IsHTMX() != tt.isHTMX {
			t.Errorf(
				"accept=%q ct=%q: WantsJSON=%v IsJSON=%v IsHTMX=%v",
				tt.accept,
				tt.ct,
				c.WantsJSON(),
				c.IsJSON(),
				c.IsHTMX(),
			)
		}
	}
}

func TestSecurity_A01_IPIgnoresSpoofedForwardedFor(t *testing.T) {
	t.Parallel()

	_, proxies, _ := net.ParseCIDR("10.0.0.0/8")

	tests := []struct {
		name, remote, xff string
		trusted           bool
		want              string
	}{
		{name: "direct", remote: "1.2.3.4:5000", xff: "9.9.9.9", want: "1.2.3.4"},
		{name: "trusted proxy", remote: "10.0.0.1:5000", xff: "9.9.9.9", trusted: true, want: "9.9.9.9"},
		{
			name:    "proxy chain",
			remote:  "10.0.0.1:5000",
			xff:     "6.6.6.6, 9.9.9.9, 10.0.0.2",
			trusted: true,
			want:    "9.9.9.9",
		},
		{name: "no header", remote: "10.0.0.1:5000", trusted: true, want: "10.0.0.1"},
		{name: "all trusted", remote: "10.0.0.1:1", xff: "10.0.0.5", trusted: true, want: "10.0.0.5"},
		{name: "no port", remote: "1.2.3.4", want: "1.2.3.4"},
		// A proxy that appends its own header line (HAProxy) must not let
		// the client's first line win.
		{name: "two header lines", remote: "10.0.0.1:1", xff: "6.6.6.6\n9.9.9.9", trusted: true, want: "9.9.9.9"},
		{name: "not an ip", remote: "10.0.0.1:1", xff: "6.6.6.6, evil", trusted: true, want: "10.0.0.1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
			r.RemoteAddr = tt.remote
			if tt.xff != "" {
				for line := range strings.SplitSeq(tt.xff, "\n") {
					r.Header.Add("X-Forwarded-For", line)
				}
			}

			s := &Services{}
			if tt.trusted {
				s.TrustedProxies = []*net.IPNet{proxies}
			}

			c, _ := newCtx(t, r, s)
			if got := c.IP(); got != tt.want {
				t.Fatalf("IP() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSecurity_A01_ClientIPHeaderOnlyFromTrustedProxies(t *testing.T) {
	t.Parallel()

	_, proxies, _ := net.ParseCIDR("10.0.0.0/8")

	tests := []struct {
		name, remote string
		header       []string // X-Real-IP lines
		xff          string
		want         string
	}{
		{name: "trusted proxy", remote: "10.0.0.1:1", header: []string{"9.9.9.9"}, want: "9.9.9.9"},
		{name: "ipv6 client", remote: "10.0.0.1:1", header: []string{" 2001:db8::7 "}, want: "2001:db8::7"},
		// A client that reaches the app directly cannot pick its own IP.
		{
			name:   "spoofed by untrusted peer",
			remote: "198.51.100.7:1",
			header: []string{"9.9.9.9"},
			want:   "198.51.100.7",
		},
		{name: "missing header", remote: "10.0.0.1:1", want: "10.0.0.1"},
		{name: "not an ip", remote: "10.0.0.1:1", header: []string{"evil"}, want: "10.0.0.1"},
		{name: "list is refused", remote: "10.0.0.1:1", header: []string{"6.6.6.6, 9.9.9.9"}, want: "10.0.0.1"},
		{name: "two lines are refused", remote: "10.0.0.1:1", header: []string{"6.6.6.6", "9.9.9.9"}, want: "10.0.0.1"},
		// The named header replaces X-Forwarded-For entirely.
		{name: "forwarded-for ignored", remote: "10.0.0.1:1", xff: "9.9.9.9", want: "10.0.0.1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
			r.RemoteAddr = tt.remote
			for _, v := range tt.header {
				r.Header.Add("X-Real-Ip", v)
			}

			if tt.xff != "" {
				r.Header.Set("X-Forwarded-For", tt.xff)
			}

			s := &Services{TrustedProxies: []*net.IPNet{proxies}, ClientIPHeader: "X-Real-IP"}

			c, _ := newCtx(t, r, s)
			if got := c.IP(); got != tt.want {
				t.Fatalf("IP() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResponses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		do         func(c *Ctx) error
		wantStatus int
		wantCT     string
		wantBody   string
		wantHeader [2]string
	}{
		{
			name:       "string",
			do:         func(c *Ctx) error { return c.String(201, "hi") },
			wantStatus: 201,
			wantCT:     "text/plain; charset=utf-8",
			wantBody:   "hi",
		},
		{
			name:       "html",
			do:         func(c *Ctx) error { return c.HTML(200, "<b>x</b>") },
			wantStatus: 200,
			wantCT:     "text/html; charset=utf-8",
			wantBody:   "<b>x</b>",
		},
		{
			name:       "json",
			do:         func(c *Ctx) error { return c.JSON(200, Map{"a": 1}) },
			wantStatus: 200,
			wantCT:     "application/json",
			wantBody:   `{"a":1}`,
		},
		{
			name: "created", do: func(c *Ctx) error { return c.Created("/posts/1", Map{"id": 1}) },
			wantStatus: 201, wantBody: `{"id":1}`, wantHeader: [2]string{"Location", "/posts/1"},
		},
		{name: "accepted", do: func(c *Ctx) error { return c.Accepted(Map{}) }, wantStatus: 202, wantBody: `{}`},
		{name: "no content", do: func(c *Ctx) error { return c.NoContent() }, wantStatus: 204},
		{
			name:       "status chain",
			do:         func(c *Ctx) error { return c.Status(418).JSON(200, 1) },
			wantStatus: 418,
			wantBody:   "1",
		},
		{
			name: "blob", do: func(c *Ctx) error { return c.Blob(200, "image/png", []byte{1}) },
			wantStatus: 200, wantCT: "image/png", wantBody: "\x01",
		},
		{
			name: "stream", do: func(c *Ctx) error { return c.Stream(200, "text/csv", strings.NewReader("a,b")) },
			wantStatus: 200, wantCT: "text/csv", wantBody: "a,b",
		},
		{
			name: "problem", do: func(c *Ctx) error { return c.Problem(409, "Conflict", "taken") },
			wantStatus: 409, wantCT: "application/problem+json",
			wantBody: `{"type":"about:blank","title":"Conflict","status":409,"detail":"taken","instance":"/x"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, rec := newCtx(t, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", http.NoBody), nil)
			if err := tt.do(c); err != nil {
				t.Fatal(err)
			}

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if tt.wantCT != "" && rec.Header().Get("Content-Type") != tt.wantCT {
				t.Errorf("Content-Type = %q, want %q", rec.Header().Get("Content-Type"), tt.wantCT)
			}
			if got := strings.TrimSpace(rec.Body.String()); got != tt.wantBody {
				t.Errorf("body = %q, want %q", got, tt.wantBody)
			}
			if tt.wantHeader[0] != "" && rec.Header().Get(tt.wantHeader[0]) != tt.wantHeader[1] {
				t.Errorf("header %s = %q", tt.wantHeader[0], rec.Header().Get(tt.wantHeader[0]))
			}
			if !c.Writer().Written() || c.Writer().Status() != tt.wantStatus {
				t.Errorf("writer state = %v %d", c.Writer().Written(), c.Writer().Status())
			}
		})
	}
}

func TestJSONEncodeError(t *testing.T) {
	t.Parallel()

	c, rec := newCtx(t, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody), nil)
	if err := c.JSON(200, func() {}); err == nil {
		t.Fatal("JSON(func) error = nil")
	}
	if c.Writer().Written() || rec.Body.Len() != 0 {
		t.Fatal("failed JSON wrote a response")
	}
}

type fakeURLs map[string]string

func (f fakeURLs) URL(name string, _ ...any) (string, error) {
	if u, ok := f[name]; ok {
		return u, nil
	}
	return "", errors.New("unknown route " + name)
}

func TestRedirects(t *testing.T) {
	t.Parallel()

	s := &Services{URLs: fakeURLs{"home": "/"}, Log: slog.New(slog.DiscardHandler)}

	tests := []struct {
		name, method, referer string
		htmx                  bool
		do                    func(c *Ctx) error
		wantStatus            int
		wantLocation          string
		wantHX                string
	}{
		{
			name:         "get 302",
			method:       "GET",
			do:           func(c *Ctx) error { return c.Redirect("/a") },
			wantStatus:   302,
			wantLocation: "/a",
		},
		{
			name:         "post 303",
			method:       "POST",
			do:           func(c *Ctx) error { return c.Redirect("/a") },
			wantStatus:   303,
			wantLocation: "/a",
		},
		{
			name:         "route",
			method:       "POST",
			do:           func(c *Ctx) error { return c.RedirectRoute("home") },
			wantStatus:   303,
			wantLocation: "/",
		},
		{
			name:       "htmx",
			method:     "POST",
			htmx:       true,
			do:         func(c *Ctx) error { return c.Redirect("/a") },
			wantStatus: 200,
			wantHX:     "/a",
		},
		{
			name:         "back same origin",
			method:       "POST",
			referer:      "http://example.com/form?x=1",
			do:           func(c *Ctx) error { return c.Back("/") },
			wantStatus:   303,
			wantLocation: "/form?x=1",
		},
		{
			name:         "back relative",
			method:       "POST",
			referer:      "/form",
			do:           func(c *Ctx) error { return c.Back("/") },
			wantStatus:   303,
			wantLocation: "/form",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequestWithContext(t.Context(), tt.method, "http://example.com/x", http.NoBody)
			if tt.referer != "" {
				r.Header.Set("Referer", tt.referer)
			}
			if tt.htmx {
				r.Header.Set("HX-Request", "true")
			}

			c, rec := newCtx(t, r, s)
			if err := tt.do(c); err != nil {
				t.Fatal(err)
			}

			if rec.Code != tt.wantStatus || rec.Header().Get("Location") != tt.wantLocation ||
				rec.Header().Get("HX-Location") != tt.wantHX {
				t.Fatalf(
					"got %d Location=%q HX-Location=%q",
					rec.Code,
					rec.Header().Get("Location"),
					rec.Header().Get("HX-Location"),
				)
			}
		})
	}

	c, _ := newCtx(t, httptest.NewRequestWithContext(t.Context(), "GET", "/", http.NoBody), s)
	if err := c.RedirectRoute("nope"); err == nil {
		t.Fatal("RedirectRoute(unknown) error = nil")
	}
	if c.MustURL("nope") != "#" || c.MustURL("home") != "/" {
		t.Fatal("MustURL wrong")
	}

	bare, _ := newCtx(t, httptest.NewRequestWithContext(t.Context(), "GET", "/", http.NoBody), nil)
	if _, err := bare.URL("home"); err == nil {
		t.Fatal("URL without generator error = nil")
	}
}

func TestSecurity_A01_SafeRedirectBlocksOpenRedirects(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.com/", http.NoBody)

	tests := map[string]string{
		"":                             "/fallback",
		"/ok":                          "/ok",
		"http://example.com/ok?a=1":    "/ok?a=1",
		"https://evil.com/x":           "/fallback",
		"//evil.com/x":                 "/fallback",
		`/\evil.com`:                   "/fallback",
		"javascript:alert(1)":          "/fallback",
		"relative/path":                "/fallback",
		"http://example.com.evil.com/": "/fallback",
		"%zz":                          "/fallback",
		// A same-host absolute URL must not become a scheme-relative path.
		"http://example.com//evil.com/x":   "/fallback",
		`http://example.com/\evil.com`:     "/fallback",
		"https://example.com/%2F/evil.com": "/%2F/evil.com",
		// http.Redirect cleans relative targets: dot segments must not
		// turn into "/\evil.com" after the check.
		`/./\evil.com`:    "/fallback",
		`/../\evil.com`:   "/fallback",
		`/x/../\evil`:     "/fallback",
		"/a/..//evil.com": "/a/..//evil.com",
	}

	for target, want := range tests {
		got := SafeRedirect(r, target, "/fallback")
		if got != want {
			t.Errorf("SafeRedirect(%q) = %q, want %q", target, got, want)
		}

		w := httptest.NewRecorder()
		http.Redirect(w, r, got, http.StatusFound)

		if loc := w.Header().Get("Location"); !isLocalPath(loc) {
			t.Errorf("Redirect(SafeRedirect(%q)) Location = %q, leaves the origin", target, loc)
		}
	}
}

func TestFileAndDownload(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		"a.txt":     {Data: []byte("hello"), ModTime: time.Unix(1, 0)},
		"dir/b.css": {Data: []byte("body{}")},
	}

	c, rec := newCtx(t, httptest.NewRequestWithContext(t.Context(), "GET", "/", http.NoBody), nil)
	if err := c.Download(fsys, "a.txt", "report.txt"); err != nil {
		t.Fatal(err)
	}
	if rec.Body.String() != "hello" ||
		!strings.Contains(rec.Header().Get("Content-Disposition"), `filename=report.txt`) {
		t.Fatalf("Download() = %q %q", rec.Body.String(), rec.Header().Get("Content-Disposition"))
	}

	c, _ = newCtx(t, httptest.NewRequestWithContext(t.Context(), "GET", "/", http.NoBody), nil)
	if err := c.File(fsys, "missing.txt"); !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("File(missing) = %v", err)
	}

	c, _ = newCtx(t, httptest.NewRequestWithContext(t.Context(), "GET", "/", http.NoBody), nil)
	if err := c.File(fsys, "dir"); !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("File(dir) = %v", err)
	}
}

func TestInputJSONFormQuery(t *testing.T) {
	t.Parallel()

	t.Run("json merges over query", func(t *testing.T) {
		t.Parallel()

		r := httptest.NewRequestWithContext(
			t.Context(),
			"POST",
			"/?page=2&title=q",
			strings.NewReader(`{"title":"Hi","n":12345678901234567890,"tags":["a"]}`),
		)
		r.Header.Set("Content-Type", "application/json")

		c, _ := newCtx(t, r, nil)
		got, err := c.Input()
		if err != nil {
			t.Fatal(err)
		}

		want := map[string]any{"page": "2", "title": "Hi", "n": json.Number("12345678901234567890"), "tags": []any{"a"}}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatalf("Input() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("urlencoded nesting", func(t *testing.T) {
		t.Parallel()

		form := url.Values{
			"title":          {"x"},
			"tags[]":         {"a", "b"},
			"ids":            {"1", "2"},
			"author[email]":  {"e@x.io"},
			"items[0][name]": {"one"},
			"items[1][name]": {"two"},
			"meta[a][b][c]":  {"deep"},
			"broken[":        {"skip"},
			"[nokey]":        {"skip"},
			"weird[a]b":      {"skip"},
			"sparse[3]":      {"kept as map"},
			"single[]":       {"only"},
			"checkbox":       {"on"},
		}
		r := httptest.NewRequestWithContext(t.Context(), "POST", "/", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		c, _ := newCtx(t, r, nil)
		got, err := c.Input()
		if err != nil {
			t.Fatal(err)
		}

		want := map[string]any{
			"title":    "x",
			"tags":     []any{"a", "b"},
			"ids":      []any{"1", "2"},
			"author":   map[string]any{"email": "e@x.io"},
			"items":    []any{map[string]any{"name": "one"}, map[string]any{"name": "two"}},
			"meta":     map[string]any{"a": map[string]any{"b": map[string]any{"c": "deep"}}},
			"sparse":   map[string]any{"3": "kept as map"},
			"single":   []any{"only"},
			"checkbox": "on",
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatalf("Input() mismatch (-want +got):\n%s", diff)
		}
		if c.FormValue("title") != "x" {
			t.Fatal("FormValue")
		}
	})

	t.Run("multipart with file", func(t *testing.T) {
		t.Parallel()

		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		_ = mw.WriteField("name", "doc")
		fw, _ := mw.CreateFormFile("upload", "a.txt")
		_, _ = fw.Write([]byte("content"))
		_ = mw.Close()

		r := httptest.NewRequestWithContext(t.Context(), "POST", "/", &body)
		r.Header.Set("Content-Type", mw.FormDataContentType())

		c, _ := newCtx(t, r, nil)
		got, err := c.Input()
		if err != nil || got["name"] != "doc" {
			t.Fatalf("Input() = %v, %v", got, err)
		}

		if fh, ok := got["upload"].(*multipart.FileHeader); !ok || fh.Filename != "a.txt" {
			t.Fatalf("Input()[upload] = %#v, want the file header", got["upload"])
		}

		f, h, err := c.FormFile("upload")
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		b, _ := io.ReadAll(f)
		if string(b) != "content" || h.Filename != "a.txt" {
			t.Fatalf("FormFile() = %q %q", b, h.Filename)
		}

		if _, _, err := c.FormFile("missing"); err == nil {
			t.Fatal("FormFile(missing) error = nil")
		}
	})

	t.Run("no body", func(t *testing.T) {
		t.Parallel()

		c, _ := newCtx(t, httptest.NewRequestWithContext(t.Context(), "GET", "/?a=1", http.NoBody), nil)
		got, err := c.Input()
		if err != nil || got["a"] != "1" || c.QueryInput()["a"] != "1" {
			t.Fatalf("Input() = %v, %v", got, err)
		}
	})
}

func TestBindJSONErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, body, ct string
		limit          int64
		wantStatus     int
		wantMsg        string
	}{
		{name: "ok", body: `{"a":1}`, ct: "application/json", wantStatus: 0},
		{name: "not json", body: `a=1`, ct: "text/plain", wantStatus: 415},
		{name: "syntax", body: `{"a":`, ct: "application/json", wantStatus: 400},
		{name: "bad syntax token", body: `{"a":x}`, ct: "application/json", wantStatus: 400, wantMsg: "Malformed JSON"},
		{name: "wrong type", body: `[1]`, ct: "application/json", wantStatus: 400, wantMsg: "wrong JSON type"},
		{name: "empty", body: ``, ct: "application/json", wantStatus: 400, wantMsg: "empty"},
		{
			name:       "trailing",
			body:       `{"a":1}{"b":2}`,
			ct:         "application/json",
			wantStatus: 400,
			wantMsg:    "single JSON value",
		},
		{
			name:       "too large",
			body:       `{"a":"` + strings.Repeat("x", 100) + `"}`,
			ct:         "application/json",
			limit:      10,
			wantStatus: 413,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequestWithContext(t.Context(), "POST", "/", strings.NewReader(tt.body))
			r.Header.Set("Content-Type", tt.ct)

			rec := httptest.NewRecorder()
			if tt.limit > 0 {
				r = LimitBody(rec, r, tt.limit)
			}

			c := NewCtx(rec, r, nil)

			var v map[string]any
			err := c.BindJSON(&v)

			if tt.wantStatus == 0 {
				if err != nil {
					t.Fatalf("BindJSON() = %v", err)
				}
				return
			}
			if errs.Status(err) != tt.wantStatus {
				t.Fatalf("BindJSON() status = %d (%v), want %d", errs.Status(err), err, tt.wantStatus)
			}
			if tt.wantMsg != "" && !strings.Contains(err.Error(), tt.wantMsg) {
				t.Fatalf("BindJSON() = %v, want %q", err, tt.wantMsg)
			}
		})
	}
}

func TestSecurity_A06_InputRejectsOversizedForm(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequestWithContext(t.Context(), "POST", "/", strings.NewReader("a="+strings.Repeat("x", 100)))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rec := httptest.NewRecorder()
	c := NewCtx(rec, LimitBody(rec, r, 10), nil)

	if _, err := c.Input(); errs.Status(err) != http.StatusRequestEntityTooLarge {
		t.Fatalf("Input() = %v, want 413", err)
	}
}

func TestLimitBodyReplacesEarlierLimit(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name          string
		first, second int64
		wantErr       bool
	}{
		{name: "route raises the router default", first: 10, second: 1000},
		{name: "route lowers it", first: 1000, second: 10, wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequestWithContext(t.Context(), "POST", "/", strings.NewReader(strings.Repeat("x", 100)))
			rec := httptest.NewRecorder()
			r = LimitBody(rec, LimitBody(rec, r, tt.first), tt.second)

			_, err := io.ReadAll(r.Body)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ReadAll() = %v, wantErr %v", err, tt.wantErr)
			}

			if n, ok := BodyLimitOf(r); !ok || n != tt.second {
				t.Fatalf("BodyLimitOf() = %d, %v, want %d", n, ok, tt.second)
			}
		})
	}
}

func TestFormToMapDepthLimit(t *testing.T) {
	t.Parallel()

	key := "a" + strings.Repeat("[x]", maxInputDepth+1)
	if got := FormToMap(url.Values{key: {"v"}}); len(got) != 0 {
		t.Fatalf("FormToMap(deep) = %v, want dropped", got)
	}
}

// Security events are WARN records with a stable "event" attribute, the
// request method and path, so alerts can match on them.
func TestSecurity_A09_SecurityEventsAtWarn(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	s := &Services{Log: slog.New(slog.NewJSONHandler(&buf, nil))}
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/login", http.NoBody)
	r = r.WithContext(WithServices(r.Context(), s))

	SecurityEvent(r, "auth.login_failed", "guard", "web")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("log record %q: %v", buf.String(), err)
	}

	want := map[string]any{
		"level":  "WARN",
		"event":  "auth.login_failed",
		"method": "POST",
		"path":   "/login",
		"guard":  "web",
	}
	for k, v := range want {
		if rec[k] != v {
			t.Errorf("security event %s = %v, want %v (record %s)", k, rec[k], v, buf.String())
		}
	}
}

func FuzzFormToMap(f *testing.F) {
	for _, s := range []string{"a=1", "a[]=1&a[]=2", "a[b][0]=x", "a[=1", "[]=x", "a[0]=1&a[1][b]=2"} {
		f.Add(s)
	}
	f.Fuzz(func(_ *testing.T, q string) {
		vals, err := url.ParseQuery(q)
		if err != nil {
			return
		}
		_ = FormToMap(vals)
	})
}

func FuzzSafeRedirect(f *testing.F) {
	for _, s := range []string{
		"/ok", "//evil.com", `/\evil.com`, "http://example.com//evil.com", "https://example.com/a?b=1",
		"javascript:alert(1)", "/%2F/x", "http://example.com:80/x", "\t//evil.com", "",
	} {
		f.Add(s)
	}

	r := httptest.NewRequestWithContext(f.Context(), http.MethodGet, "http://example.com/", http.NoBody)
	base, _ := url.Parse("http://example.com/page")

	f.Fuzz(func(t *testing.T, target string) {
		got := SafeRedirect(r, target, "/fallback")
		if got == "/fallback" {
			return
		}

		// Whatever is accepted must stay on this origin once a browser
		// resolves it against the current page.
		ref, err := url.Parse(got)
		if err != nil {
			t.Fatalf("SafeRedirect(%q) = %q, unparsable", target, got)
		}

		if u := base.ResolveReference(ref); u.Host != "example.com" || u.Scheme != "http" || !isLocalPath(got) {
			t.Fatalf("SafeRedirect(%q) = %q, leaves the origin", target, got)
		}

		// http.Redirect cleans relative paths: check what the browser gets.
		w := httptest.NewRecorder()
		http.Redirect(w, r, got, http.StatusFound)

		if loc := w.Header().Get("Location"); !isLocalPath(loc) {
			t.Fatalf("Redirect(SafeRedirect(%q)) Location = %q, leaves the origin", target, loc)
		}
	})
}
