package vukem

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/api"
	"github.com/AlexGalhardo/vukem/cache"
	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/csrf"
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/i18n"
	"github.com/AlexGalhardo/vukem/internal/testutil"
	"github.com/AlexGalhardo/vukem/mail"
	"github.com/AlexGalhardo/vukem/middleware"
	"github.com/AlexGalhardo/vukem/openapi"
	"github.com/AlexGalhardo/vukem/queue"
	"github.com/AlexGalhardo/vukem/ratelimit"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/schedule"
	"github.com/AlexGalhardo/vukem/schema"
	"github.com/AlexGalhardo/vukem/web"
	"github.com/AlexGalhardo/vukem/z"
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

func TestWithViewFuncs(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{"home.html": {Data: []byte(`{{upper "hi"}} {{money 1234}}`)}}

	app := newTestApp(t, WithViews(fsys),
		WithViewFuncs(template.FuncMap{"upper": strings.ToUpper}),
		WithViewFuncs(template.FuncMap{"money": func(c int) string { return fmt.Sprintf("$%d.%02d", c/100, c%100) }}),
		WithRoutes(func(r *router.Router, _ *App) error {
			r.Get("/", func(c *web.Ctx) error { return c.Render("home", nil) })
			return nil
		}))

	if rec := doReq(t, app, "/"); rec.Body.String() != "HI $12.34" {
		t.Fatalf("render = %d %q, want %q", rec.Code, rec.Body.String(), "HI $12.34")
	}

	bad := map[string][]Option{
		"without WithViews": {WithViewFuncs(template.FuncMap{"upper": strings.ToUpper})},
		"duplicate name": {
			WithViews(fsys),
			WithViewFuncs(template.FuncMap{"upper": strings.ToUpper}),
			WithViewFuncs(template.FuncMap{"upper": strings.ToLower}),
		},
		"nil func": {WithViews(fsys), WithViewFuncs(template.FuncMap{"upper": nil})},
		"built-in name": {
			WithViews(fsys),
			WithViewFuncs(template.FuncMap{"csrfField": func() template.HTML { return "" }}),
		},
	}
	for name, opts := range bad {
		opts = append(opts, WithSource(testSource(nil)), WithLogger(discardLogger()))
		if _, err := New(opts...); err == nil {
			t.Errorf("New(%s) = nil error, want a boot error", name)
		}
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

type healthBody struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

func serveReady(t *testing.T, app *App) (int, healthBody, *httptest.ResponseRecorder) {
	t.Helper()

	rec := httptest.NewRecorder()
	app.ReadyHandler().
		ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/ready", http.NoBody))

	var body healthBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("ReadyHandler body %q: %v", rec.Body.String(), err)
	}

	return rec.Code, body, rec
}

func TestReadyHandler(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		checks   map[string]HealthCheck
		wantCode int
		want     healthBody
	}{
		{
			name:     "no checks",
			wantCode: http.StatusOK,
			want:     healthBody{Status: "ok", Checks: map[string]string{}},
		},
		{
			name:     "all healthy",
			checks:   map[string]HealthCheck{"db": okCheck, "cache": okCheck},
			wantCode: http.StatusOK,
			want:     healthBody{Status: "ok", Checks: map[string]string{"db": "ok", "cache": "ok"}},
		},
		{
			name: "one failing",
			checks: map[string]HealthCheck{
				"db":    okCheck,
				"cache": func(context.Context) error { return errors.New("dial tcp 10.0.0.5:6379: refused") },
			},
			wantCode: http.StatusServiceUnavailable,
			want:     healthBody{Status: "fail", Checks: map[string]string{"db": "ok", "cache": "fail"}},
		},
		{
			name:     "panicking check",
			checks:   map[string]HealthCheck{"boom": func(context.Context) error { panic("secret-dsn") }},
			wantCode: http.StatusServiceUnavailable,
			want:     healthBody{Status: "fail", Checks: map[string]string{"boom": "fail"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			app := newTestApp(t)
			for name, check := range tt.checks {
				app.AddHealthCheck(name, check)
			}

			code, body, rec := serveReady(t, app)
			if code != tt.wantCode {
				t.Errorf("ReadyHandler status = %d, want %d", code, tt.wantCode)
			}

			if diff := cmp.Diff(tt.want, body); diff != "" {
				t.Errorf("ReadyHandler body (-want +got):\n%s", diff)
			}

			if got := rec.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store", got)
			}

			if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
				t.Errorf("Content-Type = %q, want application/json", got)
			}
		})
	}
}

func okCheck(context.Context) error { return nil }

func TestSecurity_A10_ReadyHandlerHidesErrors(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	app.AddHealthCheck("db", func(context.Context) error {
		return errors.New("postgres://admin:hunter2@10.0.0.5/app: connection refused")
	})
	app.AddHealthCheck("cache", func(context.Context) error { panic("redis password hunter2") })

	_, _, rec := serveReady(t, app)
	for _, leak := range []string{"hunter2", "10.0.0.5", "postgres", "refused", "redis"} {
		if strings.Contains(rec.Body.String(), leak) {
			t.Errorf("ReadyHandler body %q leaks %q", rec.Body.String(), leak)
		}
	}
}

func TestReadyHandlerPerCheckTimeout(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		app := newTestApp(t)
		release := make(chan struct{})

		// A check that ignores its context must not hang the probe.
		app.AddHealthCheckTimeout("stuck", time.Second, func(context.Context) error {
			<-release
			return nil
		})
		app.AddHealthCheckTimeout("slow", 5*time.Second, func(ctx context.Context) error {
			select {
			case <-time.After(2 * time.Second):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		app.AddHealthCheck("fast", okCheck)

		start := time.Now()
		got := app.CheckHealth(t.Context())
		elapsed := time.Since(start)
		close(release)

		if elapsed != 2*time.Second {
			t.Errorf("CheckHealth took %v, want 2s (checks run in parallel, stuck bounded at 1s)", elapsed)
		}

		if !errors.Is(got["stuck"], context.DeadlineExceeded) {
			t.Errorf("CheckHealth()[stuck] = %v, want DeadlineExceeded", got["stuck"])
		}

		if got["slow"] != nil || got["fast"] != nil {
			t.Errorf("CheckHealth() = %v, want slow and fast healthy", got)
		}
	})
}

func TestCheckHealthDefaultTimeout(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		app := newTestApp(t)
		app.AddHealthCheck("hang", func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		})

		start := time.Now()
		got := app.CheckHealth(t.Context())

		if elapsed := time.Since(start); elapsed != DefaultHealthCheckTimeout {
			t.Errorf("CheckHealth took %v, want %v", elapsed, DefaultHealthCheckTimeout)
		}

		if got["hang"] == nil {
			t.Error("CheckHealth()[hang] = nil, want a timeout error")
		}
	})
}

func TestReadyHandlerMountedWithoutSession(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, WithRoutes(func(r *router.Router, app *App) error {
		r.Handle("GET /ready", app.ReadyHandler())
		return DefaultMiddleware(app, r)
	}))
	app.AddHealthCheck("db", okCheck)

	for _, target := range []string{"/ready", "/up"} {
		rec := doReq(t, app, target)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", target, rec.Code)
		}

		if c := rec.Header().Get("Set-Cookie"); c != "" {
			t.Errorf("GET %s set a cookie %q, want none (probes must not create sessions)", target, c)
		}
	}
}

func TestSecurity_A10_HungHealthCheckRunsOnce(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		app := newTestApp(t)
		release := make(chan struct{})

		var started atomic.Int32

		app.AddHealthCheckTimeout("hung", time.Second, func(context.Context) error {
			started.Add(1)
			<-release

			return nil
		})

		// Probes keep hitting a hung dependency: each must fail fast without
		// piling up another goroutine behind the first.
		for i := range 5 {
			if err := app.CheckHealth(t.Context())["hung"]; err == nil {
				t.Fatalf("CheckHealth() #%d [hung] = nil, want an error", i)
			}
		}

		if got := started.Load(); got != 1 {
			t.Fatalf("hung check started %d times, want 1", got)
		}

		close(release)
		synctest.Wait()

		if err := app.CheckHealth(t.Context())["hung"]; err != nil {
			t.Fatalf("CheckHealth() after the hung run ended = %v, want nil", err)
		}

		if got := started.Load(); got != 2 {
			t.Fatalf("check started %d times after recovery, want 2", got)
		}
	})
}

func sessionRoutes(r *router.Router) {
	hasSession := func(c *web.Ctx) error {
		if c.Session() == nil {
			return c.String(http.StatusOK, "no session")
		}

		return c.String(http.StatusOK, "session")
	}

	r.Get("/page", hasSession)
	r.Get("/webhooks/{name}", hasSession)
	r.Post("/webhooks/{name}", hasSession)
	r.Post("/form", hasSession)
}

func serve(t *testing.T, app *App, method, target string, crossSite bool) *httptest.ResponseRecorder {
	t.Helper()

	h, err := app.Handler()
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequestWithContext(t.Context(), method, target, http.NoBody)
	if crossSite {
		req.Header.Set("Sec-Fetch-Site", "cross-site")
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

func TestDefaultMiddlewareWith(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, WithRoutes(func(r *router.Router, app *App) error {
		sessionRoutes(r)

		return DefaultMiddlewareWith(app, r, MiddlewareOptions{
			SecureHeaders: func(cfg *middleware.SecureHeadersConfig) {
				cfg.CSPSources = map[string][]string{"script-src": {"https://plausible.io"}}
				cfg.ReportURI = "/csp-reports"
			},
			SessionBypass: []string{"/webhooks/"},
			CSRFBypass:    []string{"POST /webhooks/{name}"},
		})
	}))

	tests := []struct {
		method, target string
		crossSite      bool
		code           int
		body           string
	}{
		{http.MethodGet, "/page", false, http.StatusOK, "session"},
		{http.MethodGet, "/webhooks/stripe", false, http.StatusOK, "no session"},
		{http.MethodPost, "/webhooks/stripe", true, http.StatusOK, "no session"},
		{http.MethodPost, "/form", true, http.StatusForbidden, ""},
		{http.MethodPost, "/form", false, http.StatusOK, "session"},
	}

	for _, tt := range tests {
		rec := serve(t, app, tt.method, tt.target, tt.crossSite)
		if rec.Code != tt.code || (tt.body != "" && rec.Body.String() != tt.body) {
			t.Errorf("%s %s (cross-site %v) = %d %q, want %d %q",
				tt.method, tt.target, tt.crossSite, rec.Code, rec.Body.String(), tt.code, tt.body)
		}
	}

	rec := serve(t, app, http.MethodGet, "/page", false)
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "https://plausible.io") ||
		!strings.Contains(csp, "report-uri /csp-reports") || !strings.Contains(csp, "'nonce-") {
		t.Errorf("CSP = %q, want the extra script source, the report URI and the nonce", csp)
	}
}

