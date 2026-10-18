package view

import (
	"bytes"
	"context"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/AlexGalhardo/vukem/middleware"
	"github.com/AlexGalhardo/vukem/vite"
	"github.com/AlexGalhardo/vukem/web"
)

var views = fstest.MapFS{
	"layouts/app.html": {Data: []byte(`<html><title>{{block "title" .}}Vukem{{end}}</title>` +
		`<body>{{template "partials/nav" .}}{{block "content" .}}{{end}}</body></html>`)},
	"layouts/admin.html":    {Data: []byte(`<admin>{{block "content" .}}{{end}}</admin>`)},
	"partials/nav.html":     {Data: []byte(`<nav>{{.User}}</nav>`)},
	"components/alert.html": {Data: []byte(`<div class="alert {{.Type}}">{{.Text}}</div>`)},
	"posts/index.html": {Data: []byte(`{{define "title"}}Posts{{end}}{{define "content"}}<h1>Posts</h1>` +
		`{{block "post-list" .}}<ul>{{range .Posts}}<li>{{.}}</li>{{end}}</ul>{{end}}{{end}}`)},
	"admin/dash.html": {Data: []byte("{{/* layout: admin */}}{{define \"content\"}}dash{{end}}")},
	"plain.html":      {Data: []byte("{{/* layout: none */}}<p>{{.Msg}}</p>")},
	"helpers.html": {Data: []byte(`{{/* layout: none */}}` +
		`{{route "posts.show" "id" 7}}|{{asset "css/app.css"}}|{{old "title"}}|{{old "author.email"}}|{{error "title"}}|` +
		`{{len errors}}|{{flash "status"}}|{{cspNonce}}|{{template "components/alert" dict "Type" "ok" "Text" "<b>"}}|` +
		`<script>var data = {{json .}};</script>|{{csrfField}}|{{htmxScript "/js/htmx.min.js"}}`)},
	"readme.txt": {Data: []byte("ignored")},
}

type urls struct{}

func (urls) URL(string, ...any) (string, error) { return "/posts/7", nil }

func newEngine(t *testing.T, fsys fstest.MapFS, reload bool) *Engine {
	t.Helper()

	e, err := New(Config{FS: fsys, Reload: reload, URLs: urls{}})
	if err != nil {
		t.Fatal(err)
	}

	return e
}

func render(ctx context.Context, t *testing.T, e *Engine, name string, data any) string {
	t.Helper()

	var b bytes.Buffer
	if err := e.Render(ctx, &b, name, data); err != nil {
		t.Fatal(err)
	}

	return b.String()
}

func TestLayoutsBlocksPartials(t *testing.T) {
	t.Parallel()

	e := newEngine(t, views, false)
	data := map[string]any{"User": "ana", "Posts": []string{"a", "<b>"}}

	got := render(t.Context(), t, e, "posts/index", data)
	want := `<html><title>Posts</title><body><nav>ana</nav><h1>Posts</h1><ul><li>a</li><li>&lt;b&gt;</li></ul></body></html>`
	if got != want {
		t.Fatalf("page =\n%s\nwant\n%s", got, want)
	}

	var b bytes.Buffer
	if err := e.RenderBlock(t.Context(), &b, "posts/index", "post-list", data); err != nil {
		t.Fatal(err)
	}
	if b.String() != `<ul><li>a</li><li>&lt;b&gt;</li></ul>` {
		t.Fatalf("fragment = %s", b.String())
	}

	if got := render(t.Context(), t, e, "admin/dash", nil); got != "<admin>dash</admin>" {
		t.Fatalf("custom layout = %s", got)
	}
	if got := render(t.Context(), t, e, "plain", map[string]string{"Msg": "<x>"}); got != "<p>&lt;x&gt;</p>" {
		t.Fatalf("no layout = %s", got)
	}

	if !e.Has("posts/index") || e.Has("layouts/app") || e.Has("readme") {
		t.Fatal("Has")
	}
	if len(e.Names()) != 4 {
		t.Fatalf("Names = %v", e.Names())
	}

	if err := e.Render(t.Context(), &b, "missing", nil); err == nil {
		t.Fatal("missing view rendered")
	}
	if err := e.RenderBlock(t.Context(), &b, "posts/index", "nope", nil); err == nil {
		t.Fatal("missing block rendered")
	}
}

