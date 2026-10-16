package otel_test

import (
	"context"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/AlexGalhardo/vukem/drivers/otel"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

func newRecorder(t *testing.T) (*sdktrace.TracerProvider, *tracetest.SpanRecorder) {
	t.Helper()

	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	return tp, sr
}

func onlySpan(t *testing.T, sr *tracetest.SpanRecorder) sdktrace.ReadOnlySpan {
	t.Helper()

	spans := sr.Ended()
	if len(spans) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(spans))
	}

	return spans[0]
}

func attrs(s sdktrace.ReadOnlySpan) map[string]string {
	m := map[string]string{}
	for _, kv := range s.Attributes() {
		m[string(kv.Key)] = kv.Value.String()
	}

	return m
}

func testMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /users/{id}", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /boom", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	mux.HandleFunc("/any", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})

	return mux
}

func TestMiddleware_ServerSpanPerRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		method     string
		target     string
		wantName   string
		wantAttrs  map[string]string
		wantStatus codes.Code
	}{
		{
			name:     "route pattern, never the raw path",
			method:   http.MethodGet,
			target:   "https://example.com:8443/users/42?token=secret",
			wantName: "GET /users/{id}",
			wantAttrs: map[string]string{
				"http.request.method":       "GET",
				"http.route":                "/users/{id}",
				"http.response.status_code": "200",
				"url.scheme":                "https",
				"server.address":            "example.com",
				"server.port":               "8443",
				"network.protocol.version":  "1.1",
				"user_agent.original":       "vukem-test",
			},
			wantStatus: codes.Unset,
		},
		{
			name:     "exact root pattern",
			method:   http.MethodGet,
			target:   "http://example.com/",
			wantName: "GET /",
			wantAttrs: map[string]string{
				"http.request.method":       "GET",
				"http.route":                "/",
				"http.response.status_code": "204",
				"url.scheme":                "http",
				"server.address":            "example.com",
				"network.protocol.version":  "1.1",
				"user_agent.original":       "vukem-test",
			},
			wantStatus: codes.Unset,
		},
		{
			name:     "5xx is an error",
			method:   http.MethodPost,
			target:   "http://example.com/boom",
			wantName: "POST /boom",
			wantAttrs: map[string]string{
				"http.request.method":       "POST",
				"http.route":                "/boom",
				"http.response.status_code": "503",
				"error.type":                "503",
				"url.scheme":                "http",
				"server.address":            "example.com",
				"network.protocol.version":  "1.1",
				"user_agent.original":       "vukem-test",
			},
			wantStatus: codes.Error,
		},
		{
			name:     "4xx is not a server error",
			method:   http.MethodGet,
			target:   "http://example.com/nope/123",
			wantName: "GET",
			wantAttrs: map[string]string{
				"http.request.method":       "GET",
				"http.response.status_code": "404",
				"url.scheme":                "http",
				"server.address":            "example.com",
				"network.protocol.version":  "1.1",
				"user_agent.original":       "vukem-test",
			},
			wantStatus: codes.Unset,
		},
		{
			name:     "unknown method has bounded cardinality",
			method:   "PURGE",
			target:   "http://example.com/any",
			wantName: "HTTP /any",
			wantAttrs: map[string]string{
				"http.request.method":       "_OTHER",
				"http.route":                "/any",
				"http.response.status_code": "418",
				"url.scheme":                "http",
				"server.address":            "example.com",
				"network.protocol.version":  "1.1",
				"user_agent.original":       "vukem-test",
			},
			wantStatus: codes.Unset,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tp, sr := newRecorder(t)
			h := otel.Middleware(tp)(testMux())

			req := httptest.NewRequest(tt.method, tt.target, http.NoBody)
			req.Header.Set("User-Agent", "vukem-test")
			h.ServeHTTP(httptest.NewRecorder(), req)

			s := onlySpan(t, sr)
			if s.Name() != tt.wantName {
				t.Errorf("span name = %q, want %q", s.Name(), tt.wantName)
			}

			if s.SpanKind() != trace.SpanKindServer {
				t.Errorf("span kind = %v, want server", s.SpanKind())
			}

			if diff := cmp.Diff(tt.wantAttrs, attrs(s)); diff != "" {
				t.Errorf("attributes (-want +got):\n%s", diff)
			}

			if s.Status().Code != tt.wantStatus {
				t.Errorf("status = %v, want %v", s.Status().Code, tt.wantStatus)
			}

			if s.Parent().IsValid() {
				t.Errorf("parent = %v, want a root span", s.Parent())
			}
		})
	}
}

