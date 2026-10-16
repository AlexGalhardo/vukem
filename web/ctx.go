// Package web provides the request context (*Ctx) used by Vukem handlers:
// request helpers, typed input, responses, and the central error handler.
//
// A handler has the signature func(*Ctx) error. Returning an error hands it
// to the error handler, which renders the right response for the client
// (HTML, redirect with flashed errors, HTMX fragment or problem+json).
package web

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/AlexGalhardo/vukem/errs"
)

// HandlerFunc handles a request. Returned errors go to the error handler.
type HandlerFunc func(c *Ctx) error

// Map is a convenience alias for template and JSON data.
type Map = map[string]any

// URLGenerator builds URLs for named routes.
type URLGenerator interface {
	URL(name string, params ...any) (string, error)
}

// ErrorHandler writes the response for an error returned by a handler.
type ErrorHandler func(c *Ctx, err error)

// Services are the shared dependencies every Ctx can reach. The router owns
// one Services value; fields may be nil when the feature is not installed.
type Services struct {
	URLs   URLGenerator
	Views  Renderer
	Errors ErrorHandler
	Log    *slog.Logger
	// Debug enables detailed error pages (never in production).
	Debug bool
	// TrustedProxies lists CIDRs whose X-Forwarded-For is honoured by IP().
	TrustedProxies []*net.IPNet
	// ProblemBaseURL prefixes RFC 9457 problem "type" URIs; empty means
	// "about:blank".
	ProblemBaseURL string
	// CheckResponses makes JSONWith validate outgoing data against its
	// schema (enabled outside production by the app).
	CheckResponses bool
	// Authorizer backs Ctx.Authorize and the "can" template function
	// (an authz.Gate). Without one, every authorization check denies.
	Authorizer Authorizer
}

// Ctx is the per-request context. It must not be used after the handler
// returns or from other goroutines.
type Ctx struct {
	w     *ResponseWriter
	r     *http.Request
	s     *Services
	route string
}

// NewCtx returns a Ctx for w and r. Routers call it; tests may too.
func NewCtx(w http.ResponseWriter, r *http.Request, s *Services) *Ctx {
	if s == nil {
		s = &Services{}
	}

	rw, ok := w.(*ResponseWriter)
	if !ok {
		rw = &ResponseWriter{ResponseWriter: w}
	}

	return &Ctx{w: rw, r: r, s: s}
}

// Request returns the underlying request.
func (c *Ctx) Request() *http.Request { return c.r }

// SetRequest replaces the request (e.g. after adding context values).
func (c *Ctx) SetRequest(r *http.Request) { c.r = r }

// Writer returns the response writer.
func (c *Ctx) Writer() *ResponseWriter { return c.w }

// Context returns the request context.
func (c *Ctx) Context() context.Context { return c.r.Context() }

// Services returns the shared services.
func (c *Ctx) Services() *Services { return c.s }

// Log returns the request logger.
func (c *Ctx) Log() *slog.Logger {
	if c.s.Log != nil {
		return c.s.Log
	}

	return slog.Default()
}

// RouteName returns the name of the matched route, if any.
func (c *Ctx) RouteName() string { return c.route }

// SetRouteName is used by routers to record the matched route.
func (c *Ctx) SetRouteName(name string) { c.route = name }

// Method returns the request method.
func (c *Ctx) Method() string { return c.r.Method }

// Path returns the request path.
func (c *Ctx) Path() string { return c.r.URL.Path }

// Param returns a path parameter ({name} in the route pattern).
func (c *Ctx) Param(name string) string { return c.r.PathValue(name) }

// ParamInt returns a path parameter as int64, or a 404 error when it is not
// a valid integer (a non-numeric id names no resource).
func (c *Ctx) ParamInt(name string) (int64, error) {
	n, err := strconv.ParseInt(c.r.PathValue(name), 10, 64)
	if err != nil {
		return 0, errs.ErrNotFound
	}

	return n, nil
}