func TestDefaultMiddlewareWithStateless(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, WithRoutes(func(r *router.Router, app *App) error {
		sessionRoutes(r)

		return DefaultMiddlewareWith(app, r, MiddlewareOptions{Stateless: true})
	}))

	if rec := serve(t, app, http.MethodGet, "/page", false); rec.Body.String() != "no session" ||
		rec.Header().Get("Set-Cookie") != "" {
		t.Errorf("stateless GET /page = %q (Set-Cookie %q), want no session and no cookie",
			rec.Body.String(), rec.Header().Get("Set-Cookie"))
	}

	// CSRF protection stays on without sessions.
	if rec := serve(t, app, http.MethodPost, "/form", true); rec.Code != http.StatusForbidden {
		t.Errorf("stateless cross-site POST /form = %d, want 403", rec.Code)
	}
}

func TestSecurity_A01_DefaultMiddlewareWithCSRFTokenFallback(t *testing.T) {
	t.Parallel()

	for _, fallback := range []bool{false, true} {
		app := newTestApp(t, WithRoutes(func(r *router.Router, app *App) error {
			sessionRoutes(r)

			return DefaultMiddlewareWith(app, r, MiddlewareOptions{CSRFTokenFallback: fallback})
		}))

		sessions, err := app.Sessions()
		if err != nil {
			t.Fatal(err)
		}

		h, err := app.Handler()
		if err != nil {
			t.Fatal(err)
		}

		// Old browsers send neither Sec-Fetch-Site nor Origin.
		post := func(cookie string) int {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/form", http.NoBody)
			req.AddCookie(&http.Cookie{Name: cookie, Value: "x"})

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			return rec.Code
		}

		wantSession := http.StatusOK
		if fallback {
			wantSession = http.StatusForbidden
		}

		if got := post(sessions.CookieName()); got != wantSession {
			t.Errorf("fallback %v: POST with the session cookie = %d, want %d", fallback, got, wantSession)
		}
		if got := post("theme"); got != http.StatusOK {
			t.Errorf("fallback %v: POST with an unrelated cookie = %d, want 200", fallback, got)
		}
	}
}

func TestSecurity_A02_DefaultMiddlewareWithRefusesBadOptions(t *testing.T) {
	t.Parallel()

	tests := map[string]MiddlewareOptions{
		"unsafe CSP source": {SecureHeaders: func(cfg *middleware.SecureHeadersConfig) {
			cfg.CSPSources = map[string][]string{"style-src": {"'unsafe-inline'"}}
		}},
		"bad report URI": {SecureHeaders: func(cfg *middleware.SecureHeadersConfig) {
			cfg.ReportURI = "javascript:alert(1)"
		}},
		"bad session bypass":            {SessionBypass: []string{"/{bad"}},
		"empty session bypass":          {SessionBypass: []string{""}},
		"stateless with session bypass": {Stateless: true, SessionBypass: []string{"/webhooks/"}},
		"stateless with token fallback": {Stateless: true, CSRFTokenFallback: true},
		"bad CSRF bypass":               {CSRFBypass: []string{"/{bad"}},
	}

	for name, opts := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := router.New(&web.Services{})
			if err := DefaultMiddlewareWith(newTestApp(t), r, opts); err == nil {
				t.Errorf("DefaultMiddlewareWith(%s) = nil, want an error", name)
			}
		})
	}
}

func TestDefaultMiddlewareWithStatelessSessionFeatures(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, WithRoutes(func(r *router.Router, app *App) error {
		r.Post("/flash", func(c *web.Ctx) error {
			if err := c.Flash("status", "saved"); err != nil {
				return err
			}

			return c.String(http.StatusOK, "flashed")
		})
		r.Get("/token", func(c *web.Ctx) error { return c.String(http.StatusOK, "token="+csrf.Token(c.Context())) })

		return DefaultMiddlewareWith(app, r, MiddlewareOptions{Stateless: true})
	}))

	// Flash fails closed with a 500 instead of panicking or dropping the message.
	if rec := serve(t, app, http.MethodPost, "/flash", false); rec.Code != http.StatusInternalServerError {
		t.Errorf("stateless POST /flash = %d %q, want 500", rec.Code, rec.Body.String())
	}

	if rec := serve(t, app, http.MethodGet, "/token", false); rec.Body.String() != "token=" {
		t.Errorf("stateless csrf.Token = %q, want empty", rec.Body.String())
	}
}

// TestBypassMatchesRouter checks that a bypass pattern skips exactly the
// requests the router serves with the same pattern.
func TestBypassMatchesRouter(t *testing.T) {
	t.Parallel()

	patterns := []string{"/webhooks/", "POST /hooks/{id}", "/exact", "api.example.com/x"}

	r := router.New(&web.Services{})
	for _, p := range patterns {
		r.Handle(p, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(p)) }))
	}

	routed, err := r.Handler()
	if err != nil {
		t.Fatal(err)
	}

	skipped := false
	mw, err := bypass(patterns, func(http.Handler) http.Handler {
		return http.HandlerFunc(func(http.ResponseWriter, *http.Request) { skipped = false })
	})
	if err != nil {
		t.Fatal(err)
	}

	matcher := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { skipped = true }))

	for _, tt := range []struct{ method, target string }{
		{http.MethodGet, "/webhooks/stripe"},
		{http.MethodGet, "/webhooks/a/b"},
		{http.MethodGet, "/webhooks"},
		{http.MethodPost, "/hooks/1"},
		{http.MethodGet, "/hooks/1"},
		{http.MethodPost, "/hooks/1/x"},
		{http.MethodGet, "/exact"},
		{http.MethodGet, "/exact/"},
		{http.MethodGet, "http://api.example.com/x"},
		{http.MethodGet, "http://other.example.com/x"},
		{http.MethodGet, "/x"},
	} {
		req := httptest.NewRequestWithContext(t.Context(), tt.method, tt.target, http.NoBody)

		rec := httptest.NewRecorder()
		routed.ServeHTTP(rec, req)
		wantSkip := rec.Code == http.StatusOK

		matcher.ServeHTTP(httptest.NewRecorder(), req)
		if skipped != wantSkip {
			t.Errorf("%s %s: bypassed = %v, router served the pattern = %v (%d %q)",
				tt.method, tt.target, skipped, wantSkip, rec.Code, rec.Body.String())
		}
	}
}

func TestAPIMiddlewareWith(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, WithRoutes(func(r *router.Router, app *App) error {
		r.Get("/ping", func(c *web.Ctx) error { return c.JSON(http.StatusOK, web.Map{"ok": true}) })

		return APIMiddlewareWith(app, r, APIMiddlewareOptions{
			SecureHeaders: func(cfg *middleware.SecureHeadersConfig) { cfg.ReportURI = "/csp-reports" },
		})
	}))

	rec := serve(t, app, http.MethodGet, "/ping", false)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /ping = %d, want 200", rec.Code)
	}

	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "report-uri /csp-reports") ||
		!strings.Contains(csp, "default-src 'self'") {
		t.Errorf("CSP = %q, want the defaults plus the report URI", csp)
	}

	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
}

func TestSecurity_A02_APIMiddlewareWithRefusesBadOptions(t *testing.T) {
	t.Parallel()

	tests := map[string]APIMiddlewareOptions{
		"unsafe CSP source": {SecureHeaders: func(cfg *middleware.SecureHeadersConfig) {
			cfg.CSPSources = map[string][]string{"script-src": {"'unsafe-eval'"}}
		}},
		"bad report URI": {SecureHeaders: func(cfg *middleware.SecureHeadersConfig) {
			cfg.ReportURI = "javascript:alert(1)"
		}},
	}

	for name, opts := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := router.New(&web.Services{})
			if err := APIMiddlewareWith(newTestApp(t), r, opts); err == nil {
				t.Errorf("APIMiddlewareWith(%s) = nil, want an error", name)
			}
		})
	}
}

// proxyApp serves the client IP at /ip under the default middleware, with
// env added to the test configuration.
func proxyApp(t *testing.T, env map[string]string) *App {
	t.Helper()

	app, err := New(WithSource(testSource(env)), WithLogger(discardLogger()),
		WithRoutes(func(r *router.Router, app *App) error {
			r.Get("/ip", func(c *web.Ctx) error { return c.String(http.StatusOK, c.IP()) })
			return DefaultMiddleware(app, r)
		}))
	if err != nil {
		t.Fatal(err)
	}

	return app
}