type fakeSession map[string]any

func (f fakeSession) Get(k string) any    { return f[k] }
func (f fakeSession) Put(k string, v any) { f[k] = v }
func (fakeSession) Forget(...string)      {}
func (fakeSession) Pull(string) any       { return nil }
func (fakeSession) Flash(string, any)     {}
func (fakeSession) Regenerate()           {}
func (fakeSession) Invalidate()           {}
func (fakeSession) ID() string            { return "" }

func TestRequestHelpers(t *testing.T) {
	t.Parallel()

	sess := fakeSession{
		web.OldInputKey: map[string]any{"title": "Hello", "author": map[string]any{"email": "a@b.co"}},
		web.ErrorsKey:   map[string]any{"title": []any{"Too short."}},
		"status":        "Saved!",
	}

	// Run SecureHeaders to get a nonce into the context.
	reqCtx := make(chan context.Context, 1)
	middleware.SecureHeaders(middleware.SecureHeadersConfig{})(
		http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { reqCtx <- r.Context() }),
	).ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), "GET", "/", http.NoBody))

	ctx := web.WithSession(<-reqCtx, sess)

	got := render(ctx, t, newEngine(t, views, false), "helpers", map[string]string{"x": "</script>"})
	nonce := middleware.CSPNonce(ctx)
	token, _ := sess["_token"].(string)

	parts := strings.Split(got, "|")
	want := []string{
		"/posts/7", "/css/app.css", "Hello", "a@b.co", "Too short.", "1", "Saved!", nonce,
		`<div class="alert ok">&lt;b&gt;</div>`,
		`<script>var data = {"x":"\u003c/script\u003e"};</script>`,
		`<input type="hidden" name="_token" value="` + token + `">`,
		`<script src="/js/htmx.min.js" defer nonce="` + nonce + `"></script>`,
	}

	if len(parts) != len(want) || token == "" {
		t.Fatalf("output = %s", got)
	}

	for i := range want {
		if parts[i] != want[i] {
			t.Errorf("part %d = %q, want %q", i, parts[i], want[i])
		}
	}

	// Without a session the helpers degrade to empty values.
	got = render(t.Context(), t, newEngine(t, views, false), "helpers", nil)
	if !strings.HasPrefix(got, "/posts/7|/css/app.css||||0||") {
		t.Fatalf("no-session output = %s", got)
	}
}

func TestViteFuncs(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		"page.html": {Data: []byte(`{{/* layout: none */}}{{viteReactRefresh}}{{vite "resources/js/app.ts"}}|` +
			`{{asset "resources/img/logo.png"}}|{{asset "js/htmx.min.js"}}`)},
	}
	public := fstest.MapFS{"build/.vite/manifest.json": {Data: []byte(`{
		"resources/js/app.ts": {"file": "assets/app-1.js", "isEntry": true},
		"resources/img/logo.png": {"file": "assets/logo-2.png"}}`)}}

	v, err := vite.New(vite.Config{FS: public})
	if err != nil {
		t.Fatal(err)
	}

	e, err := New(Config{FS: fsys, Vite: v})
	if err != nil {
		t.Fatal(err)
	}

	got := render(t.Context(), t, e, "page", nil)
	want := `<script type="module" src="/build/assets/app-1.js"></script>` + "\n" +
		`|/build/assets/logo-2.png|/js/htmx.min.js`

	if got != want {
		t.Errorf("page = %q, want %q", got, want)
	}

	var b bytes.Buffer
	if err := newEngine(t, fsys, false).Render(t.Context(), &b, "page", nil); err == nil {
		t.Error("vite without Config.Vite rendered, want error")
	}
}

