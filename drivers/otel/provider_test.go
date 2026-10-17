package otel_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"
	otelglobal "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/drivers/otel"
	"github.com/AlexGalhardo/vukem/mail"
	"github.com/AlexGalhardo/vukem/queue"
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
		{
			name:    "OTEL_RESOURCE_ATTRIBUTES service.name over APP_NAME",
			env:     map[string]string{"OTEL_RESOURCE_ATTRIBUTES": "service.name=from%20attrs,team=core"},
			service: "from attrs",
		},
		{
			name: "OTEL_SERVICE_NAME over OTEL_RESOURCE_ATTRIBUTES",
			env: map[string]string{
				"OTEL_SERVICE_NAME": "shop-api", "OTEL_RESOURCE_ATTRIBUTES": "service.name=from-attrs",
			},
			service: "shop-api",
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
		{
			// Metrics off: this case checks traces, and a metrics shutdown
			// would dial the collector.
			name: "generic endpoint",
			env: map[string]string{
				"OTEL_EXPORTER_OTLP_ENDPOINT": "http://collector:4318/",
				"OTEL_METRICS_EXPORTER":       "none",
			},
		},
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

func TestProvider_MetricsEndpoint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		env         map[string]string
		wantMetrics bool
		wantErr     string
	}{
		{
			name: "metrics endpoint",
			env: map[string]string{
				"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "https://collector.example.com/v1/metrics",
			},
			wantMetrics: true,
		},
		{
			name: "generic endpoint with the otlp metrics exporter",
			env: map[string]string{
				"OTEL_EXPORTER_OTLP_ENDPOINT": "http://collector:4318",
				"OTEL_METRICS_EXPORTER":       "otlp",
			},
			wantMetrics: true,
		},
		{
			// The spec defaults OTEL_METRICS_EXPORTER to otlp, so the
			// generic endpoint covers every signal.
			name:        "generic endpoint alone turns metrics on",
			env:         map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://collector:4318"},
			wantMetrics: true,
		},
		{
			name: "none keeps the generic endpoint to traces",
			env: map[string]string{
				"OTEL_EXPORTER_OTLP_ENDPOINT": "http://collector:4318",
				"OTEL_METRICS_EXPORTER":       "none",
			},
		},
		{
			name: "metrics endpoint overrides the generic one",
			env: map[string]string{
				"OTEL_EXPORTER_OTLP_ENDPOINT":         "http://collector:4318",
				"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "https://metrics.example.com/v1/metrics",
			},
			wantMetrics: true,
		},
		{
			name: "bad metrics endpoint wins over a good generic one",
			env: map[string]string{
				"OTEL_EXPORTER_OTLP_ENDPOINT":         "http://collector:4318",
				"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "collector:4318",
			},
			wantErr: "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT must be an http(s) URL",
		},
		{
			name: "none turns the metrics endpoint off",
			env: map[string]string{
				"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "http://collector:4318/v1/metrics",
				"OTEL_METRICS_EXPORTER":               "none",
			},
		},
		{
			name:    "bad metrics endpoint",
			env:     map[string]string{"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "collector:4318"},
			wantErr: "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT must be an http(s) URL",
		},
		{
			name: "unknown metrics exporter",
			env: map[string]string{
				"OTEL_EXPORTER_OTLP_ENDPOINT": "http://collector:4318",
				"OTEL_METRICS_EXPORTER":       "prometheus",
			},
			wantErr: "OTEL_METRICS_EXPORTER must be otlp or none",
		},
		{
			name: "bad export interval",
			env: map[string]string{
				"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "http://collector:4318/v1/metrics",
				"OTEL_METRIC_EXPORT_INTERVAL":         "1m",
			},
			wantErr: "OTEL_METRIC_EXPORT_INTERVAL must be a positive number of milliseconds",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p := otel.New(otel.Config{})

			// The metrics shutdown flushes to the collector: point every
			// endpoint at a local one, so the test does no network I/O.
			collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			t.Cleanup(collector.Close)

			env := map[string]string{}
			for k, v := range tt.env {
				env[k] = strings.NewReplacer(
					"http://collector:4318", collector.URL,
					"https://collector.example.com", collector.URL,
					"https://metrics.example.com", collector.URL,
				).Replace(v)
			}

			app, err := newApp(t, env, p)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("New() error = %v, want %q", err, tt.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}

			_, got := p.MeterProvider().(*sdkmetric.MeterProvider)
			if got != tt.wantMetrics {
				t.Errorf("MeterProvider() is the SDK provider = %v, want %v", got, tt.wantMetrics)
			}

			if err := app.Shutdown(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

type pingJob struct{}

func (pingJob) Kind() string { return "ping" }

type noticeMail struct{}

func (noticeMail) Envelope() mail.Envelope {
	return mail.Envelope{To: []string{"ana@example.com"}, Subject: "Notice"}
}

func (noticeMail) Content() mail.Content { return mail.Text("hello") }

func TestProvider_InstrumentsDatabaseQueueMailAndHTTP(t *testing.T) {
	t.Parallel()

	exp := keepExporter{tracetest.NewInMemoryExporter()}
	reader := sdkmetric.NewManualReader()
	p := otel.New(otel.Config{Exporter: exp, MetricReader: reader})

	app, err := vukem.New(
		vukem.WithSource(config.MapSource(map[string]string{
			"APP_KEY": config.GenerateKey(), "APP_ENV": "development",
			"DATABASE_URL": "file:" + filepath.Join(t.TempDir(), "o.db"), "MAIL_MAILER": "array",
		})),
		vukem.WithLogger(slog.New(slog.DiscardHandler)),
		vukem.WithStdout(io.Discard),
		vukem.WithProviders(p),
		vukem.WithJobs(queue.Handle(func(context.Context, pingJob) error { return nil })),
		vukem.WithRoutes(func(r *router.Router, app *vukem.App) error {
			r.Get("/orders/{id}", func(c *web.Ctx) error {
				db, err := app.DB()
				if err != nil {
					return err
				}

				if _, err := db.ExecContext(c.Context(), "SELECT 1"); err != nil {
					return err
				}

				q, err := app.Queue()
				if err != nil {
					return err
				}

				if err := q.Dispatch(c.Context(), pingJob{}); err != nil {
					return err
				}

				m, err := app.Mailer()
				if err != nil {
					return err
				}

				if err := m.Send(c.Context(), noticeMail{}); err != nil {
					return err
				}

				return c.String(http.StatusOK, "order")
			})

			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	app.Router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/orders/7", http.NoBody))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /orders/7 = %d, want 200: %s", rec.Code, rec.Body)
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &rm); err != nil {
		t.Fatal(err)
	}

	var metrics []string
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			metrics = append(metrics, m.Name)
		}
	}

	slices.Sort(metrics)

	wantMetrics := []string{otel.MetricDBDuration, otel.MetricHTTPServerDuration, otel.MetricJobDuration}
	if diff := cmp.Diff(wantMetrics, metrics); diff != "" {
		t.Errorf("metrics (-want +got):\n%s", diff)
	}

	// Shutdown closes the database before t.TempDir is removed (Windows).
	if err := app.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, s := range exp.GetSpans() {
		names = append(names, s.Name)

		if s.Name == "SELECT" {
			if got := attribute.NewSet(s.Attributes...); got.HasValue("db.system.name") {
				if v, _ := got.Value("db.system.name"); v.AsString() != "sqlite" {
					t.Errorf("SELECT db.system.name = %q, want sqlite (from DATABASE_URL)", v.AsString())
				}
			} else {
				t.Error("SELECT span has no db.system.name")
			}
		}
	}

	slices.Sort(names)

	want := []string{"GET /orders/{id}", "SELECT", "mail send", "process default", "send default"}
	if diff := cmp.Diff(want, names); diff != "" {
		t.Fatalf("exported spans (-want +got):\n%s", diff)
	}
}

func TestProvider_MetricsWithoutTraces(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	p := otel.New(otel.Config{MetricReader: reader})

	app, err := newApp(t, nil, p)
	if err != nil {
		t.Fatal(err)
	}

	app.Router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/orders/7", http.NoBody))

	if _, ok := p.TracerProvider().(*sdktrace.TracerProvider); ok {
		t.Error("TracerProvider() is the SDK provider without an endpoint, want no-op")
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &rm); err != nil {
		t.Fatal(err)
	}

	if len(rm.ScopeMetrics) != 1 || rm.ScopeMetrics[0].Metrics[0].Name != otel.MetricHTTPServerDuration {
		t.Fatalf("metrics = %+v, want %s", rm.ScopeMetrics, otel.MetricHTTPServerDuration)
	}

	if err := app.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// TestProvider_ExporterSettingsFromSource checks that OTEL_* exporter
// settings in app.Source (a .env file, here a map) reach the collector,
// not only those of the process environment.
func TestProvider_ExporterSettingsFromSource(t *testing.T) {
	t.Parallel()

	type request struct {
		Path, APIKey, Encoding string
	}

	var (
		mu  sync.Mutex
		got []request
	)

	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = append(got, request{r.URL.Path, r.Header.Get("X-Api-Key"), r.Header.Get("Content-Encoding")})
		mu.Unlock()

		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(collector.Close)

	p := otel.New(otel.Config{})

	app, err := newApp(t, map[string]string{
		"OTEL_EXPORTER_OTLP_ENDPOINT":    collector.URL,
		"OTEL_EXPORTER_OTLP_HEADERS":     "x-api-key=k%20ey, other=1",
		"OTEL_EXPORTER_OTLP_COMPRESSION": "gzip",
		"OTEL_EXPORTER_OTLP_TIMEOUT":     "5000",
	}, p)
	if err != nil {
		t.Fatal(err)
	}

	app.Router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/orders/7", http.NoBody))

	if err := app.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()

	slices.SortFunc(got, func(a, b request) int { return strings.Compare(a.Path, b.Path) })

	// The generic endpoint carries both signals, each on its own path.
	want := []request{
		{Path: "/v1/metrics", APIKey: "k ey", Encoding: "gzip"},
		{Path: "/v1/traces", APIKey: "k ey", Encoding: "gzip"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("collector requests (-want +got):\n%s", diff)
	}
}

func TestProvider_SamplerFromSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		env   map[string]string
		spans int
	}{
		{env: map[string]string{"OTEL_TRACES_SAMPLER": "always_off"}, spans: 0},
		{env: map[string]string{"OTEL_TRACES_SAMPLER": "traceidratio", "OTEL_TRACES_SAMPLER_ARG": "0"}, spans: 0},
		{env: map[string]string{"OTEL_TRACES_SAMPLER": "parentbased_traceidratio"}, spans: 1},
		{env: map[string]string{"OTEL_TRACES_SAMPLER": "always_on"}, spans: 1},
	}

	for _, tt := range tests {
		t.Run(tt.env["OTEL_TRACES_SAMPLER"]+tt.env["OTEL_TRACES_SAMPLER_ARG"], func(t *testing.T) {
			t.Parallel()

			exp := keepExporter{tracetest.NewInMemoryExporter()}

			app, err := newApp(t, tt.env, otel.New(otel.Config{Exporter: exp}))
			if err != nil {
				t.Fatal(err)
			}

			app.Router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/orders/7", http.NoBody))

			if err := app.Shutdown(t.Context()); err != nil {
				t.Fatal(err)
			}

			if n := len(exp.GetSpans()); n != tt.spans {
				t.Fatalf("exported spans = %d, want %d", n, tt.spans)
			}
		})
	}
}

func TestSecurity_A09_OTelBadSettingsDoNotEchoHeaders(t *testing.T) {
	t.Parallel()

	const secret = "s3cr3t-api-key"

	tests := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{
			name:    "headers",
			env:     map[string]string{"OTEL_EXPORTER_OTLP_HEADERS": "Authorization Bearer " + secret},
			wantErr: "OTEL_EXPORTER_OTLP_HEADERS: entry 1 is not key=value",
		},
		{
			name:    "traces headers",
			env:     map[string]string{"OTEL_EXPORTER_OTLP_TRACES_HEADERS": "a=1,b=%zz" + secret},
			wantErr: "OTEL_EXPORTER_OTLP_TRACES_HEADERS: entry 2: bad percent-encoding",
		},
		{
			name: "metrics headers",
			env: map[string]string{
				"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "http://collector:4318/v1/metrics",
				"OTEL_EXPORTER_OTLP_METRICS_HEADERS":  "Authorization Bearer " + secret,
			},
			wantErr: "OTEL_EXPORTER_OTLP_METRICS_HEADERS: entry 1 is not key=value",
		},
		{
			name:    "timeout",
			env:     map[string]string{"OTEL_EXPORTER_OTLP_TRACES_TIMEOUT": "10s"},
			wantErr: "OTEL_EXPORTER_OTLP_TRACES_TIMEOUT must be a positive number of milliseconds",
		},
		{
			name:    "compression",
			env:     map[string]string{"OTEL_EXPORTER_OTLP_COMPRESSION": "zstd"},
			wantErr: "OTEL_EXPORTER_OTLP_COMPRESSION must be gzip or none",
		},
		{
			name:    "sampler",
			env:     map[string]string{"OTEL_TRACES_SAMPLER": "sometimes"},
			wantErr: "unknown OTEL_TRACES_SAMPLER",
		},
		{
			name:    "sampler arg",
			env:     map[string]string{"OTEL_TRACES_SAMPLER": "traceidratio", "OTEL_TRACES_SAMPLER_ARG": "2"},
			wantErr: "OTEL_TRACES_SAMPLER_ARG must be a ratio between 0 and 1",
		},
		{
			name:    "resource attributes",
			env:     map[string]string{"OTEL_RESOURCE_ATTRIBUTES": "=x"},
			wantErr: "OTEL_RESOURCE_ATTRIBUTES: entry 1 is not key=value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tt.env["OTEL_EXPORTER_OTLP_ENDPOINT"] = "http://collector:4318"

			_, err := newApp(t, tt.env, otel.New(otel.Config{}))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("New() error = %v, want %q", err, tt.wantErr)
			}

			if strings.Contains(err.Error(), secret) {
				t.Fatalf("New() error = %v, echoes the header value", err)
			}

			for k, v := range tt.env {
				if k != "OTEL_EXPORTER_OTLP_ENDPOINT" && strings.Contains(err.Error(), strconv.Quote(v)) {
					t.Errorf("New() error = %v, echoes the %s value", err, k)
				}
			}
		})
	}
}