func proxyReq(t *testing.T, app *App, peer string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	h, err := app.Handler()
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/ip", http.NoBody)
	req.RemoteAddr = peer
	for k, v := range header {
		req.Header.Set(k, v)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

func TestSecurity_A01_TrustedProxiesFromEnv(t *testing.T) {
	t.Parallel()

	spoof := map[string]string{"X-Forwarded-For": "203.0.113.9"}

	tests := []struct {
		name    string
		proxies string
		peer    string
		want    string
	}{
		// Default: nobody is trusted, so a spoofed header is ignored.
		{name: "unset trusts nobody", peer: "10.1.2.3:4000", want: "10.1.2.3"},
		{name: "cidr from trusted peer", proxies: "10.0.0.0/8", peer: "10.1.2.3:4000", want: "203.0.113.9"},
		{name: "untrusted peer is ignored", proxies: "10.0.0.0/8", peer: "198.51.100.7:4000", want: "198.51.100.7"},
		{name: "bare ip and spaces", proxies: " 192.0.2.1 , 10.0.0.0/8", peer: "192.0.2.1:4000", want: "203.0.113.9"},
		{name: "bare ip is exact", proxies: "192.0.2.1", peer: "192.0.2.2:4000", want: "192.0.2.2"},
		{name: "ipv6 cidr", proxies: "fd00::/8", peer: "[fd00::1]:4000", want: "203.0.113.9"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			app := proxyApp(t, map[string]string{"TRUSTED_PROXIES": tt.proxies})

			rec := proxyReq(t, app, tt.peer, spoof)
			if got := rec.Body.String(); got != tt.want {
				t.Fatalf("IP() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSecurity_A02_TrustedProxiesMisconfigurationRefusesBoot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{name: "bad cidr", env: map[string]string{"TRUSTED_PROXIES": "10.0.0.0/33"}, want: "TRUSTED_PROXIES"},
		{
			name: "hostname",
			env:  map[string]string{"TRUSTED_PROXIES": "10.0.0.0/8,proxy.internal"},
			want: "proxy.internal",
		},
		{
			name: "header without proxies",
			env:  map[string]string{"CLIENT_IP_HEADER": "X-Real-IP"},
			want: "CLIENT_IP_HEADER requires TRUSTED_PROXIES",
		},
		{
			name: "invalid header name",
			env:  map[string]string{"TRUSTED_PROXIES": "10.0.0.0/8", "CLIENT_IP_HEADER": "X Real IP"},
			want: "CLIENT_IP_HEADER",
		},
		{
			name: "list header",
			env:  map[string]string{"TRUSTED_PROXIES": "10.0.0.0/8", "CLIENT_IP_HEADER": "x-forwarded-for"},
			want: "CLIENT_IP_HEADER",
		},
		{
			name: "forced hsts without https url",
			env:  map[string]string{"HSTS_FORCE": "true", "APP_URL": "http://example.com"},
			want: "HSTS_FORCE requires an https APP_URL",
		},
		{
			name: "ipv6 prefix zero",
			env:  map[string]string{"RATE_LIMIT_IPV6_PREFIX": "0"},
			want: "RATE_LIMIT_IPV6_PREFIX",
		},
		{
			name: "ipv6 prefix too long",
			env:  map[string]string{"RATE_LIMIT_IPV6_PREFIX": "129"},
			want: "RATE_LIMIT_IPV6_PREFIX",
		},
		{
			name: "ipv6 prefix not a number",
			env:  map[string]string{"RATE_LIMIT_IPV6_PREFIX": "/64"},
			want: "RATE_LIMIT_IPV6_PREFIX",
		},
		{
			name: "header limit too small",
			env:  map[string]string{"HTTP_MAX_HEADER_BYTES": "1024"},
			want: "HTTP_MAX_HEADER_BYTES",
		},
		{
			name: "header limit above go's default",
			env:  map[string]string{"HTTP_MAX_HEADER_BYTES": "2097152"},
			want: "HTTP_MAX_HEADER_BYTES",
		},
		{
			name: "header limit not a number",
			env:  map[string]string{"HTTP_MAX_HEADER_BYTES": "64KiB"},
			want: "HTTP_MAX_HEADER_BYTES",
		},
		// X-Forwarded-Proto from any peer would let clients claim HTTPS.
		{
			name: "proto without proxies",
			env:  map[string]string{"TRUST_FORWARDED_PROTO": "true"},
			want: "TRUST_FORWARDED_PROTO requires TRUSTED_PROXIES",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := New(WithSource(testSource(tt.env)), WithLogger(discardLogger()))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("New() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestSecurity_A06_RateLimitIPv6PrefixFromEnv(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		env    map[string]string
		peer   string
		header string // X-Real-IP; default 2001:db8:5:6:7::8
		want   string
	}{
		{name: "default /64", peer: "[2001:db8:1:2::9]:1", want: "ip:2001:db8:1:2::/64"},
		{
			name: "env /56",
			env:  map[string]string{"RATE_LIMIT_IPV6_PREFIX": "56"},
			peer: "[2001:db8:1:2::9]:1",
			want: "ip:2001:db8:1::/56",
		},
		{
			name: "ipv4 unchanged",
			env:  map[string]string{"RATE_LIMIT_IPV6_PREFIX": "56"},
			peer: "198.51.100.7:1",
			want: "ip:198.51.100.7",
		},
		{
			name: "proxy header ipv6",
			env:  map[string]string{"TRUSTED_PROXIES": "100.64.0.0/24", "CLIENT_IP_HEADER": "X-Real-IP"},
			peer: "100.64.0.3:1",
			want: "ip:2001:db8:5:6::/64",
		},
		{
			name:   "proxy header ipv4-mapped",
			env:    map[string]string{"TRUSTED_PROXIES": "100.64.0.0/24", "CLIENT_IP_HEADER": "X-Real-IP"},
			peer:   "100.64.0.3:1",
			header: "::ffff:203.0.113.9",
			want:   "ip:203.0.113.9",
		},
		{
			name: "env /1",
			env:  map[string]string{"RATE_LIMIT_IPV6_PREFIX": "1"},
			peer: "[2001:db8:1:2::9]:1",
			want: "ip:::/1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			app, err := New(WithSource(testSource(tt.env)), WithLogger(discardLogger()),
				WithRoutes(func(r *router.Router, _ *App) error {
					r.Get("/ip", func(c *web.Ctx) error { return c.String(http.StatusOK, ratelimit.ByIP(c.Request())) })
					return nil
				}))
			if err != nil {
				t.Fatal(err)
			}

			header := tt.header
			if header == "" {
				header = "2001:db8:5:6:7::8"
			}

			rec := proxyReq(t, app, tt.peer, map[string]string{"X-Real-IP": header})
			if got := rec.Body.String(); got != tt.want {
				t.Fatalf("ByIP = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSecurity_A09_WarnsOnceOnProxyHeadersWithoutTrustedProxies(t *testing.T) {
	t.Parallel()

	const warning = "every client shares one rate-limit bucket"

	tests := []struct {
		name   string
		env    map[string]string
		peer   string
		header string
		ip     string // c.IP(); default the peer host
		want   int    // warnings after three requests
	}{
		{name: "railway edge", peer: "100.64.0.7:4000", header: "X-Real-IP", want: 1},
		{name: "private peer xff", peer: "10.1.2.3:4000", header: "X-Forwarded-For", want: 1},
		{name: "loopback peer", peer: "127.0.0.1:4000", header: "X-Forwarded-For", want: 1},
		{name: "ula peer", peer: "[fd00::1]:4000", header: "X-Real-IP", want: 1},
		{name: "public peer", peer: "198.51.100.7:4000", header: "X-Forwarded-For"},
		{name: "no header", peer: "100.64.0.7:4000"},
		{
			name:   "proxies set",
			env:    map[string]string{"TRUSTED_PROXIES": "100.64.0.0/24", "CLIENT_IP_HEADER": "X-Real-IP"},
			peer:   "100.64.0.7:4000",
			header: "X-Real-IP",
			ip:     "203.0.113.9",
		},
		{
			name:   "not production",
			env:    map[string]string{"APP_ENV": "test"},
			peer:   "100.64.0.7:4000",
			header: "X-Real-IP",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			env := map[string]string{"APP_ENV": "production", "APP_URL": "https://example.com"}
			maps.Copy(env, tt.env)

			var buf bytes.Buffer

			app, err := New(WithSource(testSource(env)), WithLogger(slog.New(slog.NewTextHandler(&buf, nil))),
				WithRoutes(func(r *router.Router, _ *App) error {
					r.Get("/ip", func(c *web.Ctx) error { return c.String(http.StatusOK, c.IP()) })
					return nil
				}))
			if err != nil {
				t.Fatal(err)
			}

			host, _, _ := net.SplitHostPort(tt.peer)

			for range 3 {
				header := map[string]string{}
				if tt.header != "" {
					header[tt.header] = "203.0.113.9"
				}

				// Without TRUSTED_PROXIES the header is never trusted, warning or not.
				want := host
				if tt.ip != "" {
					want = tt.ip
				}

				if got := proxyReq(t, app, tt.peer, header).Body.String(); got != want {
					t.Fatalf("IP() = %q, want %q", got, want)
				}
			}

			if got := strings.Count(buf.String(), warning); got != tt.want {
				t.Fatalf("warnings = %d, want %d; log %q", got, tt.want, buf.String())
			}
		})
	}
}

func TestSecurity_A02_TrustForwardedProtoOnlyFromTrustedProxies(t *testing.T) {
	t.Parallel()

	https := map[string]string{"X-Forwarded-Proto": "https"}

	tests := []struct {
		name     string
		env      map[string]string
		peer     string
		wantHSTS bool
	}{
		{name: "off by default", env: map[string]string{"TRUSTED_PROXIES": "10.0.0.0/8"}, peer: "10.0.0.1:1"},
		{
			name:     "trusted proxy",
			env:      map[string]string{"TRUSTED_PROXIES": "10.0.0.0/8", "TRUST_FORWARDED_PROTO": "true"},
			peer:     "10.0.0.1:1",
			wantHSTS: true,
		},
		{
			name: "spoofed by a client",
			env:  map[string]string{"TRUSTED_PROXIES": "10.0.0.0/8", "TRUST_FORWARDED_PROTO": "true"},
			peer: "198.51.100.7:1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			app := proxyApp(t, tt.env)

			rec := proxyReq(t, app, tt.peer, https)
			if got := rec.Header().Get("Strict-Transport-Security") != ""; got != tt.wantHSTS {
				t.Fatalf("HSTS sent = %v, want %v", got, tt.wantHSTS)
			}

			// web.Ctx.IsHTTPS (Inertia's X-Inertia-Location) follows the same setting.
			if got, want := app.Router.Services().TrustForwardedProto, tt.env["TRUST_FORWARDED_PROTO"] == "true"; got != want {
				t.Errorf("Services().TrustForwardedProto = %v, want %v", got, want)
			}
		})
	}
}

func TestSecurity_A02_HSTSForceFromEnv(t *testing.T) {
	t.Parallel()

	prod := map[string]string{"APP_ENV": "production", "APP_URL": "https://example.com"}

	tests := []struct {
		name     string
		env      map[string]string
		wantHSTS bool
	}{
		// Plain HTTP from the proxy, no trusted range: no HSTS by default.
		{name: "off by default", env: prod},
		{name: "forced in production", env: with(prod, "HSTS_FORCE", "true"), wantHSTS: true},
		{
			name: "not applied outside production",
			env:  map[string]string{"APP_URL": "https://example.com", "HSTS_FORCE": "true"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := proxyReq(t, proxyApp(t, tt.env), "198.51.100.7:1", nil)
			if got := rec.Header().Get("Strict-Transport-Security") != ""; got != tt.wantHSTS {
				t.Fatalf("HSTS sent = %v, want %v", got, tt.wantHSTS)
			}
		})
	}
}

func TestHSTSForceWarnsOutsideProduction(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		env  string
		warn bool
	}{
		{env: "test", warn: true},
		{env: "production"},
	} {
		t.Run(tt.env, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			env := map[string]string{"APP_ENV": tt.env, "APP_URL": "https://example.com", "HSTS_FORCE": "true"}
			if _, err := New(
				WithSource(testSource(env)),
				WithLogger(slog.New(slog.NewTextHandler(&buf, nil))),
			); err != nil {
				t.Fatal(err)
			}

			if got := strings.Contains(buf.String(), "level=WARN") &&
				strings.Contains(buf.String(), "HSTS_FORCE"); got != tt.warn {
				t.Fatalf("warned = %v, want %v; log %q", got, tt.warn, buf.String())
			}
		})
	}
}

func with(m map[string]string, k, v string) map[string]string {
	out := maps.Clone(m)
	out[k] = v

	return out
}

func TestAddrJoinsHostAndPort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		host string
		want string
	}{
		{host: "localhost", want: "localhost:8080"},
		{host: "0.0.0.0", want: "0.0.0.0:8080"},
		// IPv6 hosts need brackets, or "::" becomes the invalid ":::8080".
		{host: "::", want: "[::]:8080"},
		{host: "[::]", want: "[::]:8080"},
		{host: "fd12::1", want: "[fd12::1]:8080"},
	}

	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			t.Parallel()

			app := newTestApp(t, WithSource(testSource(map[string]string{"HOST": tt.host, "PORT": "8080"})))
			if got := app.Addr(); got != tt.want {
				t.Fatalf("Addr() = %q, want %q", got, tt.want)
			}

			if _, _, err := net.SplitHostPort(app.Addr()); err != nil {
				t.Fatalf("Addr() %q is not host:port: %v", app.Addr(), err)
			}
		})
	}
}

func TestSecurity_A01_ClientIPHeaderFromEnv(t *testing.T) {
	t.Parallel()

	headers := map[string]string{"X-Real-IP": "203.0.113.9", "X-Forwarded-For": "198.51.100.99"}

	tests := []struct {
		name string
		env  map[string]string
		peer string
		want string
	}{
		{
			name: "trusted proxy",
			env:  map[string]string{"TRUSTED_PROXIES": "10.0.0.0/8", "CLIENT_IP_HEADER": "x-real-ip"},
			peer: "10.1.2.3:4000",
			want: "203.0.113.9",
		},
		{
			name: "spoofed by untrusted peer",
			env:  map[string]string{"TRUSTED_PROXIES": "10.0.0.0/8", "CLIENT_IP_HEADER": "X-Real-IP"},
			peer: "198.51.100.7:4000",
			want: "198.51.100.7",
		},
		{name: "unset ignores the header", peer: "10.1.2.3:4000", want: "10.1.2.3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := proxyReq(t, proxyApp(t, tt.env), tt.peer, headers)
			if got := rec.Body.String(); got != tt.want {
				t.Fatalf("IP() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTrustedProxiesWarnsOnCatchAll(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		proxies string
		warn    bool
	}{
		{proxies: "0.0.0.0/0", warn: true},
		{proxies: "10.0.0.0/8, ::/0", warn: true},
		{proxies: "10.0.0.0/8"},
	} {
		t.Run(tt.proxies, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			_, err := New(WithSource(testSource(map[string]string{"TRUSTED_PROXIES": tt.proxies})),
				WithLogger(slog.New(slog.NewTextHandler(&buf, nil))))
			if err != nil {
				t.Fatal(err)
			}

			if got := strings.Contains(buf.String(), "level=WARN") &&
				strings.Contains(buf.String(), "TRUSTED_PROXIES"); got != tt.warn {
				t.Fatalf("warned = %v, want %v; log %q", got, tt.warn, buf.String())
			}
		})
	}
}

func TestWithTranslations(t *testing.T) {
	t.Parallel()

	lang := fstest.MapFS{
		"en.json": {Data: []byte(`{"hello": "Hello"}`)},
		"pt.json": {Data: []byte(`{"hello": "Olá"}`)},
	}
	views := fstest.MapFS{"home.html": {Data: []byte(`{{/* layout: none */}}{{t "hello"}}`)}}

	routes := WithRoutes(func(r *router.Router, app *App) error {
		r.Get("/", func(c *web.Ctx) error { return c.Render("home", nil) })
		r.Get("/go", func(c *web.Ctx) error { return c.String(http.StatusOK, i18n.T(c.Context(), "hello")) })

		return DefaultMiddleware(app, r)
	})

	tests := []struct {
		name     string
		env      map[string]string
		target   string
		accept   string
		want     string
		wantLang string
	}{
		{"default", nil, "/", "", "Hello", "en"},
		{"accept-language", nil, "/", "pt-BR,en;q=0.5", "Olá", "pt"},
		{"query in handler", nil, "/go?lang=pt", "", "Olá", "pt"},
		{"APP_LOCALE", map[string]string{"APP_LOCALE": "pt"}, "/", "", "Olá", "pt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			app, err := New(WithSource(testSource(tt.env)), WithLogger(discardLogger()),
				WithTranslations(lang), WithViews(views), routes)
			if err != nil {
				t.Fatal(err)
			}

			if app.I18n == nil {
				t.Fatal("App.I18n = nil, want the bundle")
			}

			h, err := app.Handler()
			if err != nil {
				t.Fatal(err)
			}

			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.target, http.NoBody)
			if tt.accept != "" {
				r.Header.Set("Accept-Language", tt.accept)
			}

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)

			if got := rec.Body.String(); got != tt.want {
				t.Errorf("GET %s body = %q, want %q", tt.target, got, tt.want)
			}

			if got := rec.Header().Get("Content-Language"); got != tt.wantLang {
				t.Errorf("GET %s Content-Language = %q, want %q", tt.target, got, tt.wantLang)
			}
		})
	}
}

