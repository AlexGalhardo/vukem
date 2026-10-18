package vukem

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/inertia"
	"github.com/AlexGalhardo/vukem/internal/testutil"
	"github.com/AlexGalhardo/vukem/mail"
	"github.com/AlexGalhardo/vukem/queue"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/sse"
	"github.com/AlexGalhardo/vukem/web"
)

var testKey = config.GenerateKey()

func testSource(extra map[string]string) *config.Source {
	m := map[string]string{"APP_KEY": testKey, "APP_ENV": "test"}
	maps.Copy(m, extra)

	return config.MapSource(m)
}

type recorder struct {
	mu  sync.Mutex
	log []string
}

func (r *recorder) add(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.log = append(r.log, s)
}

func (r *recorder) get() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.log)
}

type testProvider struct {
	name  string
	rec   *recorder
	kinds []Kind
	fail  string
}

func (p *testProvider) Register(*App) error {
	p.rec.add("register:" + p.name)
	if p.fail == "register" {
		return errors.New("boom")
	}
	return nil
}

func (p *testProvider) Boot(context.Context, *App) error {
	p.rec.add("boot:" + p.name)
	if p.fail == "boot" {
		return errors.New("boom")
	}
	return nil
}

func (p *testProvider) Shutdown(context.Context, *App) error {
	p.rec.add("shutdown:" + p.name)
	if p.fail == "shutdown" {
		return errors.New("boom")
	}
	return nil
}

type filtered struct {
	testProvider
}

func (f *filtered) Kinds() []Kind { return f.kinds }

