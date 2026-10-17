// Package otel traces and measures Vukem apps with OpenTelemetry: a
// net/http server middleware that emits one server span per request (W3C
// traceparent and baggage propagation, HTTP semantic conventions), an
// [Observer] that turns the app's database, queue and mail hooks
// (App.Instrument) and outbound HTTP round trips into spans and metrics,
// and a provider that builds an OTLP/HTTP TracerProvider (and a
// MeterProvider on Config.MetricReader) from app.Source and flushes them
// on shutdown.
//
//	app, err := vukem.New(vukem.WithProviders(otel.New(otel.Config{})), ...)
//
// or, with a TracerProvider of your own:
//
//	r.UseServer(otel.Middleware(tp))
//	log := slog.New(otel.LogHandler(base)) // trace_id and span_id on records
//
// The provider installs [LogHandler] on app.Log itself.
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/subsystems.md.
package otel

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

// ScopeName is the instrumentation scope of the spans this package emits.
const ScopeName = "github.com/AlexGalhardo/vukem/drivers/otel"

// Option configures Middleware, NewObserver and the Observer's Transport.
type Option func(*options)

type options struct {
	propagator propagation.TextMapPropagator
	meter      metric.MeterProvider
	queryText  bool
	inject     bool
}

// WithPropagator replaces the default W3C TraceContext + Baggage propagator
// (extracted by Middleware, injected by Transport with
// WithClientPropagation).
func WithPropagator(p propagation.TextMapPropagator) Option {
	return func(o *options) { o.propagator = p }
}

// DefaultPropagator is W3C TraceContext + Baggage.
func DefaultPropagator() propagation.TextMapPropagator {
	return propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})
}

type ctxKey struct{}

// state marks a request already traced by an outer Middleware so a
// route-level install only names the span instead of starting another.
type state struct {
	span  trace.Span
	route string
}

// Middleware returns net/http middleware that starts a server span per
// request, continuing the trace from the incoming traceparent header.
//
// The span is named "METHOD /route/{param}" after the matched ServeMux
// pattern (never the raw path, which would explode cardinality), or just
// "METHOD" when no route matched. Install it with Router.UseServer to time
// the whole stack; installing it again with Router.Use names the span even
// when middleware in between replaced the *http.Request. 5xx responses and
// panics mark the span as an error. Query strings and paths are not
// recorded: they may carry tokens. With WithMeterProvider each request is
// also an http.server.request.duration sample (method, route, status).
func Middleware(tp trace.TracerProvider, opts ...Option) func(http.Handler) http.Handler {
	o := newOptions(opts)
	tracer := tp.Tracer(ScopeName)
	duration := newInstruments(o.meter).httpServer

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if st, ok := r.Context().Value(ctxKey{}).(*state); ok {
				if route := routeOf(r.Pattern); route != "" && st.route == "" {
					st.route = route
					nameSpan(st.span, r.Method, route)
				}

				next.ServeHTTP(w, r)

				return
			}

			start := time.Now()
			ctx := o.propagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
			method := methodOf(r.Method)
			route := routeOf(r.Pattern)

			ctx, span := tracer.Start(ctx, spanName(method, route),
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(requestAttrs(r, method)...),
			)

			st := &state{span: span}
			if route != "" {
				st.route = route
				span.SetAttributes(semconv.HTTPRoute(route))
			}

			req := r.WithContext(context.WithValue(ctx, ctxKey{}, st))
			rw := &recorder{ResponseWriter: w}

			metricAttrs := func(extra ...attribute.KeyValue) metric.MeasurementOption {
				attrs := append([]attribute.KeyValue{
					semconv.HTTPRequestMethodKey.String(method),
					semconv.URLScheme(schemeOf(r)),
				}, extra...)
				if st.route != "" {
					attrs = append(attrs, semconv.HTTPRoute(st.route))
				}

				return metric.WithAttributes(attrs...)
			}

			defer func() {
				if p := recover(); p != nil {
					span.SetAttributes(semconv.ErrorTypeKey.String("panic"))
					span.SetStatus(codes.Error, "panic")
					span.End()
					duration.Record(ctx, time.Since(start).Seconds(),
						metricAttrs(semconv.ErrorTypeKey.String("panic")))
					panic(p)
				}

				// ServeMux sets Pattern on the request it receives: req, when no
				// middleware in between cloned it.
				if st.route == "" {
					if route := routeOf(req.Pattern); route != "" {
						st.route = route
						nameSpan(span, r.Method, route)
					}
				}

				status := rw.status
				if status == 0 {
					status = http.StatusOK
				}

				attrs := []attribute.KeyValue{semconv.HTTPResponseStatusCode(status)}
				span.SetAttributes(attrs[0])

				if status >= http.StatusInternalServerError {
					attrs = append(attrs, semconv.ErrorTypeKey.String(strconv.Itoa(status)))
					span.SetAttributes(attrs[1])
					span.SetStatus(codes.Error, http.StatusText(status))
				}

				span.End()
				duration.Record(ctx, time.Since(start).Seconds(), metricAttrs(attrs...))
			}()

			next.ServeHTTP(rw, req)
		})
	}
}