func TestWithTranslations_BootErrors(t *testing.T) {
	t.Parallel()

	for name, tt := range map[string]struct {
		lang fstest.MapFS
		env  map[string]string
	}{
		"broken catalog":     {fstest.MapFS{"en.json": {Data: []byte(`{`)}}, nil},
		"invalid APP_LOCALE": {fstest.MapFS{}, map[string]string{"APP_LOCALE": "../en"}},
	} {
		if _, err := New(
			WithSource(testSource(tt.env)),
			WithLogger(discardLogger()),
			WithTranslations(tt.lang),
		); err == nil {
			t.Errorf("%s: New() error = nil, want error", name)
		}
	}
}

// The framework's built-in messages (i18n.Bundle.AddBuiltin) translate
// keyed errors in the request locale, under the app's own catalog.
func TestWithTranslations_BuiltinMessages(t *testing.T) {
	t.Parallel()

	lang := fstest.MapFS{
		"en.json": {Data: []byte(`{"auth": {"unverified": "Please verify your email first."}}`)},
		"pt.json": {Data: []byte(`{}`)},
	}

	routes := WithRoutes(func(r *router.Router, app *App) error {
		r.Get("/", func(*web.Ctx) error {
			return errs.NewKey(http.StatusForbidden, "auth.unverified", "Your email address is not verified.", nil)
		})

		return DefaultMiddleware(app, r)
	})

	app, err := New(WithSource(testSource(nil)), WithLogger(discardLogger()), WithTranslations(lang), routes)
	if err != nil {
		t.Fatal(err)
	}

	h, err := app.Handler()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct{ lang, want string }{
		{"pt-BR", "Seu endereço de e-mail não foi verificado."},
		{"en", "Please verify your email first."},
	}
	for _, tt := range tests {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
		r.Header.Set("Accept", "application/json")
		r.Header.Set("Accept-Language", tt.lang)

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)

		if !strings.Contains(rec.Body.String(), tt.want) {
			t.Errorf("GET / in %s = %d %s, want detail %q", tt.lang, rec.Code, rec.Body.String(), tt.want)
		}
	}
}

func TestDefaultMiddlewareWithI18n(t *testing.T) {
	t.Parallel()

	lang := fstest.MapFS{
		"en.json": {Data: []byte(`{"hello": "Hello"}`)},
		"pt.json": {Data: []byte(`{"hello": "Olá"}`)},
		"ru.json": {Data: []byte(`{"hello": "Привет"}`)},
	}

	app := newTestApp(t, WithTranslations(lang), WithRoutes(func(r *router.Router, app *App) error {
		r.Get("/", func(c *web.Ctx) error { return c.String(http.StatusOK, i18n.T(c.Context(), "hello")) })

		return DefaultMiddlewareWith(app, r, MiddlewareOptions{I18n: &i18n.MiddlewareConfig{
			Order:   []i18n.Source{i18n.SourceQuery, i18n.SourceHeader},
			Locales: []string{"en", "pt"},
		}})
	}))

	h, err := app.Handler()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name, target, cookie, accept, want string
	}{
		{"cookie source off", "/", "pt", "", "Hello"},
		{"query", "/?lang=pt", "", "", "Olá"},
		{"disallowed query falls to header", "/?lang=ru", "", "pt", "Olá"},
		{"disallowed header", "/", "", "ru", "Hello"},
	}

	for _, tt := range tests {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.target, http.NoBody)
		if tt.cookie != "" {
			r.AddCookie(&http.Cookie{Name: "lang", Value: tt.cookie})
		}

		if tt.accept != "" {
			r.Header.Set("Accept-Language", tt.accept)
		}

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)

		if got := rec.Body.String(); got != tt.want {
			t.Errorf("%s: GET %s body = %q, want %q", tt.name, tt.target, got, tt.want)
		}
	}
}

func TestDefaultMiddlewareWithI18n_BootErrors(t *testing.T) {
	t.Parallel()

	lang := fstest.MapFS{"en.json": {Data: []byte(`{"hello": "Hello"}`)}}

	tests := map[string]struct {
		opts []Option
		cfg  i18n.MiddlewareConfig
	}{
		"without translations": {nil, i18n.MiddlewareConfig{}},
		"unknown source":       {[]Option{WithTranslations(lang)}, i18n.MiddlewareConfig{Order: []i18n.Source{"ip"}}},
		"locale without catalog": {
			[]Option{WithTranslations(lang)},
			i18n.MiddlewareConfig{Locales: []string{"en", "de"}},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := router.New(&web.Services{})
			if err := DefaultMiddlewareWith(
				newTestApp(t, tt.opts...),
				r,
				MiddlewareOptions{I18n: &tt.cfg},
			); err == nil {
				t.Errorf("DefaultMiddlewareWith(%s) = nil, want an error", name)
			}
		})
	}
}