func TestProviderLifecycleOrder(t *testing.T) {
	t.Parallel()

	rec := &recorder{}
	a := &testProvider{name: "a", rec: rec}
	b := &testProvider{name: "b", rec: rec}
	webOnly := &filtered{testProvider{name: "web", rec: rec, kinds: []Kind{KindWeb}}}

	app, err := New(WithSource(testSource(nil)), WithKind(KindConsole), WithProviders(a, webOnly, b),
		WithLogger(discardLogger()))
	if err != nil {
		t.Fatal(err)
	}

	app.OnShutdown(func(context.Context) error { rec.add("hook:1"); return nil })
	app.OnShutdown(func(context.Context) error { rec.add("hook:2"); return nil })

	if err := app.Boot(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := app.Boot(t.Context()); err != nil { // idempotent
		t.Fatal(err)
	}
	if err := app.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := app.Shutdown(t.Context()); err != nil { // idempotent
		t.Fatal(err)
	}

	want := []string{"register:a", "register:b", "boot:a", "boot:b", "hook:2", "hook:1", "shutdown:b", "shutdown:a"}
	if got := rec.get(); !slices.Equal(got, want) {
		t.Fatalf("lifecycle = %v, want %v", got, want)
	}
}

func TestProviderErrors(t *testing.T) {
	t.Parallel()

	for _, phase := range []string{"register", "boot", "shutdown"} {
		t.Run(phase, func(t *testing.T) {
			t.Parallel()

			p := &testProvider{name: "x", rec: &recorder{}, fail: phase}
			app, err := New(WithSource(testSource(nil)), WithProviders(p), WithLogger(discardLogger()))
			if phase == "register" {
				if err == nil || !strings.Contains(err.Error(), "register") {
					t.Fatalf("New() error = %v, want register error", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}

			if phase == "boot" {
				err = app.Boot(t.Context())
			} else {
				err = app.Shutdown(t.Context())
			}
			if err == nil || !strings.Contains(err.Error(), phase) {
				t.Fatalf("%s error = %v", phase, err)
			}
		})
	}
}

func TestSecurity_A02_BootRefusesMisconfiguration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  *config.Source
		want string
	}{
		// Outside production a missing key is allowed (TestSecurity_A04_MissingKeyFailsClosed).
		{
			name: "missing key in production",
			src:  config.MapSource(map[string]string{"APP_ENV": "production"}),
			want: "APP_KEY is not set",
		},
		{
			name: "short key",
			src:  config.MapSource(map[string]string{"APP_KEY": "base64:YWJj"}),
			want: "at least 32 bytes",
		},
		{
			name: "debug in production",
			src:  testSource(map[string]string{"APP_ENV": "production", "APP_DEBUG": "true"}),
			want: "APP_DEBUG must be false",
		},
		{name: "bad port", src: testSource(map[string]string{"PORT": "70000"}), want: "out of range"},
		// A typo must not silently drop the production checks (fail closed).
		{name: "unknown env", src: testSource(map[string]string{"APP_ENV": "prod"}), want: "APP_ENV"},
		{name: "env with space", src: testSource(map[string]string{"APP_ENV": "production "}), want: "APP_ENV"},
		{name: "env case", src: testSource(map[string]string{"APP_ENV": "Production"}), want: "APP_ENV"},
		{name: "unparsable value", src: testSource(map[string]string{"PORT": "x"}), want: "PORT"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := New(WithSource(tt.src))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("New() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestNewLoadsFromEnvAndLogger(t *testing.T) {
	t.Parallel()

	getenv := func(k string) (string, bool) {
		v, ok := map[string]string{"APP_KEY": testKey, "APP_ENV": "production", "APP_NAME": "Blog"}[k]
		return v, ok
	}

	var buf bytes.Buffer

	app, err := New(WithEnv(emptyFS{}, getenv), WithLogOutput(&buf))
	if err != nil {
		t.Fatal(err)
	}

	if app.Config.Name != "Blog" || !app.Config.IsProduction() || app.Addr() != "localhost:3000" {
		t.Fatalf("config = %+v", app.Config)
	}

	app.Log.Info("hello")
	if !strings.HasPrefix(buf.String(), "{") {
		t.Fatalf("production logger is not JSON: %q", buf.String())
	}
}

// TestSecurity_A09_RepeatedErrorsAggregated: in production the default
// logger writes a burst of identical errors once, plus a "repeated"
// summary with the count (flushed by Shutdown at the latest).
func TestSecurity_A09_RepeatedErrorsAggregated(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		env         map[string]string
		wantRecords int
		wantSummary bool
	}{
		{
			name:        "production default",
			env:         map[string]string{"APP_ENV": "production"},
			wantRecords: 1,
			wantSummary: true,
		},
		{name: "development default", env: map[string]string{"APP_ENV": "development"}, wantRecords: 3},
		{
			name:        "opt in outside production",
			env:         map[string]string{"LOG_DEDUP_WINDOW": "30s"},
			wantRecords: 1,
			wantSummary: true,
		},
		{
			name:        "disabled in production",
			env:         map[string]string{"APP_ENV": "production", "LOG_DEDUP_WINDOW": "0"},
			wantRecords: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			app, err := New(WithSource(testSource(tt.env)), WithLogOutput(&buf), WithoutHTTP())
			if err != nil {
				t.Fatal(err)
			}

			for range 3 {
				app.Log.Error("db down", "error", "timeout")
			}

			if err := app.Shutdown(t.Context()); err != nil {
				t.Fatalf("Shutdown() = %v", err)
			}

			out := buf.String()
			if got := strings.Count(out, "db down"); got != tt.wantRecords+boolInt(tt.wantSummary) {
				t.Errorf(
					"records mentioning the error = %d, want %d\n%s",
					got,
					tt.wantRecords+boolInt(tt.wantSummary),
					out,
				)
			}

			if got := strings.Contains(out, "repeated") && strings.Contains(out, "count"); got != tt.wantSummary {
				t.Errorf("summary written = %v, want %v\n%s", got, tt.wantSummary, out)
			}
		})
	}
}

func boolInt(b bool) int {
	if b {
		return 1
	}

	return 0
}

func TestNewRejectsBadLogDedupWindow(t *testing.T) {
	t.Parallel()

	for _, v := range []string{"soon", "-1m"} {
		_, err := New(WithSource(testSource(map[string]string{"LOG_DEDUP_WINDOW": v})), WithLogger(discardLogger()))
		if err == nil || !strings.Contains(err.Error(), "LOG_DEDUP_WINDOW") {
			t.Errorf("New(LOG_DEDUP_WINDOW=%q) = %v, want a LOG_DEDUP_WINDOW error", v, err)
		}
	}
}

func TestRunStopsOnCancelInReverseOrder(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		rec := &recorder{}
		p := &testProvider{name: "p", rec: rec}

		app, err := New(WithSource(testSource(nil)), WithProviders(p), WithLogger(discardLogger()), WithoutHTTP(),
			WithRunner("worker", RunnerFunc(func(ctx context.Context) error {
				rec.add("run")
				<-ctx.Done()
				rec.add("stop")
				return ctx.Err()
			})))
		if err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- app.Run(ctx) }()

		synctest.Wait()
		cancel()

		if err := <-done; err != nil {
			t.Fatalf("Run() = %v, want nil on clean shutdown", err)
		}

		want := []string{"register:p", "boot:p", "run", "stop", "shutdown:p"}
		if got := rec.get(); !slices.Equal(got, want) {
			t.Fatalf("order = %v, want %v", got, want)
		}
	})
}