func nameSpan(span trace.Span, method, route string) {
	span.SetName(spanName(methodOf(method), route))
	span.SetAttributes(semconv.HTTPRoute(route))
}

func spanName(method, route string) string {
	if method == "_OTHER" {
		method = "HTTP"
	}

	if route == "" {
		return method
	}

	return method + " " + route
}

// routeOf turns a ServeMux pattern ("GET example.com/users/{id}") into the
// http.route attribute ("/users/{id}").
func routeOf(pattern string) string {
	if _, rest, ok := strings.Cut(pattern, " "); ok {
		pattern = strings.TrimSpace(rest)
	}

	i := strings.IndexByte(pattern, '/')
	if i < 0 {
		return ""
	}

	route := strings.TrimSuffix(pattern[i:], "{$}")
	if route == "" {
		return "/"
	}

	return route
}

// methodOf bounds the method's cardinality, as the semantic conventions ask.
func methodOf(m string) string {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodConnect, http.MethodOptions, http.MethodTrace:
		return m
	default:
		return "_OTHER"
	}
}

func schemeOf(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}

	return "http"
}

func requestAttrs(r *http.Request, method string) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		semconv.HTTPRequestMethodKey.String(method),
		semconv.URLScheme(schemeOf(r)),
		semconv.NetworkProtocolVersion(protocolVersion(r)),
	}

	if host, port := hostPort(r.Host); host != "" {
		attrs = append(attrs, semconv.ServerAddress(host))
		if port > 0 {
			attrs = append(attrs, semconv.ServerPort(port))
		}
	}

	if ua := r.UserAgent(); ua != "" {
		attrs = append(attrs, semconv.UserAgentOriginal(ua))
	}

	return attrs
}

func protocolVersion(r *http.Request) string {
	if r.ProtoMajor >= 2 {
		return strconv.Itoa(r.ProtoMajor)
	}

	return strconv.Itoa(r.ProtoMajor) + "." + strconv.Itoa(r.ProtoMinor)
}

func hostPort(hostport string) (string, int) {
	host, p, err := net.SplitHostPort(hostport)
	if err != nil {
		return hostport, 0
	}

	port, err := strconv.Atoi(p)
	if err != nil {
		return host, 0
	}

	return host, port
}

// recorder captures the response status code.
type recorder struct {
	http.ResponseWriter

	status int
}

func (w *recorder) WriteHeader(code int) {
	// 1xx responses (103 Early Hints) are informational, not the status.
	if w.status == 0 && code >= http.StatusOK {
		w.status = code
	}

	w.ResponseWriter.WriteHeader(code)
}

func (w *recorder) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}

	return w.ResponseWriter.Write(b)
}

// Flush keeps streaming responses (SSE) working through the wrapper.
func (w *recorder) Flush() {
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *recorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }
