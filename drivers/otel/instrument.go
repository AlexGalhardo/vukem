package otel

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/mail"
	"github.com/AlexGalhardo/vukem/queue"
)

// Metric names follow the OpenTelemetry semantic conventions where one
// exists; durations are histograms in seconds, so their count is the
// request, query or job count.
const (
	MetricHTTPServerDuration = "http.server.request.duration"
	MetricHTTPClientDuration = "http.client.request.duration"
	MetricDBDuration         = "db.client.operation.duration"
	MetricJobDuration        = "messaging.process.duration"
	MetricJobFailures        = "vukem.queue.job.failures"
)

// JobKindKey is the attribute carrying a queue job's kind (its Kind()).
const JobKindKey = attribute.Key("vukem.job.kind")

// durationBuckets are the boundaries the semantic conventions recommend
// for HTTP durations; they fit queries and jobs well enough too.
var durationBuckets = []float64{ //nolint:gochecknoglobals // read-only table
	0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10,
}

// WithMeterProvider records metrics with mp next to the spans (Middleware,
// Transport and Observer). Without it no metric is recorded.
func WithMeterProvider(mp metric.MeterProvider) Option {
	return func(o *options) { o.meter = mp }
}

// WithQueryText records the SQL text (db.query.text) on database spans.
// It is off by default: statements are parameterized through
// database.DB, but SQL built by concatenation would leak its values.
func WithQueryText() Option {
	return func(o *options) { o.queryText = true }
}

// WithDBSystem sets db.system.name ("postgresql", "mysql", "sqlite"...)
// on database spans and metrics. The Provider derives it from
// DATABASE_URL.
func WithDBSystem(name string) Option {
	return func(o *options) { o.dbSystem = name }
}

// WithClientPropagation makes Transport inject the trace context (and
// baggage, with the default propagator) into outgoing requests. It is off
// by default: requests to third parties would receive trace ids and any
// baggage entries; turn it on for clients that call your own services.
func WithClientPropagation() Option {
	return func(o *options) { o.inject = true }
}

func newOptions(opts []Option) options {
	o := options{propagator: DefaultPropagator()}
	for _, opt := range opts {
		opt(&o)
	}

	if o.meter == nil {
		o.meter = metricnoop.NewMeterProvider()
	}

	return o
}

type instruments struct {
	httpServer  metric.Float64Histogram
	httpClient  metric.Float64Histogram
	db          metric.Float64Histogram
	job         metric.Float64Histogram
	jobFailures metric.Int64Counter
}

func newInstruments(mp metric.MeterProvider) instruments {
	m := mp.Meter(ScopeName)
	noop := metricnoop.Meter{}

	// The names are valid constants, so creation only fails on a broken
	// MeterProvider; recording then goes nowhere instead of failing requests.
	histogram := func(name, desc string) metric.Float64Histogram {
		h, err := m.Float64Histogram(name, metric.WithUnit("s"), metric.WithDescription(desc),
			metric.WithExplicitBucketBoundaries(durationBuckets...))
		if err != nil {
			h, _ = noop.Float64Histogram(name)
		}

		return h
	}

	failures, err := m.Int64Counter(MetricJobFailures, metric.WithUnit("{failure}"),
		metric.WithDescription("Failed queue job attempts."))
	if err != nil {
		failures, _ = noop.Int64Counter(MetricJobFailures)
	}

	return instruments{
		httpServer:  histogram(MetricHTTPServerDuration, "Duration of HTTP server requests."),
		httpClient:  histogram(MetricHTTPClientDuration, "Duration of outbound HTTP requests."),
		db:          histogram(MetricDBDuration, "Duration of database statements."),
		job:         histogram(MetricJobDuration, "Duration of queue job attempts."),
		jobFailures: failures,
	}
}