func TestAPIMiddlewareWithI18n(t *testing.T) {
	t.Parallel()

	lang := fstest.MapFS{
		"en.json": {Data: []byte(`{"hello": "Hello"}`)},
		"pt.json": {Data: []byte(`{"hello": "Olá", "validation": {"too_small": {"string": "Curto demais."}}}`)},
	}

	querySchema := z.Struct[struct {
		Q string `json:"q"`
	}](z.Shape{"q": z.String().Min(3)})

	tests := []struct {
		name     string
		opts     APIMiddlewareOptions
		target   string
		cookie   string
		accept   string
		wantCode int
		wantBody string
		wantLang string
	}{
		{"query", APIMiddlewareOptions{}, "/?lang=pt&q=abc", "", "", 200, "Olá", "pt"},
		{"header", APIMiddlewareOptions{}, "/?q=abc", "", "pt", 200, "Olá", "pt"},
		{"cookie ignored", APIMiddlewareOptions{}, "/?q=abc", "pt", "", 200, "Hello", "en"},
		{"localized 422", APIMiddlewareOptions{}, "/?q=a", "", "pt", 422, "Curto demais.", "pt"},
		{
			"custom order",
			APIMiddlewareOptions{I18n: &i18n.MiddlewareConfig{Order: []i18n.Source{i18n.SourceCookie}}},
			"/?lang=en&q=abc", "pt", "", 200, "Olá", "pt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			app := newTestApp(t, WithTranslations(lang), WithRoutes(func(r *router.Router, app *App) error {
				r.Get("/", func(c *web.Ctx) error {
					if _, err := web.ParseQuery(c, querySchema); err != nil {
						return err
					}

					return c.String(http.StatusOK, i18n.T(c.Context(), "hello"))
				})

				return APIMiddlewareWith(app, r, tt.opts)
			}))

			h, err := app.Handler()
			if err != nil {
				t.Fatal(err)
			}

			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.target, http.NoBody)
			r.Header.Set("Accept", "application/json")

			if tt.cookie != "" {
				r.AddCookie(&http.Cookie{Name: "lang", Value: tt.cookie})
			}

			if tt.accept != "" {
				r.Header.Set("Accept-Language", tt.accept)
			}

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)

			if rec.Code != tt.wantCode || !strings.Contains(rec.Body.String(), tt.wantBody) ||
				rec.Header().Get("Content-Language") != tt.wantLang {
				t.Errorf(
					"GET %s = %d %q (Content-Language %q), want %d containing %q (%q)",
					tt.target,
					rec.Code,
					rec.Body.String(),
					rec.Header().Get("Content-Language"),
					tt.wantCode,
					tt.wantBody,
					tt.wantLang,
				)
			}
		})
	}
}

func TestAPIMiddlewareWithI18n_BootErrors(t *testing.T) {
	t.Parallel()

	lang := fstest.MapFS{"en.json": {Data: []byte(`{"hello": "Hello"}`)}}

	tests := map[string]struct {
		opts []Option
		cfg  i18n.MiddlewareConfig
	}{
		"without translations": {nil, i18n.MiddlewareConfig{}},
		"locales without default": {
			[]Option{WithTranslations(lang)},
			i18n.MiddlewareConfig{Locales: []string{"pt"}},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := router.New(&web.Services{})
			if err := APIMiddlewareWith(newTestApp(t, tt.opts...), r, APIMiddlewareOptions{I18n: &tt.cfg}); err == nil {
				t.Errorf("APIMiddlewareWith(%s) = nil, want an error", name)
			}
		})
	}
}

// TestMain lets the test binary act as a fake "node" for ssr:start: with
// VUKEM_SSR_HELPER set it appends a line to that file and exits with
// VUKEM_SSR_EXIT.
func TestMain(m *testing.M) {
	if log := os.Getenv("VUKEM_SSR_HELPER"); log != "" {
		f, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = f.WriteString(strings.Join(os.Args[1:], " ") + "\n")
			_ = f.Close()
		}

		if os.Getenv("VUKEM_SSR_EXIT") == "0" {
			os.Exit(0)
		}

		os.Exit(3)
	}

	os.Exit(m.Run())
}

func TestSSRStartRestartsCrashedServer(t *testing.T) { //nolint:paralleltest // sets process environment
	dir := t.TempDir()
	bundle := filepath.Join(dir, "ssr.js")
	runs := filepath.Join(dir, "runs.log")

	if err := os.WriteFile(bundle, []byte("//"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("VUKEM_SSR_HELPER", runs)
	t.Setenv("VUKEM_SSR_EXIT", "3")

	app := newTestApp(t)
	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan error, 1)
	go func() {
		done <- runCommand(ctx, app, "ssr:start",
			[]string{"-bundle", bundle, "-runtime", os.Args[0], "-restart-delay", "10ms"})
	}()

	deadline := time.Now().Add(10 * time.Second)
	for {
		b, _ := os.ReadFile(runs)
		if strings.Count(string(b), bundle) >= 3 {
			break
		}

		if time.Now().After(deadline) {
			t.Fatalf("server not restarted, runs:\n%s", b)
		}

		time.Sleep(10 * time.Millisecond) //nolint:forbidigo // waits on real child processes
	}

	cancel()

	if err := <-done; err != nil {
		t.Errorf("ssr:start after cancel = %v", err)
	}

	t.Setenv("VUKEM_SSR_EXIT", "0")

	if err := runCommand(
		t.Context(),
		app,
		"ssr:start",
		[]string{"-bundle", bundle, "-runtime", os.Args[0]},
	); err != nil {
		t.Errorf("clean exit = %v", err)
	}

	err := runCommand(t.Context(), app, "ssr:start", []string{"-bundle", filepath.Join(dir, "missing.js")})
	if err == nil || !strings.Contains(err.Error(), "npm run build") {
		t.Errorf("missing bundle = %v", err)
	}
}

// VF-027 (HexaScore): `serve -with-ssr` supervises the SSR server next to
// the app, restarting it even after a clean exit, because the instance
// keeps serving pages that need it.
func TestServeWithSSR(t *testing.T) { //nolint:paralleltest // sets process environment
	dir := t.TempDir()
	bundle := filepath.Join(dir, "ssr.js")
	runs := filepath.Join(dir, "runs.log")

	if err := os.WriteFile(bundle, []byte("//"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("VUKEM_SSR_HELPER", runs)
	t.Setenv("VUKEM_SSR_EXIT", "0")

	build := func(opts ...Option) (*App, error) {
		return New(append(opts, WithLogger(discardLogger()), WithoutHTTP())...)
	}
	serve := func(ctx context.Context, args ...string) error {
		return RunMain(ctx, append([]string{"app", "serve"}, args...), testSource(nil).Lookup, build)
	}

	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, "-with-ssr", "-ssr-bundle", bundle, "-ssr-runtime", os.Args[0],
			"-ssr-restart-delay", "10ms")
	}()

	deadline := time.Now().Add(10 * time.Second)
	for {
		b, _ := os.ReadFile(runs)
		if strings.Count(string(b), bundle) >= 3 {
			break
		}

		if time.Now().After(deadline) {
			t.Fatalf("SSR server not supervised by serve, runs:\n%s", b)
		}

		time.Sleep(10 * time.Millisecond) //nolint:forbidigo // waits on real child processes
	}

	cancel()

	if err := <-done; err != nil {
		t.Errorf("serve -with-ssr after cancel = %v", err)
	}

	if err := serve(t.Context(), "-with-ssr", "-ssr-bundle", filepath.Join(dir, "missing.js")); err == nil ||
		!strings.Contains(err.Error(), "not found") {
		t.Errorf("serve -with-ssr with a missing bundle = %v, want a boot error", err)
	}

	if err := serve(t.Context(), "-nope"); !errors.Is(err, errUsage) {
		t.Errorf("serve -nope = %v, want errUsage", err)
	}
}

func TestSSRCheck(t *testing.T) {
	t.Parallel()

	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}

		_, _ = io.WriteString(w, `{"status":"OK"}`)
	}))
	t.Cleanup(healthy.Close)

	var out bytes.Buffer

	app := newTestApp(t, WithStdout(&out))

	if err := runCommand(t.Context(), app, "ssr:check", []string{"-url", healthy.URL}); err != nil ||
		!strings.Contains(out.String(), "healthy") {
		t.Errorf("ssr:check healthy = %v %q", err, out.String())
	}

	if err := runCommand(t.Context(), app, "ssr:check", []string{"-url", "http://127.0.0.1:1"}); err == nil {
		t.Error("ssr:check on a closed port = nil")
	}
}

type pingOut struct {
	Pong bool `json:"pong"`
}

func openAPIRoutes(r *router.Router, app *App) error {
	ServeOpenAPI(app, r, "/openapi.json")
	api.Register(r, "GET /api/ping", api.Op{Name: "ping"},
		func(*web.Ctx, struct{}) (pingOut, error) { return pingOut{Pong: true}, nil })

	return nil
}

