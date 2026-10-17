package otel

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	otelglobal "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/AlexGalhardo/vukem"
)

// Config configures the provider. The zero value reads everything from
// app.Source (the process environment and .env files).
type Config struct {
	// ServiceName overrides OTEL_SERVICE_NAME, which defaults to APP_NAME.
	ServiceName string
	// Exporter replaces the OTLP/HTTP exporter (tests, other backends).
	// When nil, OTEL_EXPORTER_OTLP_TRACES_ENDPOINT or
	// OTEL_EXPORTER_OTLP_ENDPOINT selects the collector; with neither set
	// tracing is disabled.
	Exporter sdktrace.SpanExporter
	// Sampler defaults to OTEL_TRACES_SAMPLER (and OTEL_TRACES_SAMPLER_ARG),
	// else ParentBased(AlwaysSample).
	Sampler sdktrace.Sampler
	// MetricReader turns metrics on: the provider builds a MeterProvider
	// on it (same resource as the traces) and records HTTP server, outbound
	// HTTP, database and queue metrics. Pass
	// sdkmetric.NewPeriodicReader(exporter) with the exporter of your
	// backend. When nil no metric is recorded.
	MetricReader sdkmetric.Reader
	// QueryText records the SQL text on database spans (see WithQueryText).
	QueryText bool
	// DBSystem is db.system.name on database spans and metrics (see
	// WithDBSystem). It defaults to the system of DATABASE_URL's scheme
	// (postgresql, mysql, mariadb, sqlite); unknown schemes leave it unset.
	DBSystem string
	// Global also installs the TracerProvider (with the propagator) and
	// the MeterProvider as the OpenTelemetry globals, for libraries
	// instrumented through them. Only providers that are on are installed:
	// a disabled signal never replaces an existing global with a no-op.
	Global bool
}

// Provider is a vukem.Provider that builds a TracerProvider (and a
// MeterProvider with Config.MetricReader), traces and measures every HTTP
// request with Middleware, attaches an Observer to the app's database,
// queue and mailer, and flushes pending data on Shutdown.
type Provider struct {
	cfg Config
	tp  *sdktrace.TracerProvider
	mp  *sdkmetric.MeterProvider
	ob  *Observer
}

// New returns the provider; pass it to vukem.WithProviders first, so its
// middleware wraps the whole stack and its hooks are attached before
// another provider uses the database, queue or mailer.
func New(cfg Config) *Provider { return &Provider{cfg: cfg} }

// TracerProvider returns the SDK provider, or a no-op one when tracing is
// disabled or Register has not run.
func (p *Provider) TracerProvider() trace.TracerProvider {
	if p.tp == nil {
		return noop.NewTracerProvider()
	}

	return p.tp
}

// MeterProvider returns the SDK provider, or a no-op one when metrics are
// disabled or Register has not run.
func (p *Provider) MeterProvider() metric.MeterProvider {
	if p.mp == nil {
		return metricnoop.NewMeterProvider()
	}

	return p.mp
}

// Transport wraps next (http.DefaultTransport when nil) with the
// provider's Observer: outbound requests become client spans and
// http.client.request.duration samples. Use it as an http.Client's
// Transport or as httpclient.Options.WrapTransport. Before Register, or
// when the provider is disabled, it returns next unchanged.
func (p *Provider) Transport(next http.RoundTripper) http.RoundTripper {
	if p.ob == nil {
		if next == nil {
			return http.DefaultTransport
		}

		return next
	}

	return p.ob.Transport(next)
}

// Register implements vukem.Provider.
func (p *Provider) Register(app *vukem.App) error {
	getenv := app.Source.Get
	if strings.EqualFold(getenv("OTEL_SDK_DISABLED"), "true") {
		return nil
	}

	exp := p.cfg.Exporter
	if exp == nil {
		var err error
		if exp, err = traceExporter(getenv); err != nil {
			return err
		}
	}

	if exp == nil && p.cfg.MetricReader == nil {
		return nil
	}

	res, err := p.resource(app)
	if err != nil {
		return err
	}

	var sampler sdktrace.Sampler

	if exp != nil {
		if sampler = p.cfg.Sampler; sampler == nil {
			if sampler, err = samplerFromEnv(getenv); err != nil {
				return err
			}
		}

		if sampler == nil {
			sampler = sdktrace.ParentBased(sdktrace.AlwaysSample())
		}
	}

	opts := []Option{}
	if p.cfg.QueryText {
		opts = append(opts, WithQueryText())
	}

	if sys := p.cfg.DBSystem; sys != "" {
		opts = append(opts, WithDBSystem(sys))
	} else if sys := dbSystemOf(getenv("DATABASE_URL")); sys != "" {
		opts = append(opts, WithDBSystem(sys))
	}

	// Side effects on the app (hooks, logger, globals, middleware) come
	// after everything that can fail; the SDK providers start goroutines,
	// so a failed Register shuts them down.
	if exp != nil {
		p.tp = sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(exp),
			sdktrace.WithResource(res),
			sdktrace.WithSampler(sampler),
		)
	}

	if p.cfg.MetricReader != nil {
		p.mp = sdkmetric.NewMeterProvider(sdkmetric.WithReader(p.cfg.MetricReader), sdkmetric.WithResource(res))
	}

	opts = append(opts, WithMeterProvider(p.MeterProvider()))
	ob := NewObserver(p.TracerProvider(), opts...)

	// A hook that cannot be attached is a boot error rather than a warning:
	// the fix (list otel.New first in WithProviders) is immediate, and a
	// silently partial trace is harder to notice than a failed boot.
	if err := app.Instrument(ob.Instrumentation()); err != nil {
		_ = p.Shutdown(context.Background(), app)
		p.tp, p.mp = nil, nil

		return fmt.Errorf("otel: %w", err)
	}

	p.ob = ob

	if p.tp != nil {
		// Loggers taken from app.Log after Register (request logging,
		// web.Ctx.Log, queue, mail) carry the trace and span ids of the
		// record's context.
		app.Log = slog.New(LogHandler(app.Log.Handler()))
		app.Router.Services().Log = app.Log

		if p.cfg.Global {
			otelglobal.SetTracerProvider(p.tp)
			otelglobal.SetTextMapPropagator(DefaultPropagator())
		}
	}

	if p.mp != nil && p.cfg.Global {
		otelglobal.SetMeterProvider(p.mp)
	}

	mw := Middleware(p.TracerProvider(), opts...)
	app.Router.UseServer(mw)
	app.Router.Use(mw)

	return nil
}

