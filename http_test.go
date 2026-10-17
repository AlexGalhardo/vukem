package vukem

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/internal/testutil"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/schedule"
	"github.com/AlexGalhardo/vukem/schema"
	"github.com/AlexGalhardo/vukem/web"
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
	if rec.Body.String() != "hi Vukem" || rec.Header().Get("X-Request-ID") == "" ||
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

func TestRunMainServesH2COutsideProduction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"development", map[string]string{"APP_ENV": "development"}, true},
		{"production", map[string]string{"APP_ENV": "production"}, false},
		{"production opt-in", map[string]string{"APP_ENV": "production", "HTTP_H2C": "true"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ready := make(chan string, 1)
			ctx, cancel := context.WithCancel(t.Context())
			env := map[string]string{"APP_KEY": testKey, "PORT": "0", "HOST": "127.0.0.1"}
			maps.Copy(env, tt.env)
			getenv := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
			build := func(opts ...Option) (*App, error) {
				return New(append(opts, WithLogger(discardLogger()), WithReady(ready))...)
			}

			errc := make(chan error, 1)
			go func() { errc <- RunMain(ctx, []string{"app"}, getenv, build) }()

			addr := <-ready

			var protocols http.Protocols
			protocols.SetUnencryptedHTTP2(true)
			client := &http.Client{Transport: &http.Transport{Protocols: &protocols}}

			req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr+"/up", http.NoBody)
			res, err := client.Do(req)
			if err == nil {
				res.Body.Close()
			}

			if got := err == nil && res.ProtoMajor == 2; got != tt.want {
				t.Errorf("h2c GET /up served = %v (err %v), want %v", got, err, tt.want)
			}

			cancel()
			if err := <-errc; err != nil {
				t.Fatalf("RunMain() = %v", err)
			}
		})
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

	if err := run("about", "-json"); err != nil || !strings.Contains(out.String(), `"Environment":"test"`) ||
		!strings.Contains(out.String(), `"Trusted proxies":"none"`) {
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

	// Outside production a missing APP_KEY is not a boot error.
	getenv := func(k string) (string, bool) { return map[string]string{"APP_ENV": "production"}[k], k == "APP_ENV" }

	err := RunMain(t.Context(), []string{"app"}, getenv, New)
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

	public := fstest.MapFS{"build/.vite/manifest.json": {Data: []byte(`{"app.ts": {"file": "assets/app-1.js"}}`)}}
	app = newTestApp(t, WithViews(fstest.MapFS{"v.html": {Data: []byte(`{{vite "app.ts"}}`)}}), WithVite(public),
		WithRoutes(func(r *router.Router, _ *App) error {
			r.Get("/", func(c *web.Ctx) error { return c.Render("v", nil) })
			return nil
		}))

	if got := doReq(t, app, "/").Body.String(); !strings.Contains(got, `src="/build/assets/app-1.js"`) {
		t.Fatalf("vite render = %q", got)
	}

	failing := WithRoutes(func(*router.Router, *App) error { return errors.New("no db") })
	if _, err := New(WithSource(testSource(nil)), WithLogger(discardLogger()), failing); err == nil ||
		!strings.Contains(err.Error(), "routes: no db") {
		t.Fatalf("routes error = %v", err)
	}
}

func TestSessionDrivers(t *testing.T) {
	t.Parallel()

	dbURL := "file:" + filepath.ToSlash(filepath.Join(testutil.TempDir(t), "s.db"))

	for driver, wantErr := range map[string]bool{"cookie": false, "memory": false, "database": false, "redis": true} {
		app, err := New(WithSource(testSource(map[string]string{"SESSION_DRIVER": driver, "DATABASE_URL": dbURL})),
			WithLogger(discardLogger()))
		if err != nil {
			t.Fatal(err)
		}

		t.Cleanup(func() { _ = app.Shutdown(context.Background()) })

		if _, err := app.Sessions(); (err != nil) != wantErr {
			t.Errorf("SESSION_DRIVER=%s: err = %v", driver, err)
		}
	}

	db, err := database.Open(t.Context(), dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if has, err := schema.New(t.Context(), db).HasTable("sessions"); err != nil || !has {
		t.Fatalf("sessions table: has=%v err=%v", has, err)
	}

	broken, _ := New(
		WithSource(testSource(map[string]string{"SESSION_DRIVER": "database", "DATABASE_URL": "mysql://x"})),
		WithLogger(discardLogger()),
	)
	if _, err := broken.Sessions(); err == nil {
		t.Fatal("database driver without a usable database")
	}
}

func TestAPIMiddleware(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, WithRoutes(func(r *router.Router, app *App) error {
		r.Post("/api/things", func(c *web.Ctx) error { return c.JSON(201, map[string]string{"ok": "yes"}) })
		return APIMiddleware(app, r)
	}))

	h, err := app.Handler()
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/things", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	// No session cookie and no CSRF check for API apps.
	if rec.Code != 201 || rec.Header().Get("Set-Cookie") != "" || rec.Header().Get("X-Request-Id") == "" {
		t.Fatalf("api = %d %v", rec.Code, rec.Header())
	}
}

func TestDatabaseSessionsArePrunedOnSchedule(t *testing.T) {
	t.Parallel()

	dbURL := "file:" + filepath.ToSlash(filepath.Join(testutil.TempDir(t), "s.db"))

	newApp := func(driver string) *App {
		app, err := New(WithSource(testSource(map[string]string{"SESSION_DRIVER": driver, "DATABASE_URL": dbURL})),
			WithLogger(discardLogger()))
		if err != nil {
			t.Fatal(err)
		}

		t.Cleanup(func() { _ = app.Shutdown(context.Background()) })

		return app
	}

	app := newApp("database")

	s, err := app.Schedule()
	if err != nil {
		t.Fatal(err)
	}

	var task *schedule.Task
	for _, tk := range s.Tasks() {
		if tk.Name() == "session:prune" {
			task = tk
		}
	}

	if task == nil || task.Expression() != "0 * * * *" {
		t.Fatalf("session:prune not scheduled hourly: %v", task)
	}

	if _, err := app.Sessions(); err != nil {
		t.Fatal(err)
	}

	db, err := app.DB()
	if err != nil {
		t.Fatal(err)
	}

	// One expired row, one live row.
	if _, err := db.ExecContext(t.Context(), "INSERT INTO sessions (id, data, expires_at) VALUES (?, ?, ?), (?, ?, ?)",
		"OLD", "x", time.Now().Add(-time.Hour).Unix(), "NEW", "x", time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}

	if err := app.Call(t.Context(), "session:prune", nil); err != nil {
		t.Fatalf("session:prune = %v", err)
	}

	var n int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM sessions").Scan(&n); err != nil || n != 1 {
		t.Fatalf("sessions after prune = %d, %v, want 1", n, err)
	}

	// Cookie sessions have nothing to prune and no task.
	cs, err := newApp("cookie").Schedule()
	if err != nil {
		t.Fatal(err)
	}

	for _, tk := range cs.Tasks() {
		if tk.Name() == "session:prune" {
			t.Fatal("session:prune scheduled for cookie sessions")
		}
	}
}