func TestServeOpenAPI(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	app := newTestApp(t, WithSource(testSource(map[string]string{
		"APP_NAME": "Shop", "API_VERSION": "3.0.0",
		"OPENAPI_PREFIX": "/api",
	})), WithStdout(&out), WithRoutes(openAPIRoutes))

	rec := doReq(t, app, "/openapi.json")
	if rec.Code != 200 {
		t.Fatalf("GET /openapi.json = %d", rec.Code)
	}

	var doc struct {
		Info struct {
			Title   string `json:"title"`
			Version string `json:"version"`
		} `json:"info"`
		Paths map[string]any `json:"paths"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}

	if doc.Info.Title != "Shop" || doc.Info.Version != "3.0.0" || len(doc.Paths) != 1 || doc.Paths["/api/ping"] == nil {
		t.Fatalf("doc = %+v", doc)
	}

	if err := openAPIExportCommand().Run(t.Context(), app, nil); err != nil ||
		!strings.Contains(out.String(), `"/api/ping"`) {
		t.Fatalf("openapi:export = %v %q", err, out.String())
	}
}

func TestSecurity_A02_OpenAPIOffInProduction(t *testing.T) {
	t.Parallel()

	prod := map[string]string{"APP_ENV": "production"}

	if rec := doReq(
		t,
		newTestApp(t, WithSource(testSource(prod)), WithRoutes(openAPIRoutes)),
		"/openapi.json",
	); rec.Code != 404 {
		t.Fatalf("production /openapi.json = %d, want 404", rec.Code)
	}

	prod["OPENAPI_IN_PRODUCTION"] = "true"
	if rec := doReq(
		t,
		newTestApp(t, WithSource(testSource(prod)), WithRoutes(openAPIRoutes)),
		"/openapi.json",
	); rec.Code != 200 {
		t.Fatalf("opted-in /openapi.json = %d, want 200", rec.Code)
	}
}

func TestCodegenTypesCommand(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	build := func(opts ...Option) (*App, error) {
		return New(append(opts, WithLogger(discardLogger()), WithStdout(&out), WithRoutes(openAPIRoutes))...)
	}

	if err := RunMain(
		t.Context(),
		[]string{"app", "codegen:types", "-zod"},
		testSource(nil).Lookup,
		build,
	); err != nil {
		t.Fatal(err)
	}

	if s := out.String(); !strings.Contains(s, "export type PingResponse = PingOut;") ||
		!strings.Contains(s, "PingOutSchema") {
		t.Fatalf("codegen:types = %q", s)
	}
}

func TestCodegenTypesCommand_CreatesDirectories(t *testing.T) {
	t.Parallel()

	target := filepath.Join(t.TempDir(), "resources", "js", "types.ts")
	build := func(opts ...Option) (*App, error) {
		return New(append(opts, WithLogger(discardLogger()), WithRoutes(openAPIRoutes))...)
	}

	if err := RunMain(
		t.Context(), []string{"app", "codegen:types", "-o", target}, testSource(nil).Lookup, build,
	); err != nil {
		t.Fatal(err)
	}

	if b, err := os.ReadFile(target); err != nil || !strings.Contains(string(b), "PingResponse") {
		t.Fatalf("codegen:types -o %s wrote %q, %v", target, b, err)
	}
}

func TestCodegenTypesCommand_DashWritesToStdout(t *testing.T) {
	t.Chdir(t.TempDir())

	var out bytes.Buffer

	build := func(opts ...Option) (*App, error) {
		return New(append(opts, WithLogger(discardLogger()), WithStdout(&out), WithRoutes(openAPIRoutes))...)
	}

	if err := RunMain(
		t.Context(), []string{"app", "codegen:types", "-o", "-"}, testSource(nil).Lookup, build,
	); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), "PingResponse") {
		t.Fatalf("codegen:types -o - printed %q, want the types on stdout", out.String())
	}

	if _, err := os.Stat("-"); err == nil {
		t.Fatal(`codegen:types -o - created a file named "-"`)
	}
}

func TestServeOpenAPIWithPrefix(t *testing.T) {
	t.Parallel()

	routes := func(r *router.Router, app *App) error {
		ServeOpenAPIWith(app, r, "/api/v1/openapi.json", OpenAPIOptions{PathPrefix: "/api/v1", InProduction: true})
		ServeOpenAPI(app, r, "/openapi.json")
		api.Register(r, "GET /api/v1/ping", api.Op{Name: "ping"},
			func(*web.Ctx, struct{}) (pingOut, error) { return pingOut{Pong: true}, nil })
		api.Register(r, "GET /api/v10/ping", api.Op{Name: "ping10"},
			func(*web.Ctx, struct{}) (pingOut, error) { return pingOut{Pong: true}, nil })
		api.Register(r, "GET /internal/stats", api.Op{Name: "stats"},
			func(*web.Ctx, struct{}) (pingOut, error) { return pingOut{}, nil })

		return nil
	}

	paths := func(t *testing.T, app *App, target string) []string {
		t.Helper()

		rec := doReq(t, app, target)
		if rec.Code != 200 {
			t.Fatalf("GET %s = %d, want 200", target, rec.Code)
		}

		var doc struct {
			Paths map[string]any `json:"paths"`
		}

		if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
			t.Fatal(err)
		}

		return slices.Sorted(maps.Keys(doc.Paths))
	}

	dev := newTestApp(t, WithSource(testSource(nil)), WithRoutes(routes))
	if got := paths(t, dev, "/api/v1/openapi.json"); !slices.Equal(got, []string{"/api/v1/ping"}) {
		t.Errorf("prefixed document paths = %v, want [/api/v1/ping]", got)
	}

	if got := paths(t, dev, "/openapi.json"); !slices.Contains(got, "/internal/stats") {
		t.Errorf("unprefixed document paths = %v, want every route", got)
	}

	prod := newTestApp(t, WithSource(testSource(map[string]string{"APP_ENV": "production"})), WithRoutes(routes))
	if got := paths(t, prod, "/api/v1/openapi.json"); !slices.Equal(got, []string{"/api/v1/ping"}) {
		t.Errorf("opted-in production document paths = %v", got)
	}

	if rec := doReq(t, prod, "/openapi.json"); rec.Code != 404 {
		t.Errorf("production /openapi.json without opt-in = %d, want 404", rec.Code)
	}
}

func TestRateLimitersFromConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  map[string]string
	}{
		{name: "default is memory", env: nil},
		{name: "memory", env: map[string]string{"RATE_LIMIT_STORE": "memory"}},
		{name: "cache", env: map[string]string{"RATE_LIMIT_STORE": "cache", "CACHE_STORE": "database"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.env["CACHE_STORE"] == "database" {
				tt.env["DATABASE_URL"] = "file:" + filepath.ToSlash(filepath.Join(testutil.TempDir(t), "rl.db"))
			}

			app := newTestApp(t, WithSource(testSource(tt.env)))
			t.Cleanup(func() { _ = app.Shutdown(context.Background()) })

			limiters, err := app.RateLimiters()
			if err != nil {
				t.Fatal(err)
			}

			l := limiters("api", 1, time.Minute)
			if res, err := l.Allow(t.Context(), "ip:192.0.2.1"); err != nil || !res.Allowed {
				t.Fatalf("first Allow() = %+v, %v", res, err)
			}

			if res, _ := l.Allow(t.Context(), "ip:192.0.2.1"); res.Allowed {
				t.Fatal("second Allow() allowed, want limit 1")
			}
		})
	}
}

// A typo in RATE_LIMIT_STORE must not quietly fall back to per-process
// limits, which multiply the budget by the number of servers.
func TestSecurity_A02_UnknownRateLimitStoreFails(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, WithSource(testSource(map[string]string{"RATE_LIMIT_STORE": "databse"})))

	if limiters, err := app.RateLimiters(); err == nil || limiters != nil {
		t.Fatalf("RateLimiters() with RATE_LIMIT_STORE=databse = %v, %v, want an error", limiters != nil, err)
	}
}

// RATE_LIMIT_STORE=cache over the per-process memory cache would look
// shared while every server keeps its own budget: refuse it at boot.
func TestSecurity_A02_RateLimitCacheNeedsSharedStore(t *testing.T) {
	t.Parallel()

	env := map[string]string{"RATE_LIMIT_STORE": "cache", "CACHE_STORE": "memory"}

	app := newTestApp(t, WithSource(testSource(env)))
	if limiters, err := app.RateLimiters(); err == nil || limiters != nil {
		t.Fatalf("RateLimiters() with CACHE_STORE=memory = %v, %v, want an error", limiters != nil, err)
	}

	// A store set in code (Redis) is shared.
	app = newTestApp(t, WithSource(testSource(env)), WithCacheStore(sharedStore{cache.NewMemory(0)}))
	if _, err := app.RateLimiters(); err != nil {
		t.Fatalf("RateLimiters() with WithCacheStore = %v", err)
	}

	// The memory store set in code, directly, lazily or wrapped, is not,
	// nor is a store that does not declare cache.Sharer (fail closed).
	for name, opt := range map[string]Option{
		"eager":          WithCacheStore(cache.NewMemory(0)),
		"lazy":           WithCacheStoreFunc(func(*App) (cache.Store, error) { return cache.NewMemory(0), nil }),
		"lazy default":   WithCacheStoreFunc(func(*App) (cache.Store, error) { return nil, ErrUseDefault }),
		"wrapped":        WithCacheStore(wrappedStore{cache.NewMemory(0)}),
		"opaque wrapped": WithCacheStore(opaqueStore{cache.NewMemory(0)}),
	} {
		app = newTestApp(t, WithSource(testSource(env)), opt)
		if limiters, err := app.RateLimiters(); err == nil || limiters != nil {
			t.Errorf("RateLimiters() with a %s memory store = %v, %v, want an error", name, limiters != nil, err)
		}
	}
}

// sharedStore stands for a store every server reaches (Redis).
type sharedStore struct{ *cache.Memory }

func (sharedStore) Shared() bool { return true }

// opaqueStore decorates a store (metrics) without declaring cache.Sharer
// or exposing Unwrap.
type opaqueStore struct{ cache.Store }

// wrappedStore decorates a store (metrics) and exposes it with Unwrap.
type wrappedStore struct{ cache.Store }

func (w wrappedStore) Unwrap() cache.Store { return w.Store }

// Two app instances on one database (two servers) share the budget with
// RATE_LIMIT_STORE=cache and CACHE_STORE=database.
func TestRateLimitersShareTheDatabase(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"RATE_LIMIT_STORE": "cache",
		"CACHE_STORE":      "database",
		"DATABASE_URL":     "file:" + filepath.ToSlash(filepath.Join(testutil.TempDir(t), "rl.db")),
	}

	var allowed int

	for range 2 {
		app, err := New(WithSource(testSource(env)), WithLogger(discardLogger()))
		if err != nil {
			t.Fatal(err)
		}

		t.Cleanup(func() { _ = app.Shutdown(context.Background()) })

		limiters, err := app.RateLimiters()
		if err != nil {
			t.Fatal(err)
		}

		l := limiters("login", 3, time.Hour)

		for range 2 {
			res, err := l.Allow(t.Context(), "ip:192.0.2.1")
			if err != nil {
				t.Fatal(err)
			}

			if res.Allowed {
				allowed++
			}
		}
	}

	// shortcut: a window boundary between the requests would reset the
	// count; a 1h window makes that unlikely enough for a unit test.
	if allowed != 3 {
		t.Fatalf("4 requests across 2 instances, limit 3: %d allowed, want 3", allowed)
	}
}

func TestSecurity_A02_DevtoolsRefusedInProduction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  map[string]string
		opts []Option
	}{
		{name: "option", env: map[string]string{"APP_ENV": "production"}, opts: []Option{WithDevtools()}},
		{name: "env", env: map[string]string{"APP_ENV": "production", "DEVTOOLS": "true"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := New(append(tt.opts, WithSource(testSource(tt.env)), WithLogger(discardLogger()))...)
			if err == nil || !strings.Contains(err.Error(), "devtools") {
				t.Fatalf("New() error = %v, want a devtools boot error", err)
			}
		})
	}

	app, err := New(WithSource(testSource(nil)), WithLogger(discardLogger()))
	if err != nil {
		t.Fatal(err)
	}

	if app.Devtools != nil {
		t.Fatal("devtools enabled by default")
	}

	if rec := devtoolsGet(t, app, "/_vukem/"); rec.Code != http.StatusNotFound {
		t.Errorf("GET /_vukem/ without devtools = %d, want 404", rec.Code)
	}
}

type welcomeMail struct{}

func (welcomeMail) Envelope() mail.Envelope {
	return mail.Envelope{To: []string{"ada@example.com"}, Subject: "Welcome"}
}

func (welcomeMail) Content() mail.Content { return mail.HTML("<h1>Hi</h1>", "Hi") }

type userSignedUp struct{}

type devtoolsJob struct{}

func (devtoolsJob) Kind() string { return "devtools.ping" }

func TestDevtoolsRecordsAppRequest(t *testing.T) {
	t.Parallel()

	app, err := New(
		WithSource(testSource(map[string]string{
			"APP_ENV": "development", "DEVTOOLS": "true", "DATABASE_URL": ":memory:", "MAIL_MAILER": "array",
		})),
		WithLogger(discardLogger()),
		WithJobs(queue.Handle(func(context.Context, devtoolsJob) error { return nil })),
		WithRoutes(func(r *router.Router, app *App) error {
			if err := DefaultMiddleware(app, r); err != nil {
				return err
			}

			r.Get("/users", func(c *web.Ctx) error {
				ctx := c.Context()

				db, err := app.DB()
				if err != nil {
					return err
				}

				for i := range 4 {
					var n int
					if err := db.QueryRowContext(ctx, "SELECT ? + 1", i).Scan(&n); err != nil {
						return err
					}
				}

				app.Log.InfoContext(ctx, "listed users", "count", 4)

				if err := app.Events.Dispatch(ctx, userSignedUp{}); err != nil {
					return err
				}

				q, err := app.Queue()
				if err != nil {
					return err
				}

				if err := q.Dispatch(ctx, devtoolsJob{}); err != nil {
					return err
				}

				m, err := app.Mailer()
				if err != nil {
					return err
				}

				if err := m.Send(ctx, welcomeMail{}); err != nil {
					return err
				}

				return c.String(http.StatusOK, "ok")
			}).Name("users.index")

			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Shutdown(context.Background()) })

	if rec := devtoolsGet(t, app, "/users"); rec.Code != http.StatusOK {
		t.Fatalf("GET /users = %d: %s", rec.Code, rec.Body)
	}

	reqs := app.Devtools.Requests()
	if len(reqs) != 1 {
		t.Fatalf("Requests() = %d, want 1", len(reqs))
	}

	r := reqs[0]
	if r.Route != "users.index" || r.Queries != 4 || len(r.NPlusOne) != 1 || r.RequestID == "" {
		t.Errorf("request = %+v", r)
	}

	pages := map[string]string{
		"/_vukem/":                 "N+1 queries",
		"/_vukem/requests/" + r.ID: "listed users",
		"/_vukem/queries":          "SELECT ? &#43; ?",
		"/_vukem/jobs":             "ping",
		"/_vukem/events":           "vukem.userSignedUp",
		"/_vukem/mails":            "Welcome",
		"/_vukem/logs":             "count=4",
		"/_vukem/routes":           "users.index",
	}

	for path, want := range pages {
		rec := devtoolsGet(t, app, path)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("GET %s = %d, want 200 containing %q", path, rec.Code, want)
		}

		// The app's default CSP (SecureHeaders) applies and the page's
		// only <style> carries its nonce.
		csp := rec.Header().Get("Content-Security-Policy")
		if !strings.Contains(csp, "style-src 'self' 'nonce-") ||
			!strings.Contains(rec.Body.String(), `<style nonce="`) {
			t.Errorf("GET %s CSP = %q", path, csp)
		}
	}

	if got := len(app.Devtools.Requests()); got != 1 {
		t.Errorf("dashboard requests were recorded: %d requests", got)
	}
}

