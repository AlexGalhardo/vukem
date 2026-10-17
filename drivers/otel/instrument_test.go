package otel_test

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/drivers/otel"
	"github.com/AlexGalhardo/vukem/mail"
	"github.com/AlexGalhardo/vukem/queue"
)

func newMeter(t *testing.T) (*sdkmetric.MeterProvider, *sdkmetric.ManualReader) {
	t.Helper()

	r := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(r))
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })

	return mp, r
}

// point is one data point of a histogram or sum, reduced to what tests
// compare: its attributes and its count (histograms) or value (sums).
type point struct {
	Attrs map[string]string
	N     int64
}

func points(t *testing.T, r *sdkmetric.ManualReader, name string) []point {
	t.Helper()

	var rm metricdata.ResourceMetrics
	if err := r.Collect(t.Context(), &rm); err != nil {
		t.Fatal(err)
	}

	setAttrs := func(s attribute.Set) map[string]string {
		m := map[string]string{}
		for _, kv := range s.ToSlice() {
			m[string(kv.Key)] = kv.Value.String()
		}

		return m
	}

	var out []point

	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}

			switch d := m.Data.(type) {
			case metricdata.Histogram[float64]:
				for _, dp := range d.DataPoints {
					out = append(out, point{Attrs: setAttrs(dp.Attributes), N: int64(dp.Count)})
				}
			case metricdata.Sum[int64]:
				for _, dp := range d.DataPoints {
					out = append(out, point{Attrs: setAttrs(dp.Attributes), N: dp.Value})
				}
			}
		}
	}

	slices.SortFunc(out, func(a, b point) int { return strings.Compare(a.Attrs["error.type"], b.Attrs["error.type"]) })

	return out
}

func TestMiddleware_RecordsServerDuration(t *testing.T) {
	t.Parallel()

	tp, _ := newRecorder(t)
	mp, r := newMeter(t)
	h := otel.Middleware(tp, otel.WithMeterProvider(mp))(testMux())

	for _, target := range []string{"/users/1", "/users/2?token=secret"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, target, http.NoBody))
	}

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/boom", http.NoBody))

	want := []point{
		{Attrs: map[string]string{
			"http.request.method": "GET", "http.route": "/users/{id}", "http.response.status_code": "200",
			"url.scheme": "http",
		}, N: 2},
		{Attrs: map[string]string{
			"http.request.method": "POST", "http.route": "/boom", "http.response.status_code": "503",
			"url.scheme": "http", "error.type": "503",
		}, N: 1},
	}
	if diff := cmp.Diff(want, points(t, r, otel.MetricHTTPServerDuration)); diff != "" {
		t.Fatalf("%s points (-want +got):\n%s", otel.MetricHTTPServerDuration, diff)
	}
}

func TestMiddleware_NoMeterProviderRecordsNothing(t *testing.T) {
	t.Parallel()

	tp, sr := newRecorder(t)
	otel.Middleware(tp)(
		testMux(),
	).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/users/1", http.NoBody))

	if s := onlySpan(t, sr); s.Name() != "GET /users/{id}" {
		t.Fatalf("span name = %q", s.Name())
	}
}