// Query returns a query string value.
func (c *Ctx) Query(name string) string { return c.r.URL.Query().Get(name) }

// QueryInt returns a query value as int, or def when absent or invalid.
func (c *Ctx) QueryInt(name string, def int) int {
	n, err := strconv.Atoi(c.r.URL.Query().Get(name))
	if err != nil {
		return def
	}

	return n
}

// QueryBool reports whether a query value is a true-like value.
func (c *Ctx) QueryBool(name string) bool {
	b, _ := strconv.ParseBool(c.r.URL.Query().Get(name))
	return b
}

// Header returns a request header.
func (c *Ctx) Header(name string) string { return c.r.Header.Get(name) }

// Cookie returns a request cookie value, or "" when absent.
func (c *Ctx) Cookie(name string) string {
	ck, err := c.r.Cookie(name)
	if err != nil {
		return ""
	}

	return ck.Value
}

// SetCookie adds a Set-Cookie header.
func (c *Ctx) SetCookie(ck *http.Cookie) { http.SetCookie(c.w, ck) }

// SetHeader sets a response header.
func (c *Ctx) SetHeader(name, value string) { c.w.Header().Set(name, value) }

// IsJSON reports whether the request body is JSON.
func (c *Ctx) IsJSON() bool {
	ct := c.r.Header.Get("Content-Type")
	return strings.HasPrefix(ct, "application/json") || strings.Contains(ct, "+json")
}

// WantsJSON reports whether the client prefers a JSON response: it accepts
// JSON but not HTML, or sent a JSON body without stating a preference.
func (c *Ctx) WantsJSON() bool {
	accept := c.r.Header.Get("Accept")
	if strings.Contains(accept, "json") && !strings.Contains(accept, "text/html") {
		return true
	}

	return (accept == "" || accept == "*/*") && c.IsJSON()
}

// IsHTMX reports whether the request was made by htmx.
func (c *Ctx) IsHTMX() bool { return c.r.Header.Get("HX-Request") == "true" }

// IP returns the client IP. X-Forwarded-For is honoured only when the
// direct peer is a trusted proxy (docs/security.md, A01/A02).
func (c *Ctx) IP() string {
	host, _, err := net.SplitHostPort(c.r.RemoteAddr)
	if err != nil {
		host = c.r.RemoteAddr
	}

	if !c.trusted(host) {
		return host
	}

	fwd := c.r.Header.Get("X-Forwarded-For")
	if fwd == "" {
		return host
	}

	// Walk right to left, skipping trusted proxies.
	parts := strings.Split(fwd, ",")
	for _, p := range slices.Backward(parts) {
		if ip := strings.TrimSpace(p); !c.trusted(ip) {
			return ip
		}
	}

	return strings.TrimSpace(parts[0])
}

func (c *Ctx) trusted(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}

	for _, n := range c.s.TrustedProxies {
		if n.Contains(ip) {
			return true
		}
	}

	return false
}

// URL builds the URL for a named route.
func (c *Ctx) URL(name string, params ...any) (string, error) {
	if c.s.URLs == nil {
		return "", errs.Wrap(http.StatusInternalServerError, errNoURLs)
	}

	return c.s.URLs.URL(name, params...)
}

// MustURL is URL for templates and code paths where the route is known to
// exist; it returns "#" and logs on error instead of panicking.
func (c *Ctx) MustURL(name string, params ...any) string {
	u, err := c.URL(name, params...)
	if err != nil {
		c.Log().ErrorContext(c.Context(), "url generation failed", "route", name, "err", err)
		return "#"
	}

	return u
}

// HandleError passes err to the error handler.
func (c *Ctx) HandleError(err error) {
	if c.s.Errors != nil {
		c.s.Errors(c, err)
		return
	}

	DefaultErrorHandler(c, err)
}

// Serve runs h with c and routes a returned error to the error handler.
func (c *Ctx) Serve(h HandlerFunc) {
	if err := h(c); err != nil {
		c.HandleError(err)
	}
}
