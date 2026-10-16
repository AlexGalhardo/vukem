package vukem

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AlexGalhardo/vukem/config"
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
		{name: "missing key", src: config.MapSource(map[string]string{}), want: "APP_KEY is not set"},
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

func TestHTTPServerListenError(t *testing.T) {
	t.Parallel()

	srv := NewHTTPServer("256.0.0.1:99999", http.NotFoundHandler())
	if err := srv.Run(t.Context()); err == nil {
		t.Fatal("Run() with bad address = nil error")
	}
}