func TestProvider_InstrumentAfterDBUseFails(t *testing.T) {
	t.Parallel()

	early := vukem.ProviderFunc(func(app *vukem.App) error {
		_, err := app.DB()
		return err
	})

	exp := &shutdownExporter{InMemoryExporter: tracetest.NewInMemoryExporter()}
	reader := sdkmetric.NewManualReader()

	_, err := vukem.New(
		vukem.WithSource(config.MapSource(map[string]string{
			"APP_KEY": config.GenerateKey(), "APP_ENV": "development", "DATABASE_URL": ":memory:",
		})),
		vukem.WithLogger(slog.New(slog.DiscardHandler)),
		vukem.WithProviders(early, otel.New(otel.Config{Exporter: exp, MetricReader: reader})),
	)

	if !errors.Is(err, vukem.ErrInstrumentTooLate) {
		t.Fatalf("New() = %v, want ErrInstrumentTooLate", err)
	}

	// The failed Register must not leak the batcher and reader.
	if !exp.shut.Load() {
		t.Error("span exporter not shut down after a failed Register")
	}

	if err := reader.Collect(t.Context(), &metricdata.ResourceMetrics{}); !errors.Is(err, sdkmetric.ErrReaderShutdown) {
		t.Errorf("reader.Collect() = %v, want ErrReaderShutdown", err)
	}
}

