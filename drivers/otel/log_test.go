package otel_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/drivers/otel"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

// lockedBuffer is a bytes.Buffer safe for the logger and the test.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.b.Write(p)
}

func (l *lockedBuffer) records(t *testing.T) []map[string]any {
	t.Helper()

	l.mu.Lock()
	defer l.mu.Unlock()

	var out []map[string]any

	for line := range strings.SplitSeq(strings.TrimSpace(l.b.String()), "\n") {
		if line == "" {
			continue
		}

		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}

		out = append(out, m)
	}

	return out
}

func TestLogHandler(t *testing.T) {
	t.Parallel()

	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(tracetest.NewSpanRecorder()))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	ctx, span := tp.Tracer("test").Start(t.Context(), "work")
	defer span.End()

	sc := span.SpanContext()

	tests := []struct {
		name string
		ctx  context.Context
		log  func(log *slog.Logger, ctx context.Context)
		want map[string]any
	}{
		{
			name: "span in context",
			ctx:  ctx,
			log:  func(log *slog.Logger, ctx context.Context) { log.InfoContext(ctx, "hi", "k", "v") },
			want: map[string]any{
				"msg": "hi", "k": "v",
				otel.TraceIDKey: sc.TraceID().String(), otel.SpanIDKey: sc.SpanID().String(),
			},
		},
		{
			name: "derived handler keeps correlating",
			ctx:  ctx,
			log:  func(log *slog.Logger, ctx context.Context) { log.With("job", "mail").ErrorContext(ctx, "failed") },
			want: map[string]any{
				"msg": "failed", "job": "mail",
				otel.TraceIDKey: sc.TraceID().String(), otel.SpanIDKey: sc.SpanID().String(),
			},
		},
		{
			name: "no span",
			ctx:  t.Context(),
			log:  func(log *slog.Logger, ctx context.Context) { log.InfoContext(ctx, "hi") },
			want: map[string]any{"msg": "hi"},
		},
		{
			name: "invalid span context",
			ctx:  trace.ContextWithSpanContext(t.Context(), trace.SpanContext{}),
			log:  func(log *slog.Logger, ctx context.Context) { log.InfoContext(ctx, "hi") },
			want: map[string]any{"msg": "hi"},
		},
		{
			name: "grouped",
			ctx:  ctx,
			log:  func(log *slog.Logger, ctx context.Context) { log.WithGroup("g").InfoContext(ctx, "hi") },
			want: map[string]any{
				"msg": "hi",
				"g":   map[string]any{otel.TraceIDKey: sc.TraceID().String(), otel.SpanIDKey: sc.SpanID().String()},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf lockedBuffer

			base := slog.NewJSONHandler(&buf, &slog.HandlerOptions{
				ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
					if len(groups) == 0 && (a.Key == slog.TimeKey || a.Key == slog.LevelKey) {
						return slog.Attr{}
					}

					return a
				},
			})
			tt.log(slog.New(otel.LogHandler(base)), tt.ctx)

			got := buf.records(t)
			if len(got) != 1 {
				t.Fatalf("records = %d, want 1", len(got))
			}

			if diff := cmp.Diff(tt.want, got[0]); diff != "" {
				t.Errorf("record (-want +got):\n%s", diff)
			}
		})
	}
}

func TestLogHandler_EnabledDelegates(t *testing.T) {
	t.Parallel()

	h := otel.LogHandler(slog.NewJSONHandler(&lockedBuffer{}, &slog.HandlerOptions{Level: slog.LevelWarn}))
	if h.Enabled(t.Context(), slog.LevelInfo) {
		t.Error("Enabled(INFO) = true, want false")
	}
}

// TestProvider_CorrelatesRequestLogs: with tracing on, the request log
// record carries the trace and span ids of the exported server span.
func TestProvider_CorrelatesRequestLogs(t *testing.T) {
	t.Parallel()

	exp := keepExporter{tracetest.NewInMemoryExporter()}
	p := otel.New(otel.Config{Exporter: exp})

	var buf lockedBuffer

	app, err := vukem.New(
		vukem.WithSource(config.MapSource(map[string]string{
			"APP_KEY": config.GenerateKey(), "APP_ENV": "development", "LOG_FORMAT": "json",
		})),
		vukem.WithLogOutput(&buf),
		vukem.WithStdout(io.Discard),
		vukem.WithProviders(p),
		vukem.WithRoutes(func(r *router.Router, app *vukem.App) error {
			if err := vukem.DefaultMiddleware(app, r); err != nil {
				return err
			}

			r.Get("/orders/{id}", func(c *web.Ctx) error {
				c.Log().InfoContext(c.Context(), "loading order")
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
		t.Fatalf("GET /orders/7 = %d, want 200", rec.Code)
	}

	if err := app.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}

	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("exported spans = %d, want 1", len(spans))
	}

	want := spans[0].SpanContext.TraceID().String()
	found := map[string]bool{}

	for _, r := range buf.records(t) {
		msg, _ := r["msg"].(string)
		if msg != "request" && msg != "loading order" {
			continue
		}

		found[msg] = true

		if r[otel.TraceIDKey] != want || r[otel.SpanIDKey] != spans[0].SpanContext.SpanID().String() {
			t.Errorf("%q record = %v, want trace_id %s and the server span id", msg, r, want)
		}
	}

	if !found["request"] || !found["loading order"] {
		t.Errorf("records found = %v, want the request log and the handler log", found)
	}
}
