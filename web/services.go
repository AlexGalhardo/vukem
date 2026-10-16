package web

import (
	"context"
	"log/slog"
	"net/http"
)

type servicesKey struct{}

// WithServices stores s in ctx so middleware can render errors consistently.
func WithServices(ctx context.Context, s *Services) context.Context {
	return context.WithValue(ctx, servicesKey{}, s)
}

// ServicesFrom returns the services stored in ctx, or an empty value.
func ServicesFrom(ctx context.Context) *Services {
	if s, ok := ctx.Value(servicesKey{}).(*Services); ok {
		return s
	}

	return &Services{}
}

// Fail renders err through the error handler. Middleware uses it to reject a
// request (401, 413, 415, 429...) with the same format as handlers.
func Fail(w http.ResponseWriter, r *http.Request, err error) {
	NewCtx(w, r, ServicesFrom(r.Context())).HandleError(err)
}

// SecurityEvent logs a security-relevant event at WARN with a stable
// "event" attribute (docs/security.md, A09).
func SecurityEvent(r *http.Request, event string, attrs ...any) {
	log := ServicesFrom(r.Context()).Log
	if log == nil {
		log = slog.Default()
	}

	base := []any{"event", event, "method", r.Method, "path", r.URL.Path}
	if id := RequestID(r.Context()); id != "" {
		base = append(base, "request_id", id)
	}

	log.WarnContext(r.Context(), "security event", append(base, attrs...)...)
}
