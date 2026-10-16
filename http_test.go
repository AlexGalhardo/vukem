package lumek

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/AlexGalhardo/golang-framework/router"
	"github.com/AlexGalhardo/golang-framework/web"
)

func newTestApp(t *testing.T, opts ...Option) *App {
	t.Helper()

	app, err := New(append([]Option{WithSource(testSource(nil)), WithLogger(discardLogger())}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}

	return app
}

func doReq(t *testing.T, app *App, target string) *httptest.ResponseRecorder {
	t.Helper()

	h, err := app.Handler()
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody))

	return rec
}

func TestHealthEndpoint(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	rec := doReq(t, app, "/up")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Fatalf("/up = %d %q", rec.Code, rec.Body.String())
	}

	app.AddHealthCheck("db", func(context.Context) error { return nil })
	app.AddHealthCheck("cache", func(context.Context) error { return errors.New("redis: connection refused 10.0.0.5") })

	rec = doReq(t, app, "/up?checks=1")

	var body struct {
		Status string            `json:"status"`
		Checks map[string]string `json:"checks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}

	if rec.Code != 503 || body.Status != "fail" || body.Checks["db"] != "ok" || body.Checks["cache"] != "fail" {
		t.Fatalf("/up?checks=1 = %d %+v", rec.Code, body)
	}
	if strings.Contains(rec.Body.String(), "10.0.0.5") {
		t.Fatal("health check leaked error details")
	}
}

func TestDefaultMiddlewareAndRoutes(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, WithRoutes(func(r *router.Router, app *App) error {
		r.Get("/hello", func(c *web.Ctx) error { return c.String(200, "hi "+app.Config.Name) }).Name("hello")
		r.Get("/boom", func(*web.Ctx) error { panic("x") })

		return DefaultMiddleware(app, r)
	}))

	rec := doReq(t, app, "/hello")
	if rec.Body.String() != "hi Lumek" || rec.Header().Get("X-Request-ID") == "" ||
		rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("/hello = %d %q %v", rec.Code, rec.Body.String(), rec.Header())
	}

	if rec := doReq(t, app, "/boom"); rec.Code != 500 {
		t.Fatalf("/boom = %d, want recovered 500", rec.Code)
	}
}

func TestRunMainServesHTTP(t *testing.T) {
	t.Parallel()

	ready := make(chan string, 1)
	ctx, cancel := context.WithCancel(t.Context())

	getenv := func(k string) (string, bool) {
		v, ok := map[string]string{"APP_KEY": testKey, "APP_ENV": "test", "PORT": "0", "HOST": "127.0.0.1"}[k]
		return v, ok
	}

	build := func(opts ...Option) (*App, error) {
		return New(append(opts, WithLogger(discardLogger()), WithReady(ready))...)
	}

	errc := make(chan error, 1)
	go func() { errc <- RunMain(ctx, []string{"app"}, getenv, build) }()

	addr := <-ready

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr+"/up", http.NoBody)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()

	if res.StatusCode != http.StatusOK || !strings.Contains(string(b), "ok") {
		t.Fatalf("GET /up = %d %q", res.StatusCode, b)
	}

	cancel()
	if err := <-errc; err != nil {
		t.Fatalf("RunMain() = %v", err)
	}
}

func TestCommands(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	getenv := func(k string) (string, bool) {
		v, ok := map[string]string{"APP_KEY": testKey, "APP_ENV": "test"}[k]
		return v, ok
	}

	var gotArgs []string
	var verbose *bool

	build := func(opts ...Option) (*App, error) {
		app, err := New(
			append(
				opts,
				WithLogger(discardLogger()),
				WithStdout(&out),
				WithRoutes(func(r *router.Router, _ *App) error {
					r.Get("/posts", func(*web.Ctx) error { return nil }).Name("posts.index")
					return nil
				}),
			)...)
		if err != nil {
			return nil, err
		}

		app.AddCommand(CommandFunc{
			CommandName: "reports:send",
			Desc:        "Send reports",
			DefineFlags: func(fs *flag.FlagSet) { verbose = fs.Bool("v", false, "verbose") },
			Fn: func(_ context.Context, a *App, args []string) error {
				if a.Kind != KindConsole {
					return errors.New("wrong kind")
				}
				gotArgs = args
				return nil
			},
		})

		return app, nil
	}

	run := func(args ...string) error {
		out.Reset()
		return RunMain(t.Context(), append([]string{"app"}, args...), getenv, build)
	}

	if err := run("reports:send", "-v", "a", "b"); err != nil || !*verbose || strings.Join(gotArgs, ",") != "a,b" {
		t.Fatalf("reports:send = %v args=%v", err, gotArgs)
	}

	if err := run(
		"list",
	); err != nil || !strings.Contains(out.String(), "reports:send") ||
		!strings.Contains(out.String(), "route:list") {
		t.Fatalf("list = %v %q", err, out.String())
	}

	if err := run(
		"route:list",
	); err != nil || !strings.Contains(out.String(), "/posts") ||
		!strings.Contains(out.String(), "posts.index") {
		t.Fatalf("route:list = %v %q", err, out.String())
	}

	if err := run("route:list", "-json"); err != nil || !strings.Contains(out.String(), `"path": "/up"`) {
		t.Fatalf("route:list -json = %v %q", err, out.String())
	}

	if err := run(
		"about",
	); err != nil || !strings.Contains(out.String(), "[redacted]") ||
		strings.Contains(out.String(), testKey) {
		t.Fatalf("about = %v %q", err, out.String())
	}

	if err := run("about", "-json"); err != nil || !strings.Contains(out.String(), `"Environment":"test"`) {
		t.Fatalf("about -json = %v %q", err, out.String())
	}

	if err := run("nope"); !errors.Is(err, errUsage) || !strings.Contains(out.String(), `unknown command "nope"`) {
		t.Fatalf("unknown = %v %q", err, out.String())
	}

	if err := run("reports:send", "-bad"); !errors.Is(err, errUsage) {
		t.Fatalf("bad flag = %v", err)
	}

	if err := run("reports:send", "-h"); err != nil {
		t.Fatalf("-h = %v", err)
	}
}

func TestRunMainBuildError(t *testing.T) {
	t.Parallel()

	err := RunMain(t.Context(), []string{"app"}, func(string) (string, bool) { return "", false },
		New)
	if err == nil || !strings.Contains(err.Error(), "APP_KEY") {
		t.Fatalf("RunMain() = %v, want APP_KEY error", err)
	}
}

func TestWithViews(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		"home.html":        {Data: []byte(`{{define "content"}}<a href="{{route "home"}}">home</a>{{end}}`)},
		"layouts/app.html": {Data: []byte(`<main>{{block "content" .}}{{end}}</main>`)},
	}

	app := newTestApp(t, WithViews(fsys), WithRoutes(func(r *router.Router, _ *App) error {
		r.Get("/", func(c *web.Ctx) error { return c.Render("home", nil) }).Name("home")
		return nil
	}))

	rec := doReq(t, app, "/")
	if rec.Body.String() != `<main><a href="/">home</a></main>` || app.Views == nil {
		t.Fatalf("render = %d %q", rec.Code, rec.Body.String())
	}

	if _, err := New(WithSource(testSource(nil)), WithLogger(discardLogger()),
		WithViews(fstest.MapFS{"bad.html": {Data: []byte("{{ x")}})); err == nil {
		t.Fatal("broken views accepted at boot")
	}

	failing := WithRoutes(func(*router.Router, *App) error { return errors.New("no db") })
	if _, err := New(WithSource(testSource(nil)), WithLogger(discardLogger()), failing); err == nil ||
		!strings.Contains(err.Error(), "routes: no db") {
		t.Fatalf("routes error = %v", err)
	}
}