func TestObserver_Query(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		opts      []otel.Option
		q         database.Query
		spanName  string
		wantAttrs map[string]string
		wantErr   bool
	}{
		{
			name: "select",
			q: database.Query{
				SQL:      "  select * from users where id = ?",
				Args:     []any{"secret"},
				Duration: time.Second,
			},
			spanName:  "SELECT",
			wantAttrs: map[string]string{"db.operation.name": "SELECT"},
		},
		{
			name:     "query text opt-in",
			opts:     []otel.Option{otel.WithQueryText()},
			q:        database.Query{SQL: "INSERT INTO t (a) VALUES (?)", Args: []any{1}},
			spanName: "INSERT",
			wantAttrs: map[string]string{
				"db.operation.name": "INSERT", "db.query.text": "INSERT INTO t (a) VALUES (?)",
			},
		},
		{
			name:      "error",
			q:         database.Query{SQL: "(WITH x AS (SELECT 1) SELECT * FROM x)", Err: errors.New("boom")},
			spanName:  "WITH",
			wantAttrs: map[string]string{"db.operation.name": "WITH", "error.type": "_OTHER"},
			wantErr:   true,
		},
		{name: "no keyword", q: database.Query{SQL: "-- comment"}, spanName: "db", wantAttrs: map[string]string{}},
		{
			name:      "db system",
			opts:      []otel.Option{otel.WithDBSystem("postgresql")},
			q:         database.Query{SQL: "UPDATE t SET a = $1"},
			spanName:  "UPDATE",
			wantAttrs: map[string]string{"db.operation.name": "UPDATE", "db.system.name": "postgresql"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tp, sr := newRecorder(t)
			mp, r := newMeter(t)
			ob := otel.NewObserver(tp, append(tt.opts, otel.WithMeterProvider(mp))...)

			ctx, parent := tp.Tracer("test").Start(t.Context(), "request")
			ob.ObserveQuery(ctx, tt.q)
			parent.End()

			s := sr.Ended()[0]
			if s.Name() != tt.spanName || s.SpanKind() != trace.SpanKindClient {
				t.Fatalf("span = %q %v, want %q client", s.Name(), s.SpanKind(), tt.spanName)
			}

			if s.Parent().SpanID() != parent.SpanContext().SpanID() {
				t.Error("query span is not a child of the context's span")
			}

			if got := s.EndTime().Sub(s.StartTime()); got != tt.q.Duration {
				t.Errorf("span duration = %v, want %v", got, tt.q.Duration)
			}

			if diff := cmp.Diff(tt.wantAttrs, attrs(s)); diff != "" {
				t.Errorf("attributes (-want +got):\n%s", diff)
			}

			if (s.Status().Code == codes.Error) != tt.wantErr {
				t.Errorf("status = %v, want error %v", s.Status(), tt.wantErr)
			}

			delete(tt.wantAttrs, "db.query.text")

			want := []point{{Attrs: tt.wantAttrs, N: 1}}
			if diff := cmp.Diff(want, points(t, r, otel.MetricDBDuration)); diff != "" {
				t.Errorf("%s (-want +got):\n%s", otel.MetricDBDuration, diff)
			}
		})
	}
}

func TestObserver_Jobs(t *testing.T) {
	t.Parallel()

	tp, sr := newRecorder(t)
	mp, r := newMeter(t)
	ob := otel.NewObserver(tp, otel.WithMeterProvider(mp))
	ctx := t.Context()

	ob.ObserveJob(ctx, queue.JobEvent{ID: "1", Kind: "ping", Queue: "default", Status: queue.JobDispatched})
	ob.ObserveJob(ctx, queue.JobEvent{
		ID: "1", Kind: "ping", Queue: "default", Status: queue.JobProcessed, Attempt: 1, Duration: time.Second,
	})
	ob.ObserveJob(ctx, queue.JobEvent{
		ID: "2", Kind: "ping", Queue: "default", Status: queue.JobFailed, Attempt: 3, Err: errors.New("x"),
	})

	type span struct {
		Name  string
		Kind  trace.SpanKind
		Error bool
		Attrs map[string]string
	}

	var got []span
	for _, s := range sr.Ended() {
		got = append(got, span{s.Name(), s.SpanKind(), s.Status().Code == codes.Error, attrs(s)})
	}

	base := func(extra map[string]string) map[string]string {
		m := map[string]string{
			"messaging.system": "vukem", "messaging.destination.name": "default", "vukem.job.kind": "ping",
		}
		maps.Copy(m, extra)

		return m
	}

	want := []span{
		{"send default", trace.SpanKindProducer, false, base(map[string]string{
			"messaging.operation.type": "send", "messaging.message.id": "1",
		})},
		{"process default", trace.SpanKindConsumer, false, base(map[string]string{
			"messaging.operation.type": "process", "messaging.message.id": "1", "vukem.job.attempt": "1",
		})},
		{"process default", trace.SpanKindConsumer, true, base(map[string]string{
			"messaging.operation.type": "process", "messaging.message.id": "2", "vukem.job.attempt": "3",
			"error.type": "_OTHER",
		})},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("spans (-want +got):\n%s", diff)
	}

	wantDur := []point{{Attrs: base(nil), N: 1}, {Attrs: base(map[string]string{"error.type": "_OTHER"}), N: 1}}
	if diff := cmp.Diff(wantDur, points(t, r, otel.MetricJobDuration)); diff != "" {
		t.Errorf("%s (-want +got):\n%s", otel.MetricJobDuration, diff)
	}

	wantFail := []point{{Attrs: base(map[string]string{"error.type": "_OTHER"}), N: 1}}
	if diff := cmp.Diff(wantFail, points(t, r, otel.MetricJobFailures)); diff != "" {
		t.Errorf("%s (-want +got):\n%s", otel.MetricJobFailures, diff)
	}
}

type failTransport struct{}