// Observer turns the reports of vukem's instrumentation hooks into spans
// and metrics: it is a database.QueryObserver and a queue.Observer, wraps
// mail transports and outbound HTTP round trippers. The Provider attaches
// one to the app; build your own with NewObserver for a custom setup.
//
// Database and job reports arrive after the work is done, so their spans
// are back-dated by the reported duration. Jobs run by a worker start a
// new trace: the dispatching request's context is not carried in the job.
type Observer struct {
	tracer trace.Tracer
	inst   instruments
	o      options
}

var (
	_ database.QueryObserver = (*Observer)(nil)
	_ queue.Observer         = (*Observer)(nil)
)

// NewObserver returns an Observer recording spans with tp (and metrics
// with WithMeterProvider).
func NewObserver(tp trace.TracerProvider, opts ...Option) *Observer {
	o := newOptions(opts)

	return &Observer{tracer: tp.Tracer(ScopeName), inst: newInstruments(o.meter), o: o}
}

// Instrumentation returns the hooks to pass to App.Instrument.
func (ob *Observer) Instrumentation() vukem.Instrumentation {
	return vukem.Instrumentation{Queries: ob, Jobs: ob, Mail: ob.MailTransport}
}

// ObserveQuery implements database.QueryObserver: one client span named
// after the statement's operation (SELECT, INSERT...) and a
// db.client.operation.duration sample. Bound arguments are never recorded.
func (ob *Observer) ObserveQuery(ctx context.Context, q database.Query) {
	end := time.Now()
	op := operationOf(q.SQL)

	attrs := []attribute.KeyValue{}
	if ob.o.dbSystem != "" {
		attrs = append(attrs, semconv.DBSystemNameKey.String(ob.o.dbSystem))
	}

	if op != "" {
		attrs = append(attrs, semconv.DBOperationName(op))
	}

	name := op
	if name == "" {
		name = "db"
	}

	spanAttrs := attrs
	if ob.o.queryText {
		spanAttrs = append(slices.Clip(spanAttrs), semconv.DBQueryText(q.SQL))
	}

	_, span := ob.tracer.Start(ctx, name,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithTimestamp(end.Add(-q.Duration)),
		trace.WithAttributes(spanAttrs...),
	)

	if q.Err != nil {
		attrs = append(attrs, semconv.ErrorTypeOther)
		span.SetAttributes(semconv.ErrorTypeOther)
		span.SetStatus(codes.Error, "query failed")
	}

	span.End(trace.WithTimestamp(end))
	ob.inst.db.Record(ctx, q.Duration.Seconds(), metric.WithAttributes(attrs...))
}

