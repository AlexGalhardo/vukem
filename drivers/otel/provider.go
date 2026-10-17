package otel

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"

	otelglobal "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/AlexGalhardo/vukem"
)

// Config configures the tracing provider. The zero value reads everything
// from the environment.
type Config struct {
	// ServiceName overrides OTEL_SERVICE_NAME, which defaults to APP_NAME.
	ServiceName string
	// Exporter replaces the OTLP/HTTP exporter (tests, other backends).
	// When nil, OTEL_EXPORTER_OTLP_TRACES_ENDPOINT or
	// OTEL_EXPORTER_OTLP_ENDPOINT selects the collector; with neither set
	// tracing is disabled.
	Exporter sdktrace.SpanExporter
	// Sampler defaults to ParentBased(AlwaysSample).
	Sampler sdktrace.Sampler
	// Global also installs the TracerProvider and propagator as the
	// OpenTelemetry globals, for libraries instrumented through them.
	Global bool
}

// Provider is a vukem.Provider that builds a TracerProvider, traces every
// HTTP request with Middleware and flushes pending spans on Shutdown.
type Provider struct {
	cfg Config
	tp  *sdktrace.TracerProvider
}

// New returns the tracing provider; pass it to vukem.WithProviders before
// routes so its middleware wraps the whole stack.
func New(cfg Config) *Provider { return &Provider{cfg: cfg} }

// TracerProvider returns the SDK provider, or a no-op one when tracing is
// disabled or Register has not run.
func (p *Provider) TracerProvider() trace.TracerProvider {
	if p.tp == nil {
		return noop.NewTracerProvider()
	}

	return p.tp
}

// Register implements vukem.Provider.
func (p *Provider) Register(app *vukem.App) error {
	if strings.EqualFold(app.Source.Get("OTEL_SDK_DISABLED"), "true") {
		return nil
	}

	exp := p.cfg.Exporter
	if exp == nil {
		endpoint, err := tracesEndpoint(app.Source.Get)
		if err != nil {
			return err
		}

		if endpoint == "" {
			return nil
		}

		// otlptracehttp.New does no I/O; the first export connects.
		exp, err = otlptracehttp.New(context.Background(), otlptracehttp.WithEndpointURL(endpoint))
		if err != nil {
			return fmt.Errorf("otel: exporter: %w", err)
		}
	}

	name := p.cfg.ServiceName
	if name == "" {
		name = app.Source.Get("OTEL_SERVICE_NAME")
	}

	if name == "" {
		name = app.Config.Name
	}

	res := resource.NewWithAttributes(semconv.SchemaURL,
		semconv.ServiceName(name),
		semconv.DeploymentEnvironmentNameKey.String(string(app.Config.Env)),
	)

	sampler := p.cfg.Sampler
	if sampler == nil {
		sampler = sdktrace.ParentBased(sdktrace.AlwaysSample())
	}

	p.tp = sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	)

	if p.cfg.Global {
		otelglobal.SetTracerProvider(p.tp)
		otelglobal.SetTextMapPropagator(DefaultPropagator())
	}

	// Loggers taken from app.Log after Register (request logging, web.Ctx.Log,
	// queue, mail) carry the trace and span ids of the record's context.
	app.Log = slog.New(LogHandler(app.Log.Handler()))
	app.Router.Services().Log = app.Log

	mw := Middleware(p.tp)
	app.Router.UseServer(mw)
	app.Router.Use(mw)

	return nil
}

// Shutdown implements vukem.Shutdowner: it exports pending spans.
func (p *Provider) Shutdown(ctx context.Context, _ *vukem.App) error {
	if p.tp == nil {
		return nil
	}

	if err := p.tp.Shutdown(ctx); err != nil {
		return fmt.Errorf("otel: shutdown: %w", err)
	}

	return nil
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