func (failTransport) Send(context.Context, *mail.Message) error { return errors.New("smtp down") }

func TestObserver_MailTransport(t *testing.T) {
	t.Parallel()

	tp, sr := newRecorder(t)
	ob := otel.NewObserver(tp)
	msg := &mail.Message{To: []string{"ana@example.com"}, Subject: "Reset your password"}

	arr := mail.NewArray()
	if err := ob.MailTransport(arr).Send(t.Context(), msg); err != nil || len(arr.Sent()) != 1 {
		t.Fatalf("Send() = %v, sent %d", err, len(arr.Sent()))
	}

	if err := ob.MailTransport(failTransport{}).Send(t.Context(), msg); err == nil {
		t.Fatal("Send() error lost")
	}

	spans := sr.Ended()
	if len(spans) != 2 || spans[0].Name() != "mail send" || spans[0].SpanKind() != trace.SpanKindClient {
		t.Fatalf("spans = %v", spans)
	}

	if spans[0].Status().Code == codes.Error || spans[1].Status().Code != codes.Error {
		t.Errorf("status = %v, %v; want ok, error", spans[0].Status(), spans[1].Status())
	}

	for _, s := range spans {
		for k, v := range attrs(s) {
			if strings.Contains(v, "ana@") || strings.Contains(v, "password") {
				t.Errorf("mail span attribute %s = %q leaks the message", k, v)
			}
		}
	}
}

func TestObserver_Transport(t *testing.T) {
	t.Parallel()

	var gotHeader []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = append(gotHeader, r.Header.Get("Traceparent"))

		if r.URL.Path == "/missing" {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	tp, sr := newRecorder(t)
	mp, r := newMeter(t)
	ob := otel.NewObserver(tp, otel.WithMeterProvider(mp))
	client := &http.Client{Transport: ob.Transport(nil)}

	for _, path := range []string{"/ok?token=secret", "/missing"} {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+path, http.NoBody)
		if err != nil {
			t.Fatal(err)
		}

		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}

		_ = resp.Body.Close()
	}

	spans := sr.Ended()
	if len(spans) != 2 || spans[0].Name() != "GET" || spans[0].SpanKind() != trace.SpanKindClient {
		t.Fatalf("spans = %v", spans)
	}

	a := attrs(spans[0])
	if a["http.response.status_code"] != "200" || a["server.address"] != "127.0.0.1" || a["server.port"] == "" {
		t.Errorf("attributes = %v", a)
	}

	if spans[0].Status().Code == codes.Error || spans[1].Status().Code != codes.Error {
		t.Errorf("status = %v, %v; want ok, error", spans[0].Status(), spans[1].Status())
	}

	if got := points(t, r, otel.MetricHTTPClientDuration); len(got) != 2 || got[1].Attrs["error.type"] != "404" {
		t.Errorf("%s = %v", otel.MetricHTTPClientDuration, got)
	}

	if diff := cmp.Diff([]string{"", ""}, gotHeader); diff != "" {
		t.Errorf("traceparent sent without WithClientPropagation (-want +got):\n%s", diff)
	}
}

func TestSecurity_A09_OTelClientSpansOmitURLAndContext(t *testing.T) {
	t.Parallel()

	var got http.Header

	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = r.Header.Clone() }))
	t.Cleanup(srv.Close)

	for _, inject := range []bool{false, true} {
		tp, sr := newRecorder(t)

		var opts []otel.Option
		if inject {
			opts = append(opts, otel.WithClientPropagation())
		}

		ctx, parent := tp.Tracer("test").Start(t.Context(), "request")

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/reset/tok3n?key=s3cret", http.NoBody)
		if err != nil {
			t.Fatal(err)
		}

		resp, err := (&http.Client{Transport: otel.NewObserver(tp, opts...).Transport(nil)}).Do(req)
		if err != nil {
			t.Fatal(err)
		}

		_ = resp.Body.Close()
		parent.End()

		if req.Header.Get("Traceparent") != "" {
			t.Error("Transport modified the caller's request")
		}

		if has := got.Get("Traceparent") != ""; has != inject {
			t.Errorf("WithClientPropagation=%v: traceparent sent = %v", inject, has)
		}

		for _, s := range sr.Ended() {
			for k, v := range attrs(s) {
				if strings.Contains(v, "tok3n") || strings.Contains(v, "s3cret") {
					t.Errorf("client span attribute %s = %q leaks the URL", k, v)
				}
			}
		}
	}
}

var _ sdktrace.SpanExporter = keepExporter{}