// operationOf returns the statement's leading keyword in upper case
// (bounded cardinality), or "" when it is not a plain word.
func operationOf(sql string) string {
	sql = strings.TrimLeft(sql, " \t\r\n(")

	end := strings.IndexFunc(sql, func(r rune) bool { return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') })
	if end < 0 {
		end = len(sql)
	}

	if end == 0 || end > 16 {
		return ""
	}

	return strings.ToUpper(sql[:end])
}

// ObserveJob implements queue.Observer: a producer span per dispatch, a
// consumer span and a messaging.process.duration sample per attempt, and
// a vukem.queue.job.failures increment per failed attempt. Payloads are
// never recorded.
func (ob *Observer) ObserveJob(ctx context.Context, e queue.JobEvent) {
	end := time.Now()
	attrs := []attribute.KeyValue{
		semconv.MessagingSystemKey.String("vukem"),
		semconv.MessagingDestinationName(e.Queue),
		JobKindKey.String(e.Kind),
	}

	if e.Status == queue.JobDispatched {
		_, span := ob.tracer.Start(
			ctx,
			"send "+e.Queue,
			trace.WithSpanKind(trace.SpanKindProducer),
			trace.WithTimestamp(end),
			trace.WithAttributes(
				append(attrs, semconv.MessagingOperationTypeSend, semconv.MessagingMessageID(e.ID))...),
		)
		span.End(trace.WithTimestamp(end))

		return
	}

	_, span := ob.tracer.Start(ctx, "process "+e.Queue,
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithTimestamp(end.Add(-e.Duration)),
		trace.WithAttributes(append(attrs,
			semconv.MessagingOperationTypeProcess,
			semconv.MessagingMessageID(e.ID),
			attribute.Int("vukem.job.attempt", e.Attempt),
		)...),
	)

	if e.Status == queue.JobFailed {
		attrs = append(attrs, semconv.ErrorTypeOther)
		span.SetAttributes(semconv.ErrorTypeOther)
		span.SetStatus(codes.Error, "job failed")
		ob.inst.jobFailures.Add(ctx, 1, metric.WithAttributes(attrs...))
	}

	span.End(trace.WithTimestamp(end))
	ob.inst.job.Record(ctx, e.Duration.Seconds(), metric.WithAttributes(attrs...))
}

// MailTransport wraps next so that every send is a client span. Addresses,
// subject and body are never recorded.
func (ob *Observer) MailTransport(next mail.Transport) mail.Transport {
	return mailTransport{next: next, tracer: ob.tracer}
}

type mailTransport struct {
	next   mail.Transport
	tracer trace.Tracer
}

func (t mailTransport) Send(ctx context.Context, msg *mail.Message) error {
	ctx, span := t.tracer.Start(ctx, "mail send", trace.WithSpanKind(trace.SpanKindClient))
	defer span.End()

	err := t.next.Send(ctx, msg)
	if err != nil {
		span.SetAttributes(semconv.ErrorTypeOther)
		span.SetStatus(codes.Error, "mail send failed")
	}

	return err
}

// Transport wraps next (http.DefaultTransport when nil) so that every
// outbound request is a client span and an http.client.request.duration
// sample. Spans record method, host, port and status, never the path or
// query string. Pass it to httpclient.Options.WrapTransport or set it as
// an http.Client's Transport.
func (ob *Observer) Transport(next http.RoundTripper) http.RoundTripper {
	if next == nil {
		next = http.DefaultTransport
	}

	return &roundTripper{next: next, ob: ob}
}

type roundTripper struct {
	next http.RoundTripper
	ob   *Observer
}

func (t *roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	method := methodOf(req.Method)

	scheme := req.URL.Scheme
	if scheme == "" {
		scheme = "http"
	}

	attrs := []attribute.KeyValue{semconv.HTTPRequestMethodKey.String(method), semconv.URLScheme(scheme)}
	if host, port := hostPort(req.URL.Host); host != "" {
		attrs = append(attrs, semconv.ServerAddress(host))
		if port > 0 {
			attrs = append(attrs, semconv.ServerPort(port))
		}
	}

	name := method
	if name == "_OTHER" {
		name = "HTTP"
	}

	ctx, span := t.ob.tracer.Start(req.Context(), name,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attrs...),
	)
	defer span.End()

	if t.ob.o.inject {
		// RoundTrippers must not modify the caller's request.
		req = req.Clone(ctx)
		t.ob.o.propagator.Inject(ctx, propagation.HeaderCarrier(req.Header))
	} else {
		req = req.WithContext(ctx)
	}

	resp, err := t.next.RoundTrip(req)

	switch {
	case err != nil:
		attrs = append(attrs, semconv.ErrorTypeOther)
		span.SetAttributes(semconv.ErrorTypeOther)
		span.SetStatus(codes.Error, "request failed")
	default:
		attrs = append(attrs, semconv.HTTPResponseStatusCode(resp.StatusCode))
		span.SetAttributes(semconv.HTTPResponseStatusCode(resp.StatusCode))

		if resp.StatusCode >= http.StatusBadRequest {
			errType := semconv.ErrorTypeKey.String(strconv.Itoa(resp.StatusCode))
			attrs = append(attrs, errType)
			span.SetAttributes(errType)
			span.SetStatus(codes.Error, http.StatusText(resp.StatusCode))
		}
	}

	t.ob.inst.httpClient.Record(ctx, time.Since(start).Seconds(), metric.WithAttributes(attrs...))

	return resp, err
}