func TestMiddleware_ExtractsTraceparentAndBaggage(t *testing.T) {
	t.Parallel()

	tp, sr := newRecorder(t)

	var (
		member  string
		current trace.SpanContext
	)

	h := otel.Middleware(tp)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		member = baggage.FromContext(r.Context()).Member("tenant").Value()
		current = trace.SpanContextFromContext(r.Context())
	}))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	req.Header.Set("baggage", "tenant=acme")
	h.ServeHTTP(httptest.NewRecorder(), req)

	s := onlySpan(t, sr)

	if got, want := s.Parent().TraceID().String(), "4bf92f3577b34da6a3ce929d0e0e4736"; got != want {
		t.Errorf("parent trace id = %s, want %s", got, want)
	}

	if got, want := s.Parent().SpanID().String(), "00f067aa0ba902b7"; got != want {
		t.Errorf("parent span id = %s, want %s", got, want)
	}

	if !s.Parent().IsRemote() {
		t.Error("parent is not remote")
	}

	if s.SpanContext().TraceID() != s.Parent().TraceID() {
		t.Errorf("trace id = %s, want the incoming one", s.SpanContext().TraceID())
	}

	if current.SpanID() != s.SpanContext().SpanID() {
		t.Errorf("handler span = %s, want the server span %s", current.SpanID(), s.SpanContext().SpanID())
	}

	if member != "acme" {
		t.Errorf("baggage tenant = %q, want acme", member)
	}
}

func TestMiddleware_PanicMarksErrorAndRepanics(t *testing.T) {
	t.Parallel()

	tp, sr := newRecorder(t)
	h := otel.Middleware(tp)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("kaboom") }))

	func() {
		defer func() {
			if p := recover(); p != "kaboom" {
				t.Errorf("recover() = %v, want kaboom", p)
			}
		}()

		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", http.NoBody))
	}()

	s := onlySpan(t, sr)
	if s.Status().Code != codes.Error {
		t.Errorf("status = %v, want error", s.Status().Code)
	}

	if got := attrs(s)["error.type"]; got != "panic" {
		t.Errorf("error.type = %q, want panic", got)
	}
}

type cloneKey struct{}

// cloneRequest stands for middleware (request id, sessions) that replaces
// the request, hiding the ServeMux pattern from outer middleware.
func cloneRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), cloneKey{}, 1)))
	})
}

func TestMiddleware_VukemRouterServerAndRouteInstall(t *testing.T) {
	t.Parallel()

	tp, sr := newRecorder(t)
	mw := otel.Middleware(tp)

	r := router.New(nil)
	r.UseServer(mw, cloneRequest)
	r.Use(mw)
	r.Get("/posts/{slug}", func(c *web.Ctx) error { return c.String(http.StatusInternalServerError, "x") })

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/posts/hello-world", http.NoBody))

	s := onlySpan(t, sr)
	if s.Name() != "GET /posts/{slug}" {
		t.Errorf("span name = %q, want %q", s.Name(), "GET /posts/{slug}")
	}

	got := attrs(s)
	if got["http.route"] != "/posts/{slug}" || got["http.response.status_code"] != "500" {
		t.Errorf("attributes = %v, want route /posts/{slug} and status 500", got)
	}

	if s.Status().Code != codes.Error {
		t.Errorf("status = %v, want error", s.Status().Code)
	}
}

func TestMiddleware_KeepsFlusher(t *testing.T) {
	t.Parallel()

	tp, _ := newRecorder(t)

	var flushed error

	h := otel.Middleware(tp)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		flushed = http.NewResponseController(w).Flush()
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", http.NoBody))

	if flushed != nil || !rec.Flushed {
		t.Errorf("Flush() = %v, flushed %v; want nil, true", flushed, rec.Flushed)
	}
}

func TestDefaultPropagator_Fields(t *testing.T) {
	t.Parallel()

	got := otel.DefaultPropagator().Fields()
	slices.Sort(got)

	want := []string{"baggage", "traceparent", "tracestate"}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Fields() (-want +got):\n%s", diff)
	}
}

func TestSecurity_A09_OTelSpansOmitPathAndQuery(t *testing.T) {
	t.Parallel()

	tp, sr := newRecorder(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /reset/{token}", func(http.ResponseWriter, *http.Request) {})

	req := httptest.NewRequest(http.MethodGet, "/reset/s3cr3t-token?signature=abc123", http.NoBody)
	otel.Middleware(tp)(mux).ServeHTTP(httptest.NewRecorder(), req)

	s := onlySpan(t, sr)
	for _, v := range append([]string{s.Name()}, slices.Collect(maps.Values(attrs(s)))...) {
		if strings.Contains(v, "s3cr3t-token") || strings.Contains(v, "abc123") {
			t.Errorf("span leaks the path or query: %q", v)
		}
	}
}