type shutdownExporter struct {
	*tracetest.InMemoryExporter

	shut atomic.Bool
}

func (e *shutdownExporter) Shutdown(ctx context.Context) error {
	e.shut.Store(true)
	return e.InMemoryExporter.Shutdown(ctx)
}

// TestProvider_GlobalInstallsOnlyRealProviders is not parallel: it
// changes the OpenTelemetry globals.
func TestProvider_GlobalInstallsOnlyRealProviders(t *testing.T) {
	existing := sdktrace.NewTracerProvider()
	t.Cleanup(func() { _ = existing.Shutdown(context.Background()) })

	otelglobal.SetTracerProvider(existing)
	t.Cleanup(func() { otelglobal.SetTracerProvider(tracenoop.NewTracerProvider()) })
	t.Cleanup(func() { otelglobal.SetMeterProvider(metricnoop.NewMeterProvider()) })

	p := otel.New(otel.Config{MetricReader: sdkmetric.NewManualReader(), Global: true})

	app, err := newApp(t, nil, p)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = app.Shutdown(context.Background()) })

	if otelglobal.GetTracerProvider() != existing {
		t.Error("Global replaced the global TracerProvider with a no-op (tracing is off)")
	}

	if otelglobal.GetMeterProvider() != p.MeterProvider() {
		t.Error("Global did not install the MeterProvider")
	}
}