func devtoolsGet(t *testing.T, app *App, path string) *httptest.ResponseRecorder {
	t.Helper()

	h, err := app.Handler()
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody)
	req.RemoteAddr, req.Host = "127.0.0.1:50000", "localhost:3000"

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

// TestSecurity_A02_BuiltinErrorPagesUnderCSP renders every built-in status
// page through DefaultMiddleware and checks it against the response's own
// Content-Security-Policy: no style or event-handler attributes, and every
// <style> and <script> element carries the request nonce (A30).
func TestSecurity_A02_BuiltinErrorPagesUnderCSP(t *testing.T) {
	t.Parallel()

	routes := WithRoutes(func(r *router.Router, app *App) error {
		r.Get("/only-get", func(c *web.Ctx) error { return c.String(200, "ok") })
		r.Get("/forbidden", func(*web.Ctx) error { return errs.ErrForbidden })
		r.Get("/invalid", func(*web.Ctx) error { return errs.NewValidation().Add("title", "required") })
		r.Get("/boom", func(*web.Ctx) error { return errors.New("boom") })

		return DefaultMiddleware(app, r)
	})

	nonceRe := regexp.MustCompile(`'nonce-([^']+)'`)
	inlineAttr := regexp.MustCompile(`(?i)<[^>]+\s(style|on[a-z]+)\s*=`)
	element := regexp.MustCompile(`(?i)<(style|script)\b[^>]*>`)

	for _, env := range []string{"test", "development"} {
		app := newTestApp(t, WithSource(testSource(map[string]string{"APP_ENV": env})), routes)

		h, err := app.Handler()
		if err != nil {
			t.Fatal(err)
		}

		for _, tt := range []struct {
			method, target, hx string
			status             int
		}{
			{http.MethodPost, "/only-get", "", 405},
			{http.MethodGet, "/missing", "", 404},
			{http.MethodGet, "/forbidden", "", 403},
			{http.MethodGet, "/invalid", "", 422},
			{http.MethodGet, "/boom", "", 500},
			{http.MethodGet, "/missing", "true", 404},
		} {
			req := httptest.NewRequestWithContext(t.Context(), tt.method, tt.target, http.NoBody)
			req.Header.Set("Accept", "text/html")

			if tt.hx != "" {
				req.Header.Set("HX-Request", tt.hx)
			}

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			name := env + " " + tt.method + " " + tt.target + " hx=" + tt.hx
			body := rec.Body.String()

			m := nonceRe.FindStringSubmatch(rec.Header().Get("Content-Security-Policy"))
			if rec.Code != tt.status || len(m) < 2 || !strings.Contains(body, "<") {
				t.Errorf("%s = %d CSP %q, want %d with a nonce CSP and HTML", name, rec.Code,
					rec.Header().Get("Content-Security-Policy"), tt.status)

				continue
			}

			if a := inlineAttr.FindString(body); a != "" {
				t.Errorf("%s has an inline attribute CSP blocks: %q", name, a)
			}

			for _, el := range element.FindAllString(body, -1) {
				if !strings.Contains(el, `nonce="`+m[1]+`"`) {
					t.Errorf("%s: %s lacks the request nonce", name, el)
				}
			}
		}
	}
}

// TestSecurity_A02_MaxHeaderBytes checks the request header cap: 64 KiB by
// default, HTTP_MAX_HEADER_BYTES overrides it, and a larger header block
// gets 431 from the server the app runs (A31).
func TestSecurity_A02_MaxHeaderBytes(t *testing.T) {
	t.Parallel()

	if got := newTestApp(t).httpServer(http.NotFoundHandler()).Server.MaxHeaderBytes; got != 64<<10 {
		t.Errorf("default MaxHeaderBytes = %d, want %d", got, 64<<10)
	}

	app := newTestApp(t, WithSource(testSource(map[string]string{"HTTP_MAX_HEADER_BYTES": "16384"})))

	srv := app.httpServer(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }),
	)
	if srv.Server.MaxHeaderBytes != 16384 {
		t.Fatalf("MaxHeaderBytes = %d, want 16384", srv.Server.MaxHeaderBytes)
	}

	ln, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	srv.Listener, srv.Ready = ln, nil
	ctx, cancel := context.WithCancel(t.Context())
	errc := make(chan error, 1)

	go func() { errc <- srv.Run(ctx) }()

	t.Cleanup(func() {
		cancel()

		if err := <-errc; err != nil {
			t.Errorf("Run() = %v", err)
		}
	})

	for _, tt := range []struct {
		size int
		want int
	}{
		{8 << 10, http.StatusNoContent},
		{64 << 10, http.StatusRequestHeaderFieldsTooLarge},
	} {
		req, err := http.NewRequestWithContext(
			t.Context(),
			http.MethodGet,
			"http://"+ln.Addr().String()+"/",
			http.NoBody,
		)
		if err != nil {
			t.Fatal(err)
		}

		req.Header.Set("X-Big", strings.Repeat("a", tt.size))

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET with a %d-byte header: %v", tt.size, err)
		}

		_ = resp.Body.Close()

		if resp.StatusCode != tt.want {
			t.Errorf("GET with a %d-byte header = %d, want %d", tt.size, resp.StatusCode, tt.want)
		}
	}
}

