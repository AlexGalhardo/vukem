package otel

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

// Log attribute keys LogHandler adds; they match the OpenTelemetry log
// data model field names, so collectors link records to traces.
const (
	TraceIDKey = "trace_id"
	SpanIDKey  = "span_id"
)

// LogHandler wraps next so that every record logged with a context that
// carries a valid span gets the TraceIDKey and SpanIDKey attributes (hex
// encoded), inside any group opened with WithGroup. Records without a span
// pass through unchanged. Log with the *Context methods (web.Ctx.Log()
// with c.Context()) for the span to be found; the provider installs it on
// app.Log when tracing is on.
func LogHandler(next slog.Handler) slog.Handler {
	return logHandler{next: next}
}

type logHandler struct {
	next slog.Handler
}

func (h logHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h logHandler) Handle(ctx context.Context, r slog.Record) error {
	if ctx != nil {
		if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
			r = r.Clone()
			r.AddAttrs(
				slog.String(TraceIDKey, sc.TraceID().String()),
				slog.String(SpanIDKey, sc.SpanID().String()),
			)
		}
	}

	return h.next.Handle(ctx, r)
}

func (h logHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return logHandler{next: h.next.WithAttrs(attrs)}
}

func (h logHandler) WithGroup(name string) slog.Handler {
	return logHandler{next: h.next.WithGroup(name)}
}