func TestRunReturnsRunnerError(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		stopped := false
		app, err := New(WithSource(testSource(nil)), WithLogger(discardLogger()), WithoutHTTP())
		if err != nil {
			t.Fatal(err)
		}

		app.AddRunner("bad", RunnerFunc(func(context.Context) error {
			time.Sleep(time.Second)
			return errors.New("crash")
		}))
		app.AddRunner("good", RunnerFunc(func(ctx context.Context) error {
			<-ctx.Done()
			stopped = true
			return nil
		}))

		err = app.Run(t.Context())
		if err == nil || !strings.Contains(err.Error(), "runner bad: crash") {
			t.Fatalf("Run() = %v, want runner error", err)
		}
		if !stopped {
			t.Fatal("other runner was not stopped")
		}
	})
}

// A runner that ignores ctx cannot hold shutdown forever: Run waits
// SHUTDOWN_TIMEOUT plus a three-second grace, logs it and shuts the app down.
func TestRunBoundsRunnersIgnoringContext(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		rec := &recorder{}
		release := make(chan struct{})

		var logs bytes.Buffer

		app, err := New(WithSource(testSource(map[string]string{"SHUTDOWN_TIMEOUT": "5s"})),
			WithProviders(&testProvider{name: "p", rec: rec}), WithoutHTTP(),
			WithLogger(slog.New(slog.NewTextHandler(&logs, nil))),
			WithRunner("stuck", RunnerFunc(func(context.Context) error { <-release; return nil })))
		if err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- app.Run(ctx) }()

		synctest.Wait()
		cancel()

		start := time.Now()
		if err := <-done; err != nil {
			t.Fatalf("Run() = %v, want nil", err)
		}

		if waited := time.Since(start); waited != 8*time.Second {
			t.Fatalf("Run waited %v after cancel, want SHUTDOWN_TIMEOUT + 3s (8s)", waited)
		}

		if got := rec.get(); !slices.Contains(got, "shutdown:p") {
			t.Fatalf("providers not shut down: %v", got)
		}

		if !strings.Contains(logs.String(), "runners still running") || !strings.Contains(logs.String(), "stuck") {
			t.Fatalf("log = %q, want the stuck runner named", logs.String())
		}

		close(release)
	})
}

// Slowloris and oversized headers are bounded on every server the app runs.
func TestSecurity_A02_ServerTimeouts(t *testing.T) {
	t.Parallel()

	s := NewHTTPServer(":0", http.NotFoundHandler()).Server

	got := []any{s.ReadHeaderTimeout, s.ReadTimeout, s.WriteTimeout, s.IdleTimeout, s.MaxHeaderBytes}
	want := []any{10 * time.Second, 30 * time.Second, 60 * time.Second, 120 * time.Second, 64 << 10}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("NewHTTPServer() limits mismatch (-want +got):\n%s", diff)
	}
}

func TestHTTPServerGracefulShutdown(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	started := make(chan struct{})

	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		fmt.Fprint(w, "done")
	})

	srv := NewHTTPServer("127.0.0.1:0", h)
	ready := make(chan string, 1)
	srv.Ready = ready

	if srv.Server.ReadHeaderTimeout == 0 || srv.Server.WriteTimeout == 0 || srv.Server.IdleTimeout == 0 ||
		srv.Server.ReadTimeout == 0 || srv.Server.MaxHeaderBytes == 0 {
		t.Fatal("NewHTTPServer() left a zero timeout")
	}

	ctx, cancel := context.WithCancel(t.Context())
	errc := make(chan error, 1)
	go func() { errc <- srv.Run(ctx) }()

	addr := <-ready
	respc := make(chan string, 1)

	go func() {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr+"/", http.NoBody)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			respc <- "error: " + err.Error()
			return
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		respc <- string(b)
	}()

	<-started
	cancel() // shutdown while the request is in flight
	close(release)

	if got := <-respc; got != "done" {
		t.Fatalf("in-flight response = %q, want done", got)
	}
	if err := <-errc; err != nil {
		t.Fatalf("Run() = %v", err)
	}
}

