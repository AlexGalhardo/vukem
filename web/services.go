package web

import (
	"context"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
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
// pass secrets (a config.Secret is logged masked) or personal data such as
// names and emails. The dispatched record carries the client IP, which is
// personal data: stored audit records need a retention period.
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

	base := []any{"event", event, "method", r.Method, "path", LogPath(r)}
	if id := RequestID(r.Context()); id != "" {
		base = append(base, "request_id", id)
	}

	log.Log(r.Context(), level, msg, append(base, attrs...)...)

	if s.Events == nil {
		return
	}

	e := security.Event{
		Name: event, At: time.Now().UTC(), Method: r.Method, Path: LogPath(r),
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

type logAttrsKey struct{}

// LogAttrs collects the attributes that AddLogAttrs adds during a request,
// for the request line. It is safe for concurrent use.
type LogAttrs struct {
	mu      sync.Mutex
	attrs   []slog.Attr
	secrets []secretParam
}

type secretParam struct{ name, value string }

// WithLogAttrs returns a context whose AddLogAttrs calls land in the
// returned holder. middleware.Logger installs one per request and appends
// its attributes to the request line once the response is written.
func WithLogAttrs(ctx context.Context) (context.Context, *LogAttrs) {
	l := &LogAttrs{}

	return context.WithValue(ctx, logAttrsKey{}, l), l
}

// AddLogAttrs adds attrs to the request line of the request carrying ctx
// (a route pattern, a hashed user id). Without a holder (no
// middleware.Logger) it does nothing. Never add secrets or tokens.
func AddLogAttrs(ctx context.Context, attrs ...slog.Attr) {
	if l, ok := ctx.Value(logAttrsKey{}).(*LogAttrs); ok {
		l.mu.Lock()
		l.attrs = append(l.attrs, attrs...)
		l.mu.Unlock()
	}
}

// RedactLogPath hides value, the value of path parameter name, from the
// path every log line of this request writes (the request line, error
// and security-event logs): LogPath shows it as "{name}". The router calls
// it for Route.SecretParams. It returns ctx, or a context carrying a new
// holder when ctx has none (no middleware.Logger), so pass the result on;
// later calls on the result return it unchanged.
func RedactLogPath(ctx context.Context, name, value string) context.Context {
	l, ok := ctx.Value(logAttrsKey{}).(*LogAttrs)
	if !ok {
		ctx, l = WithLogAttrs(ctx)
	}

	if value == "" {
		return ctx
	}

	l.mu.Lock()
	l.secrets = append(l.secrets, secretParam{name, value})
	l.mu.Unlock()

	return ctx
}

// LogPath returns r.URL.Path with the values RedactLogPath hid replaced by
// "{name}"; logs write it instead of the raw path.
func LogPath(r *http.Request) string {
	if l, ok := r.Context().Value(logAttrsKey{}).(*LogAttrs); ok {
		return l.Path(r.URL.Path)
	}

	return r.URL.Path
}

// Path returns p with the values RedactLogPath hid replaced by "{name}":
// a whole segment equal to the value, or every occurrence of a value that
// spans segments (a {name...} wildcard).
func (l *LogAttrs) Path(p string) string {
	l.mu.Lock()
	defer l.mu.Unlock()

	for _, s := range l.secrets {
		mask := "{" + s.name + "}"
		if strings.Contains(s.value, "/") {
			p = strings.ReplaceAll(p, s.value, mask)
			continue
		}

		segs := strings.Split(p, "/")
		for i, seg := range segs {
			if seg == s.value {
				segs[i] = mask
			}
		}

		p = strings.Join(segs, "/")
	}

	return p
}

// Attrs returns a copy of the attributes added so far.
func (l *LogAttrs) Attrs() []slog.Attr {
	l.mu.Lock()
	defer l.mu.Unlock()

	return slices.Clone(l.attrs)
}