// TestSecurity_A05_MaintenanceResponseCarriesSecurityHeaders checks that
// the 503 of maintenance mode leaves through SecureHeaders like every
// other response, on the web and API stacks (A57).
func TestSecurity_A05_MaintenanceResponseCarriesSecurityHeaders(t *testing.T) {
	t.Parallel()

	file := filepath.Join(testutil.TempDir(t), "down")
	if err := middleware.WriteMaintenance(file, middleware.MaintenanceState{}); err != nil {
		t.Fatal(err)
	}

	app := newTestApp(t)
	maint := maintenanceConfig(app)
	maint.File = file

	h := http.Handler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("the app handler ran during maintenance")
	}))

	stack := serverMiddleware(app, secureHeadersConfig(app), maint)
	for _, mw := range slices.Backward(stack) {
		h = mw(h)
	}

	for _, accept := range []string{"text/html", "application/json"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
		req.Header.Set("Accept", accept)

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("Accept %s: status = %d, want 503", accept, rec.Code)
		}

		for _, name := range []string{
			"Content-Security-Policy", "X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy",
		} {
			if rec.Header().Get(name) == "" {
				t.Errorf("Accept %s: maintenance 503 lacks %s", accept, name)
			}
		}
	}
}

type allowAll struct{}

func (allowAll) Allow(context.Context, string) (ratelimit.Result, error) {
	return ratelimit.Result{Allowed: true, Limit: 1, Remaining: 1}, nil
}

func TestWithRateLimiters(t *testing.T) {
	t.Parallel()

	var names []string

	app := newTestApp(t,
		WithSource(testSource(map[string]string{"RATE_LIMIT_STORE": "cache"})),
		WithRateLimiters(func(name string, _ int, _ time.Duration) ratelimit.Limiter {
			names = append(names, name)
			return allowAll{}
		}),
	)

	limiters, err := app.RateLimiters()
	if err != nil {
		t.Fatalf("RateLimiters() = %v, want the injected factory", err)
	}

	for range 3 {
		if res, err := limiters("login", 1, time.Minute).Allow(t.Context(), "k"); err != nil || !res.Allowed {
			t.Fatalf("Allow() = %+v, %v, want allowed", res, err)
		}
	}

	if diff := cmp.Diff([]string{"login", "login", "login"}, names); diff != "" {
		t.Errorf("factory calls (-want +got):\n%s", diff)
	}

	if _, err := New(WithSource(testSource(nil)), WithoutHTTP(), WithRateLimiters(nil)); err == nil {
		t.Error("New(WithRateLimiters(nil)) = nil error, want an error")
	}
}

// Routes mounted with r.Handle (plain http.Handler) go through the server
// middleware too: they are logged and get the request id like any route.
func TestHandleRoutesAreLogged(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	app := newTestApp(t,
		WithLogger(slog.New(slog.NewTextHandler(&buf, nil))),
		WithRoutes(func(r *router.Router, app *App) error {
			r.Handle("GET /raw", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusTeapot)
			}))

			return DefaultMiddleware(app, r)
		}),
	)

	rec := doReq(t, app, "/raw")
	if rec.Code != http.StatusTeapot || rec.Header().Get("X-Request-ID") == "" {
		t.Fatalf("GET /raw = %d %v, want 418 with a request id", rec.Code, rec.Header())
	}

	if log := buf.String(); !strings.Contains(log, "msg=request") || !strings.Contains(log, "path=/raw") ||
		!strings.Contains(log, "status=418") {
		t.Errorf("log = %q, want a request line for /raw", log)
	}
}

func apiKeyRoutes(r *router.Router, app *App) error {
	ServeOpenAPI(app, r, "/openapi.json")
	api.Register(r, "GET /api/keys", api.Op{Name: "keys.index", Security: []string{"apiKey"}},
		func(*web.Ctx, struct{}) (pingOut, error) { return pingOut{}, nil })

	return nil
}

func TestWithOpenAPI(t *testing.T) {
	t.Parallel()

	src := func() *config.Source {
		return testSource(map[string]string{
			"APP_NAME": "Shop", "API_VERSION": "3.0.0", "OPENAPI_PREFIX": "/api",
		})
	}

	bare := newTestApp(t, WithSource(src()), WithRoutes(apiKeyRoutes))
	if _, err := bare.OpenAPI(); !errors.Is(err, openapi.ErrUnknownSecurityScheme) {
		t.Fatalf("OpenAPI() without WithOpenAPI = %v, want ErrUnknownSecurityScheme", err)
	}

	app := newTestApp(t, WithSource(src()), WithRoutes(apiKeyRoutes), WithOpenAPI(openapi.Info{
		Description: "Shop API",
		SecuritySchemes: map[string]openapi.Schema{
			"apiKey": {"type": "apiKey", "in": "header", "name": "X-API-Key"},
		},
	}))

	doc, err := app.OpenAPI()
	if err != nil {
		t.Fatalf("OpenAPI() with WithOpenAPI = %v", err)
	}

	if doc.Info.Title != "Shop" || doc.Info.Version != "3.0.0" || doc.Info.Description != "Shop API" {
		t.Errorf("info = %+v, want the config title and version with the supplied description", doc.Info)
	}

	if got := doc.Components.SecuritySchemes["apiKey"]["name"]; got != "X-API-Key" {
		t.Errorf("securitySchemes = %v, want the apiKey header scheme", doc.Components.SecuritySchemes)
	}

	if len(doc.Paths) != 1 || doc.Paths["/api/keys"] == nil {
		t.Errorf("paths = %v, want OPENAPI_PREFIX still applied", slices.Sorted(maps.Keys(doc.Paths)))
	}

	// Title, Version and Servers override the configuration when set.
	over := newTestApp(t, WithSource(src()), WithRoutes(apiKeyRoutes), WithOpenAPI(openapi.Info{
		Title:   "Public API",
		Version: "9.9.9",
		Servers: []string{"https://api.example"},
		SecuritySchemes: map[string]openapi.Schema{
			"apiKey": {"type": "apiKey", "in": "header", "name": "X-API-Key"},
		},
	}))

	doc, err = over.OpenAPI()
	if err != nil {
		t.Fatalf("OpenAPI() = %v", err)
	}

	if doc.Info.Title != "Public API" || doc.Info.Version != "9.9.9" ||
		len(doc.Servers) != 1 || doc.Servers[0].URL != "https://api.example" {
		t.Errorf("info = %+v, servers = %+v, want the supplied values", doc.Info, doc.Servers)
	}
}

// A100: the framework's own route names are namespaced, so an app naming a
// route "health" boots. It used to take the bare name for GET /up, and the
// duplicate-name error pointed at the app's route — the one that looked
// wrong — with nothing in the docs or the type system saying the name was
// taken. It was found by starting a container.
func TestFrameworkRouteNamesDoNotCollideWithAnApp(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, WithRoutes(func(r *router.Router, _ *App) error {
		r.Get("/health", func(c *web.Ctx) error { return c.NoContent() }).Name("health")

		return nil
	}))

	if _, err := app.Handler(); err != nil {
		t.Fatalf("an app route named %q must boot: %v", "health", err)
	}

	// The framework's own route keeps a name, under a reserved prefix, so
	// it stays addressable.
	if _, err := app.Router.URL(reservedRouteName("health")); err != nil {
		t.Errorf("the framework's /up route lost its name: %v", err)
	}

	// Every name the framework registers carries the prefix, so a future
	// one cannot reintroduce the collision.
	for _, rt := range app.Router.Routes() {
		if rt.Path == "/health" {
			continue
		}

		if !strings.HasPrefix(rt.Name, reservedRoutePrefix) {
			t.Errorf("framework route %s %s is named %q, want the %q prefix",
				rt.Method, rt.Path, rt.Name, reservedRoutePrefix)
		}
	}
}

// A liveness probe answer must not be cached by an intermediary: a cached
// 200 reports a process that may already be gone. The readiness branch
// already set it.
func TestHealthEndpointIsNotCacheable(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	for _, target := range []string{"/up", "/up?checks=1"} {
		if got := doReq(t, app, target).Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s Cache-Control = %q, want no-store", target, got)
		}
	}
}

// VF-027 (HexaScore): the build version is reported by /up only when the
// app opts in, so the probe does not fingerprint the build by default.
func TestWithVersionOnHealthEndpoints(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts []Option
		want string
	}{
		{"opted in", []Option{WithVersion("v1.4.2+3f9c2ab")}, `"version":"v1.4.2+3f9c2ab"`},
		{"default", nil, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			app := newTestApp(t, tt.opts...)
			if app.Version != strings.TrimSuffix(strings.TrimPrefix(tt.want, `"version":"`), `"`) {
				t.Errorf("App.Version = %q", app.Version)
			}

			for _, target := range []string{"/up", "/up?checks=1"} {
				body := doReq(t, app, target).Body.String()
				if tt.want != "" && !strings.Contains(body, tt.want) || tt.want == "" && strings.Contains(body, "version") {
					t.Errorf("GET %s = %s, want version %q", target, body, tt.want)
				}
			}

			rec := httptest.NewRecorder()
			app.ReadyHandler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/ready", http.NoBody))

			if body := rec.Body.String(); tt.want != "" && !strings.Contains(body, tt.want) {
				t.Errorf("ReadyHandler = %s, want %s", body, tt.want)
			}
		})
	}
}

// A101: the readiness seam existed as a method; WithHealthCheck is the
// build-time form, so an app declares its dependencies next to its
// migrations and providers instead of needing a provider for it.
func TestWithHealthCheck(t *testing.T) {
	t.Parallel()

	app := newTestApp(t,
		WithHealthCheck("bucket", func(context.Context) error { return nil }),
		WithHealthCheck("search", func(context.Context) error { return errors.New("dial tcp 10.0.0.9: refused") }),
	)

	rec := doReq(t, app, "/up?checks=1")

	var body struct {
		Status string            `json:"status"`
		Checks map[string]string `json:"checks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}

	if rec.Code != 503 || body.Checks["bucket"] != "ok" || body.Checks["search"] != "fail" {
		t.Fatalf("/up?checks=1 = %d %+v", rec.Code, body)
	}

	// The failing check's error names a host; it must not reach the client.
	if strings.Contains(rec.Body.String(), "10.0.0.9") {
		t.Errorf("readiness body leaks the dependency error: %s", rec.Body.String())
	}

	// A provider registering the same name wins, so a build-time default
	// can be replaced.
	app.AddHealthCheck("search", func(context.Context) error { return nil })

	if rec := doReq(t, app, "/up?checks=1"); rec.Code != 200 {
		t.Errorf("after replacing the check: /up?checks=1 = %d %s", rec.Code, rec.Body.String())
	}
}