func TestHTTPServerShutdownEndsSSEStreams(t *testing.T) {
	t.Parallel()

	streaming := make(chan struct{})
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = sse.Serve(w, r, func(s *sse.Stream) error {
			close(streaming)
			<-s.Context().Done()

			return nil
		})
	})

	srv := NewHTTPServer("127.0.0.1:0", h)
	srv.ShutdownTimeout = 30 * time.Second
	ready := make(chan string, 1)
	srv.Ready = ready

	ctx, cancel := context.WithCancel(t.Context())
	errc := make(chan error, 1)
	go func() { errc <- srv.Run(ctx) }()

	addr := <-ready

	go func() {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr+"/", http.NoBody)
		if res, err := http.DefaultClient.Do(req); err == nil {
			_, _ = io.Copy(io.Discard, res.Body)
			_ = res.Body.Close()
		}
	}()

	<-streaming

	start := time.Now()
	cancel()

	if err := <-errc; err != nil {
		t.Fatalf("Run() = %v", err)
	}

	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("shutdown took %v with an open stream", d)
	}
}

func TestHTTPServerListenError(t *testing.T) {
	t.Parallel()

	srv := NewHTTPServer("256.0.0.1:99999", http.NotFoundHandler())
	if err := srv.Run(t.Context()); err == nil {
		t.Fatal("Run() with bad address = nil error")
	}
}

// clientProvider opens its client in Boot, like a Mongo or S3 provider.
type clientProvider struct {
	client *string
	rec    *recorder
}

func (p *clientProvider) Register(*App) error { return nil }

func (p *clientProvider) Boot(context.Context, *App) error {
	p.rec.add("boot")
	p.client = new("connected")

	return nil
}

