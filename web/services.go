package web

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/AlexGalhardo/vukem/security"
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

type cspNonceKey struct{}

// WithCSPNonce stores the request's Content-Security-Policy nonce in ctx
// (middleware.SecureHeaders does this).
func WithCSPNonce(ctx context.Context, nonce string) context.Context {
	return context.WithValue(ctx, cspNonceKey{}, nonce)
}

// CSPNonce returns the request's CSP nonce, or "".
func CSPNonce(ctx context.Context) string {
	n, _ := ctx.Value(cspNonceKey{}).(string)
	return n
}

// Fail renders err through the error handler. Middleware uses it to reject a
// request (401, 413, 415, 429...) with the same format as handlers.
func Fail(w http.ResponseWriter, r *http.Request, err error) {
	NewCtx(w, r, ServicesFrom(r.Context())).HandleError(err)
}

// SecurityEvent logs a security-relevant event at WARN with a stable
// "event" attribute (docs/security.md, A09) and dispatches it as a
// security.Event.
func SecurityEvent(r *http.Request, event string, attrs ...any) {
	emit(r, slog.LevelWarn, "security event", event, attrs, func(e security.Event) any { return e })
}

// AuditEvent logs a routine audit record (an admin recorded a payout,
// changed a role) at INFO with the same envelope as SecurityEvent: a
// stable "event" attribute, method, path and request_id. It is dispatched
// as a security.Audit, so security alerts are not mixed with routine
// records. Pass the actor as an attribute ("actor_id", u.AuthID()); never
// pass secrets (a config.Secret is logged masked).
func AuditEvent(r *http.Request, event string, attrs ...any) {
	emit(r, slog.LevelInfo, "audit event", event, attrs, func(e security.Event) any { return security.Audit(e) })
}

// emit logs event at level and dispatches as(event) on the app's bus.
func emit(r *http.Request, level slog.Level, msg, event string, attrs []any, as func(security.Event) any) {
	s := ServicesFrom(r.Context())

	log := s.Log
	if log == nil {
		log = slog.Default()
	}

	base := []any{"event", event, "method", r.Method, "path", r.URL.Path}
	if id := RequestID(r.Context()); id != "" {
		base = append(base, "request_id", id)
	}

	log.Log(r.Context(), level, msg, append(base, attrs...)...)

	if s.Events == nil {
		return
	}

	e := security.Event{
		Name: event, At: time.Now().UTC(), Method: r.Method, Path: r.URL.Path,
		IP: NewCtx(nil, r, s).IP(), RequestID: RequestID(r.Context()),
		Attrs: map[string]any{},
	}

	for i := 0; i+1 < len(attrs); i += 2 {
		if k, ok := attrs[i].(string); ok {
			e.Attrs[k] = attrs[i+1]
		}
	}

	if err := s.Events.Dispatch(r.Context(), as(e)); err != nil {
		log.ErrorContext(r.Context(), msg+" listener failed", "event", event, "err", err)
	}
}

// EventDispatcher delivers security events to listeners (an events.Bus).
type EventDispatcher interface {
	Dispatch(ctx context.Context, event any) error
}
