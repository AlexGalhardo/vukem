package vukem

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/router"
)

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