func TestRoutesAfterBootSeeBootedServices(t *testing.T) {
	t.Parallel()

	rec := &recorder{}
	p := &clientProvider{rec: rec}

	app, err := New(WithSource(testSource(nil)), WithLogger(discardLogger()), WithProviders(p),
		WithRoutes(func(*router.Router, *App) error { rec.add("routes"); return nil }),
		WithRoutesAfterBoot(func(r *router.Router, _ *App) error {
			rec.add("routes-after-boot")

			if p.client == nil {
				return errors.New("client is nil")
			}

			client := *p.client
			r.Get("/client", func(c *web.Ctx) error { return c.String(http.StatusOK, client) }).Name("client")

			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	if hasRoute(app, "client") {
		t.Fatal("after-boot route registered before Boot")
	}

	if err := app.Boot(t.Context()); err != nil {
		t.Fatal(err)
	}

	if err := app.Boot(t.Context()); err != nil { // idempotent: routes are added once
		t.Fatal(err)
	}

	if want, got := []string{"routes", "boot", "routes-after-boot"}, rec.get(); !slices.Equal(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}

	h, err := app.Handler()
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/client", nil))

	if rr.Code != http.StatusOK || rr.Body.String() != "connected" {
		t.Fatalf("GET /client = %d %q", rr.Code, rr.Body.String())
	}
}

func TestRoutesAfterBootErrors(t *testing.T) {
	t.Parallel()

	failing := WithRoutesAfterBoot(func(*router.Router, *App) error { return errors.New("boom") })

	app, err := New(WithSource(testSource(nil)), WithLogger(discardLogger()), failing)
	if err != nil {
		t.Fatal(err)
	}

	if err := app.Boot(t.Context()); err == nil || !strings.Contains(err.Error(), "routes after boot: boom") {
		t.Fatalf("Boot() = %v, want routes after boot error", err)
	}

	// A router compiled before Boot (here through Router.Handler) would
	// silently miss the late routes.
	late := WithRoutesAfterBoot(func(r *router.Router, _ *App) error {
		r.Get("/late", func(*web.Ctx) error { return nil })
		return nil
	})

	app, err = New(WithSource(testSource(nil)), WithLogger(discardLogger()), late)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := app.Router.Handler(); err != nil {
		t.Fatal(err)
	}

	if err := app.Boot(t.Context()); !errors.Is(err, ErrRoutesCompiled) {
		t.Fatalf("Boot() after Handler() = %v, want ErrRoutesCompiled", err)
	}
}

func TestRoutesAfterBootInWarmup(t *testing.T) {
	t.Parallel()

	p := &clientProvider{rec: &recorder{}}

	app, err := New(WithSource(testSource(nil)), WithLogger(discardLogger()), WithKind(KindWarmup),
		WithProviders(p),
		WithRoutesAfterBoot(func(r *router.Router, _ *App) error {
			r.Get("/client", func(*web.Ctx) error { return nil }).Name("client")
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	// Warmup never boots, yet codegen:routes needs the full route table.
	if !hasRoute(app, "client") {
		t.Fatal("after-boot route missing in warmup")
	}

	if got := p.rec.get(); len(got) != 0 {
		t.Fatalf("warmup booted providers: %v", got)
	}
}

func hasRoute(app *App, name string) bool {
	return slices.ContainsFunc(app.Router.Routes(), func(r router.RouteInfo) bool { return r.Name == name })
}

func TestBootErrorIsSticky(t *testing.T) {
	t.Parallel()

	p := &testProvider{name: "x", rec: &recorder{}, fail: "boot"}

	app, err := New(WithSource(testSource(nil)), WithLogger(discardLogger()), WithProviders(p))
	if err != nil {
		t.Fatal(err)
	}

	first := app.Boot(t.Context())
	if first == nil {
		t.Fatal("Boot() = nil, want boot error")
	}

	if again := app.Boot(t.Context()); !errors.Is(again, first) {
		t.Fatalf("second Boot() = %v, want %v", again, first)
	}

	if got := p.rec.get(); len(got) != 2 { // register + one boot
		t.Fatalf("provider calls = %v, want Boot once", got)
	}
}

func TestHandlerRefusesPendingRoutesAfterBoot(t *testing.T) {
	t.Parallel()

	app, err := New(WithSource(testSource(nil)), WithLogger(discardLogger()),
		WithRoutesAfterBoot(func(*router.Router, *App) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := app.Handler(); !errors.Is(err, ErrNotBooted) {
		t.Fatalf("Handler() before Boot = %v, want ErrNotBooted", err)
	}

	if app.Router.Compiled() {
		t.Fatal("Handler() compiled the router while routes were pending")
	}

	if err := app.Boot(t.Context()); err != nil {
		t.Fatal(err)
	}

	if _, err := app.Handler(); err != nil {
		t.Fatalf("Handler() after Boot = %v", err)
	}
}

func TestRoutesAfterBootPanicInWarmup(t *testing.T) {
	t.Parallel()

	p := &clientProvider{rec: &recorder{}}

	_, err := New(WithSource(testSource(nil)), WithLogger(discardLogger()), WithKind(KindWarmup),
		WithProviders(p),
		WithRoutesAfterBoot(func(r *router.Router, _ *App) error {
			client := *p.client // nil: warmup never boots
			r.Get("/client", func(c *web.Ctx) error { return c.String(http.StatusOK, client) })

			return nil
		}),
	)
	if err == nil || !strings.Contains(err.Error(), "must not use booted services during warmup") {
		t.Fatalf("New() in warmup = %v, want warmup panic error", err)
	}
}

func TestErrRoutesCompiledHint(t *testing.T) {
	t.Parallel()

	if msg := ErrRoutesCompiled.Error(); !strings.Contains(msg, "call Boot before Handler/Router.URL") {
		t.Fatalf("ErrRoutesCompiled = %q, want a hint", msg)
	}
}

type hookLog struct {
	mu   sync.Mutex
	name string
	log  []string
}

func (h *hookLog) add(s string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.log = append(h.log, h.name+" "+s)
}

func (h *hookLog) ObserveQuery(_ context.Context, q database.Query) { h.add("query " + q.SQL) }

func (h *hookLog) ObserveJob(_ context.Context, e queue.JobEvent) {
	h.add("job " + e.Kind + " " + string(e.Status))
}

type hookTransport struct {
	next mail.Transport
	log  *hookLog
}

func (t hookTransport) Send(ctx context.Context, msg *mail.Message) error {
	t.log.add("mail " + msg.Subject)
	return t.next.Send(ctx, msg)
}

func instrumented(t *testing.T, logs ...*hookLog) *App {
	t.Helper()

	app := newTestApp(t,
		WithSource(testSource(map[string]string{
			"DATABASE_URL": "file:" + filepath.Join(testutil.TempDir(t), "i.db"),
			"MAIL_MAILER":  "array",
		})),
		WithJobs(queue.Handle(func(context.Context, pingJob) error { return nil })),
	)
	t.Cleanup(func() { _ = app.Shutdown(context.Background()) })

	for _, l := range logs {
		err := app.Instrument(Instrumentation{
			Queries: l,
			Jobs:    l,
			Mail:    func(next mail.Transport) mail.Transport { return hookTransport{next: next, log: l} },
		})
		if err != nil {
			t.Fatalf("Instrument() = %v", err)
		}
	}

	return app
}

func TestInstrumentReachesDBQueueAndMailer(t *testing.T) {
	t.Parallel()

	a, b := &hookLog{name: "a"}, &hookLog{name: "b"}
	app := instrumented(t, a, b)

	db, err := app.DB()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := db.ExecContext(t.Context(), "SELECT 1"); err != nil {
		t.Fatal(err)
	}

	q, err := app.Queue()
	if err != nil {
		t.Fatal(err)
	}

	if err := q.Dispatch(t.Context(), pingJob{N: 1}); err != nil {
		t.Fatal(err)
	}

	m, err := app.Mailer()
	if err != nil {
		t.Fatal(err)
	}

	if err := m.Send(t.Context(), plainMail{}); err != nil {
		t.Fatal(err)
	}

	want := map[string][]string{
		"a": {"a query SELECT 1", "a job ping dispatched", "a job ping processed", "a mail Shipped"},
		"b": {"b query SELECT 1", "b job ping dispatched", "b job ping processed", "b mail Shipped"},
	}
	got := map[string][]string{"a": a.log, "b": b.log}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("hooks (-want +got):\n%s", diff)
	}

	// The last wrapper registered is the outermost transport.
	arr, ok := m.Transport().(hookTransport).next.(hookTransport).next.(*mail.Array)
	if !ok || len(arr.Sent()) != 1 {
		t.Fatalf("Mailer().Transport() = %T, want hooks around *mail.Array", m.Transport())
	}
}

func TestInstrumentAfterUseIsAnError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		use  func(*App) error
		in   func(*hookLog) Instrumentation
	}{
		{
			name: "DB",
			use:  func(a *App) error { _, err := a.DB(); return err },
			in:   func(l *hookLog) Instrumentation { return Instrumentation{Queries: l} },
		},
		{
			name: "Queue",
			use:  func(a *App) error { _, err := a.Queue(); return err },
			in:   func(l *hookLog) Instrumentation { return Instrumentation{Jobs: l} },
		},
		{
			name: "Mailer",
			use:  func(a *App) error { _, err := a.Mailer(); return err },
			in: func(*hookLog) Instrumentation {
				return Instrumentation{Mail: func(t mail.Transport) mail.Transport { return t }}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			app := instrumented(t)
			if err := tt.use(app); err != nil {
				t.Fatal(err)
			}

			if err := app.Instrument(tt.in(&hookLog{})); !errors.Is(err, ErrInstrumentTooLate) {
				t.Fatalf("Instrument() after %s() = %v, want ErrInstrumentTooLate", tt.name, err)
			}

			// Hooks for services not built yet are still accepted.
			if err := app.Instrument(Instrumentation{}); err != nil {
				t.Fatalf("Instrument(empty) = %v", err)
			}
		})
	}
}

type panicObserver struct{}

func (panicObserver) ObserveQuery(context.Context, database.Query) { panic("observer bug") }

// Devtools, WithQueryObservers and Instrument each see a query exactly
// once, and a panicking hook does not starve the ones after it.
func TestInstrumentQueriesWithDevtoolsAndQueryObservers(t *testing.T) {
	t.Parallel()

	option, hook := &countingObserver{}, &countingObserver{}

	app, err := New(
		WithSource(testSource(map[string]string{
			"APP_ENV": "development", "DEVTOOLS": "true", "DATABASE_URL": ":memory:",
		})),
		WithLogger(discardLogger()),
		WithQueryObservers(option),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Shutdown(context.Background()) })

	if err := app.Instrument(Instrumentation{Queries: panicObserver{}}); err != nil {
		t.Fatal(err)
	}

	if err := app.Instrument(Instrumentation{Queries: hook}); err != nil {
		t.Fatal(err)
	}

	db, err := app.DB()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := db.ExecContext(t.Context(), "SELECT 1"); err != nil {
		t.Fatal(err)
	}

	want := []string{"SELECT 1"}
	if diff := cmp.Diff(want, option.sql); diff != "" {
		t.Errorf("WithQueryObservers observer (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(want, hook.sql); diff != "" {
		t.Errorf("Instrument observer after a panicking one (-want +got):\n%s", diff)
	}

	if got := len(app.Devtools.Queries()); got != 1 {
		t.Errorf("len(Devtools.Queries()) = %d, want 1", got)
	}
}

type panicJobHook struct{}

func (panicJobHook) ObserveJob(context.Context, queue.JobEvent) { panic("observer bug") }

// A panicking job hook neither fails the dispatch nor starves the hooks
// after it, and devtools still sees each step once.
func TestInstrumentJobsPanicIsContained(t *testing.T) {
	t.Parallel()

	app := newTestApp(t,
		WithSource(testSource(map[string]string{"APP_ENV": "development", "DEVTOOLS": "true"})),
		WithJobs(queue.Handle(func(context.Context, pingJob) error { return nil })),
	)

	hook := &hookLog{name: "b"}
	for _, obs := range []queue.Observer{panicJobHook{}, hook} {
		if err := app.Instrument(Instrumentation{Jobs: obs}); err != nil {
			t.Fatal(err)
		}
	}

	q, err := app.Queue()
	if err != nil {
		t.Fatal(err)
	}

	if err := q.Dispatch(t.Context(), pingJob{N: 1}); err != nil {
		t.Fatalf("Dispatch() = %v", err)
	}

	if diff := cmp.Diff([]string{"b job ping dispatched", "b job ping processed"}, hook.log); diff != "" {
		t.Errorf("hook after the panicking one (-want +got):\n%s", diff)
	}

	if got := len(app.Devtools.Jobs()); got != 2 {
		t.Errorf("len(Devtools.Jobs()) = %d, want 2", got)
	}
}

func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

// emptyFS has no files, so only the process environment is used.
type emptyFS struct{}

func (emptyFS) Open(string) (fs.File, error) { return nil, fs.ErrNotExist }

// An app that uses the starter layout (resources/js/pages) but has no page
// for the Inertia error component gets one warning at boot outside
// production: errors on Inertia visits would fail to render on the client.
func TestWithInertiaWarnsWithoutErrorPage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		env       string
		files     fstest.MapFS
		component string
		warn      bool
	}{
		{"missing", "development", fstest.MapFS{"resources/js/pages/Home.tsx": {}}, "", true},
		{"react", "development", fstest.MapFS{"resources/js/pages/Error.tsx": {}}, "", false},
		{"vue", "development", fstest.MapFS{"resources/js/pages/Error.vue": {}}, "", false},
		{"svelte", "development", fstest.MapFS{"resources/js/pages/Error.svelte": {}}, "", false},
		{
			"custom component",
			"development",
			fstest.MapFS{"resources/js/pages/Errors/Show.tsx": {}},
			"Errors/Show",
			false,
		},
		{
			"custom component missing",
			"development",
			fstest.MapFS{"resources/js/pages/Error.tsx": {}},
			"Errors/Show",
			true,
		},
		{"other layout", "development", fstest.MapFS{"web/src/App.tsx": {}}, "", false},
		{"production", "production", fstest.MapFS{"resources/js/pages/Home.tsx": {}}, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			getenv := func(k string) (string, bool) {
				v, ok := map[string]string{"APP_KEY": testKey, "APP_ENV": tt.env}[k]
				return v, ok
			}

			var buf bytes.Buffer

			if _, err := New(WithEnv(tt.files, getenv), WithLogOutput(&buf),
				WithInertia(inertia.Config{ErrorComponent: tt.component})); err != nil {
				t.Fatal(err)
			}

			if got := strings.Count(
				buf.String(),
				"inertia error page not found",
			); got != map[bool]int{true: 1}[tt.warn] {
				t.Errorf("warnings = %d, want warn=%v; log:\n%s", got, tt.warn, buf.String())
			}
		})
	}
}

// Without WithEnv the project is the working directory, as for config.
func TestWithInertiaWarnsWithoutErrorPageNoEnv(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "resources", "js", "pages"), 0o750); err != nil {
		t.Fatal(err)
	}

	t.Chdir(dir)

	var buf bytes.Buffer

	if _, err := New(WithSource(testSource(map[string]string{"APP_ENV": "development"})), WithLogOutput(&buf),
		WithInertia(inertia.Config{})); err != nil {
		t.Fatal(err)
	}

	if got := strings.Count(buf.String(), "inertia error page not found"); got != 1 {
		t.Errorf("warnings = %d, want 1; log:\n%s", got, buf.String())
	}
}

