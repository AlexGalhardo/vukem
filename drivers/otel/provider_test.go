package otel_test

import (
	"context"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/drivers/otel"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

// keepExporter keeps spans after Shutdown (InMemoryExporter resets on it)
// so the test can check that Shutdown flushed them.
type keepExporter struct{ *tracetest.InMemoryExporter }

func (keepExporter) Shutdown(context.Context) error { return nil }

func newApp(t *testing.T, env map[string]string, p *otel.Provider) (*vukem.App, error) {
	t.Helper()

	m := map[string]string{"APP_KEY": config.GenerateKey(), "APP_ENV": "development", "APP_NAME": "Shop"}
	maps.Copy(m, env)

	return vukem.New(
		vukem.WithSource(config.MapSource(m)),
		vukem.WithLogger(slog.New(slog.DiscardHandler)),
		vukem.WithStdout(io.Discard),
		vukem.WithProviders(p),
		vukem.WithRoutes(func(r *router.Router, app *vukem.App) error {
			if err := vukem.DefaultMiddleware(app, r); err != nil {
				return err
			}

			r.Get("/orders/{id}", func(c *web.Ctx) error { return c.String(http.StatusOK, "order") })

			return nil
		}),
	)
}

func TestProvider_ExportsSpanPerRequestOnShutdown(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		env     map[string]string
		cfg     otel.Config
		service string
	}{
		{name: "APP_NAME by default", service: "Shop"},
		{name: "OTEL_SERVICE_NAME wins", env: map[string]string{"OTEL_SERVICE_NAME": "shop-api"}, service: "shop-api"},
		{
			name:    "Config wins",
			env:     map[string]string{"OTEL_SERVICE_NAME": "x"},
			cfg:     otel.Config{ServiceName: "cfg"},
			service: "cfg",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			exp := keepExporter{tracetest.NewInMemoryExporter()}
			tt.cfg.Exporter = exp
			p := otel.New(tt.cfg)

			app, err := newApp(t, tt.env, p)
			if err != nil {
				t.Fatal(err)
			}

			for range 2 {
				rec := httptest.NewRecorder()
				app.Router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/orders/7", http.NoBody))

				if rec.Code != http.StatusOK {
					t.Fatalf("GET /orders/7 = %d, want 200", rec.Code)
				}
			}

			if err := app.Shutdown(t.Context()); err != nil {
				t.Fatal(err)
			}

			spans := exp.GetSpans()
			if len(spans) != 2 {
				t.Fatalf("exported spans = %d, want 2", len(spans))
			}

			for _, s := range spans {
				if s.Name != "GET /orders/{id}" {
					t.Errorf("span name = %q, want %q", s.Name, "GET /orders/{id}")
				}

				got := map[string]string{}
				for _, kv := range s.Resource.Attributes() {
					got[string(kv.Key)] = kv.Value.String()
				}

				if got["service.name"] != tt.service || got["deployment.environment.name"] != "development" {
					t.Errorf("resource = %v, want service.name %s in development", got, tt.service)
				}
			}
		})
	}
}

func TestProvider_Disabled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  map[string]string
		cfg  otel.Config
	}{
		{name: "no endpoint"},
		{
			name: "OTEL_SDK_DISABLED",
			env: map[string]string{
				"OTEL_SDK_DISABLED":           "true",
				"OTEL_EXPORTER_OTLP_ENDPOINT": "http://localhost:4318",
			},
			cfg: otel.Config{Exporter: tracetest.NewInMemoryExporter()},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p := otel.New(tt.cfg)
			if _, err := newApp(t, tt.env, p); err != nil {
				t.Fatal(err)
			}

			if _, ok := p.TracerProvider().(*sdktrace.TracerProvider); ok {
				t.Error("TracerProvider() is the SDK provider, want no-op")
			}
		})
	}
}

func TestProvider_Endpoint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{name: "generic endpoint", env: map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://collector:4318/"}},
		{
			name: "traces endpoint",
			env:  map[string]string{"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "https://collector.example.com/v1/traces"},
		},
		{
			name:    "not a URL",
			env:     map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "collector:4318"},
			wantErr: "OTEL_EXPORTER_OTLP_ENDPOINT must be an http(s) URL",
		},
		{
			name:    "bad traces endpoint",
			env:     map[string]string{"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "ftp://collector"},
			wantErr: "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT must be an http(s) URL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p := otel.New(otel.Config{})

			app, err := newApp(t, tt.env, p)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("New() error = %v, want %q", err, tt.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}

			if _, ok := p.TracerProvider().(*sdktrace.TracerProvider); !ok {
				t.Error("TracerProvider() is a no-op, want the SDK provider")
			}

			// No span was recorded, so shutting down sends nothing.
			if err := app.Shutdown(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}