// dbSystemOf maps a DATABASE_URL to db.system.name, following the schemes
// of the core SQLite driver and drivers/postgres and drivers/mysql.
func dbSystemOf(rawURL string) string {
	if rawURL == ":memory:" || strings.HasPrefix(rawURL, "file:") || strings.HasPrefix(rawURL, "sqlite:") {
		return "sqlite"
	}

	scheme, _, ok := strings.Cut(rawURL, "://")
	if !ok {
		return ""
	}

	switch scheme {
	case "postgres", "postgresql":
		return "postgresql"
	case "mysql":
		return "mysql"
	case "mariadb":
		return "mariadb"
	default:
		return ""
	}
}

// traceExporter builds the OTLP/HTTP exporter from app.Source; it returns
// nil when no endpoint is set.
func traceExporter(getenv func(string) string) (sdktrace.SpanExporter, error) {
	endpoint, err := tracesEndpoint(getenv)
	if err != nil || endpoint == "" {
		return nil, err
	}

	opts, err := traceExporterOptions(getenv)
	if err != nil {
		return nil, err
	}

	// otlptracehttp.New does no I/O; the first export connects.
	exp, err := otlptracehttp.New(context.Background(), append(opts, otlptracehttp.WithEndpointURL(endpoint))...)
	if err != nil {
		return nil, fmt.Errorf("otel: exporter: %w", err)
	}

	return exp, nil
}

// resource is OTEL_RESOURCE_ATTRIBUTES over the deployment environment,
// with service.name from Config, OTEL_SERVICE_NAME, the attributes, then
// APP_NAME.
func (p *Provider) resource(app *vukem.App) (*resource.Resource, error) {
	extra, err := resourceAttributes(app.Source.Get)
	if err != nil {
		return nil, err
	}

	attrs := append([]attribute.KeyValue{
		semconv.ServiceName(app.Config.Name),
		semconv.DeploymentEnvironmentNameKey.String(string(app.Config.Env)),
	}, extra...)

	name := p.cfg.ServiceName
	if name == "" {
		name = app.Source.Get("OTEL_SERVICE_NAME")
	}

	if name != "" {
		attrs = append(attrs, semconv.ServiceName(name))
	}

	// Later attributes win on duplicate keys.
	return resource.NewWithAttributes(semconv.SchemaURL, attrs...), nil
}

// shutdownTimeout bounds Shutdown when ctx has no deadline, so an
// unreachable collector cannot hang the process.
const shutdownTimeout = 10 * time.Second

// Shutdown implements vukem.Shutdowner: it exports pending spans and
// metrics. App.Run calls it after the runners (HTTP server, queue worker)
// have drained, within SHUTDOWN_TIMEOUT, so the last requests and jobs are
// exported; without a deadline on ctx it gives up after 10s.
func (p *Provider) Shutdown(ctx context.Context, _ *vukem.App) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, shutdownTimeout)
		defer cancel()
	}

	var errs []error

	if p.tp != nil {
		if err := p.tp.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("otel: shutdown traces: %w", err))
		}
	}

	if p.mp != nil {
		if err := p.mp.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("otel: shutdown metrics: %w", err))
		}
	}

	return errors.Join(errs...)
}

// tracesEndpoint follows the OTLP exporter spec: the per-signal variable is
// used as is, the generic one gets /v1/traces appended.
func tracesEndpoint(getenv func(string) string) (string, error) {
	key, endpoint := "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")
	if endpoint == "" {
		key, endpoint = "OTEL_EXPORTER_OTLP_ENDPOINT", getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
		if endpoint != "" {
			endpoint = strings.TrimRight(endpoint, "/") + "/v1/traces"
		}
	}

	if endpoint == "" {
		return "", nil
	}

	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("otel: %s must be an http(s) URL", key)
	}

	return endpoint, nil
}