// WithInertia installs its ErrorPage before the routes run, so an app's own
// hook set there replaces it.
func TestWithInertiaErrorPageOrder(t *testing.T) {
	t.Parallel()

	called := false
	own := func(*web.Ctx, web.ErrorPage) bool { called = true; return false }

	app, err := New(WithSource(testSource(nil)), WithLogger(discardLogger()), WithInertia(inertia.Config{}),
		WithRoutes(func(r *router.Router, _ *App) error {
			if r.Services().ErrorPage == nil {
				t.Error("WithInertia did not install ErrorPage before the routes")
			}

			r.Services().ErrorPage = own

			return nil
		}))
	if err != nil {
		t.Fatal(err)
	}

	app.Router.Services().ErrorPage(nil, web.ErrorPage{})

	if !called {
		t.Error("the app's own ErrorPage hook was replaced")
	}
}

// Docker/Kubernetes secrets: APP_KEY=@file:<path> in the process
// environment is read from the file; a path that is not a regular file
// (a directory, a device) stops the boot.
func TestSecurity_A02_ProcessEnvFileSecrets(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	secret := filepath.Join(dir, "app_key")

	if err := os.WriteFile(secret, []byte(testKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	boot := func(keyRef string) (*App, error) {
		getenv := func(k string) (string, bool) {
			v, ok := map[string]string{"APP_KEY": keyRef, "APP_ENV": "production"}[k]
			return v, ok
		}

		return New(WithEnv(emptyFS{}, getenv), WithLogOutput(io.Discard))
	}

	app, err := boot("@file:" + secret)
	if err != nil {
		t.Fatalf("New(APP_KEY=@file:<secret>) = %v, want the key read from the file", err)
	}

	if app.Key.IsZero() {
		t.Error("APP_KEY not loaded from the file")
	}

	for name, ref := range map[string]string{
		"directory": "@file:" + dir,
		"missing":   "@file:" + filepath.Join(dir, "nope"),
	} {
		if _, err := boot(ref); err == nil || !strings.Contains(err.Error(), "APP_KEY") {
			t.Errorf("New(APP_KEY=%s) = %v, want an APP_KEY error", name, err)
		}
	}
}

// Boot reports every configuration problem at once, not just the first.
func TestNewReportsAllConfigErrors(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"APP_ENV":                "production",
		"APP_DEBUG":              "true",
		"HSTS_FORCE":             "true",
		"APP_URL":                "http://example.com",
		"RATE_LIMIT_IPV6_PREFIX": "0",
		"TRUSTED_PROXIES":        "not-an-ip",
		"TRUST_FORWARDED_PROTO":  "true",
		"HTTP_MAX_HEADER_BYTES":  "1",
		"APP_PREVIOUS_KEYS":      "short",
	}

	_, err := New(WithSource(testSource(env)), WithLogOutput(io.Discard))
	if err == nil {
		t.Fatal("New() = nil error, want the configuration errors")
	}

	for _, want := range []string{
		"APP_DEBUG", "HSTS_FORCE", "RATE_LIMIT_IPV6_PREFIX", "TRUSTED_PROXIES", "HTTP_MAX_HEADER_BYTES",
		"APP_PREVIOUS_KEYS",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("New() error = %q, want it to mention %s", err, want)
		}
	}

	// A TRUSTED_PROXIES that does not parse is reported once, not also as
	// "TRUST_FORWARDED_PROTO requires TRUSTED_PROXIES".
	if strings.Contains(err.Error(), "TRUST_FORWARDED_PROTO requires") {
		t.Errorf("New() error = %q, want no follow-up error from the unparsed TRUSTED_PROXIES", err)
	}
}