func TestReloadAndErrors(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{"home.html": {Data: []byte("v1")}}
	e := newEngine(t, fsys, true)

	if got := render(t.Context(), t, e, "home", nil); got != "v1" {
		t.Fatal(got)
	}

	fsys["home.html"] = &fstest.MapFile{Data: []byte("v2")}
	if got := render(t.Context(), t, e, "home", nil); got != "v2" {
		t.Fatalf("reload = %s", got)
	}

	fsys["home.html"] = &fstest.MapFile{Data: []byte("{{ broken")}
	if err := e.Render(t.Context(), &bytes.Buffer{}, "home", nil); err == nil {
		t.Fatal("broken view rendered on reload")
	}

	if _, err := New(Config{}); err == nil {
		t.Fatal("missing FS accepted")
	}
	if _, err := New(Config{FS: fstest.MapFS{"a.html": {Data: []byte("{{ x")}}}); err == nil {
		t.Fatal("syntax error accepted at boot")
	}
	if _, err := New(Config{FS: fstest.MapFS{"partials/a.html": {Data: []byte("{{ x")}}}); err == nil {
		t.Fatal("partial syntax error accepted")
	}
	if _, err := New(Config{FS: fstest.MapFS{"a.html": {Data: []byte("{{/* layout: ghost */}}x")}}}); err == nil {
		t.Fatal("unknown layout accepted")
	}

	e2, _ := New(Config{FS: fstest.MapFS{"r.html": {Data: []byte(`{{route "x"}}`)}}})
	if err := e2.Render(t.Context(), &bytes.Buffer{}, "r", nil); err == nil {
		t.Fatal("route without URLs")
	}
	if _, err := dict("a"); err == nil {
		t.Fatal("dict odd")
	}
	if _, err := dict(1, 2); err == nil {
		t.Fatal("dict non-string key")
	}
}

func TestCtxRenderIntegration(t *testing.T) {
	t.Parallel()

	e := newEngine(t, views, false)
	s := &web.Services{Views: e, Log: slog.New(slog.DiscardHandler)}
	data := map[string]any{"User": "ana", "Posts": []string{"a"}}

	run := func(r *http.Request, h web.HandlerFunc) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		web.NewCtx(rec, r, s).Serve(h)
		return rec
	}

	index := func(c *web.Ctx) error { return c.Render("posts/index", data, web.Block("post-list")) }

	full := run(httptest.NewRequestWithContext(t.Context(), "GET", "/", http.NoBody), index)
	if !strings.Contains(full.Body.String(), "<html>") || full.Header().Get("Vary") != "HX-Request" {
		t.Fatalf("full page = %s %v", full.Body.String(), full.Header())
	}

	r := httptest.NewRequestWithContext(t.Context(), "GET", "/", http.NoBody)
	r.Header.Set("HX-Request", "true")
	r.Header.Set("HX-Request-Type", "partial")
	if got := run(r, index).Body.String(); got != "<ul><li>a</li></ul>" {
		t.Fatalf("htmx partial = %s", got)
	}

	frag := run(httptest.NewRequestWithContext(t.Context(), "GET", "/", http.NoBody), func(c *web.Ctx) error {
		return c.Fragment("posts/index", "post-list", data, web.WithStatus(http.StatusCreated))
	})
	if frag.Code != 201 || frag.Body.String() != "<ul><li>a</li></ul>" {
		t.Fatalf("Fragment = %d %s", frag.Code, frag.Body.String())
	}

	bad := run(httptest.NewRequestWithContext(t.Context(), "GET", "/", http.NoBody), func(c *web.Ctx) error {
		return c.Render("missing", nil)
	})
	if bad.Code != 500 || strings.Contains(bad.Body.String(), "missing") {
		t.Fatalf("render error = %d %s", bad.Code, bad.Body.String())
	}

	comp := run(httptest.NewRequestWithContext(t.Context(), "GET", "/", http.NoBody), func(c *web.Ctx) error {
		return c.View(component("<p>templ</p>"))
	})
	if comp.Body.String() != "<p>templ</p>" {
		t.Fatalf("View = %s", comp.Body.String())
	}
}

type component string

func (c component) Render(_ context.Context, w io.Writer) error {
	_, err := io.WriteString(w, string(c))
	return err
}

func TestSecurity_A03_CustomFuncsCannotReplaceBuiltins(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{"home.html": {Data: []byte(`{{shout "hi"}}`)}}

	for _, name := range []string{"csrfField", "cspNonce", "htmxScript", "errors", "route", "asset"} {
		_, err := New(Config{FS: fsys, Funcs: template.FuncMap{
			"shout": strings.ToUpper,
			name:    func() string { return "" },
		}})
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("New(Funcs %q) = %v, want an error naming the built-in", name, err)
		}
	}

	if _, err := New(Config{FS: fsys, Funcs: template.FuncMap{"shout": strings.ToUpper}}); err != nil {
		t.Fatalf("New(custom func) = %v", err)
	}
}
