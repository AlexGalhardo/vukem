// Package web provides the request context (*Ctx) used by Vukem handlers:
// request helpers, typed input, responses, and the central error handler.
//
// A handler has the signature func(*Ctx) error. Returning an error hands it
// to the error handler, which renders the right response for the client
// (HTML, redirect with flashed errors, HTMX fragment or problem+json).
//
// [Parse], [ParseQuery] and [Bind] turn request input into typed values
// through z schemas; [Services] carries the shared dependencies (URL
// generator, view renderer, logger, authorizer) the router hands to every
// [Ctx].
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/http.md.
package web

import (
	"context"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/netip"
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
	// DebugOut receives a colored report of every 5xx error when Debug is
	// on (the app sets os.Stderr in development).
	DebugOut io.Writer
	// TrustedProxies lists CIDRs whose X-Forwarded-For is honoured by IP().
	TrustedProxies []*net.IPNet
	// ClientIPHeader, when set, names a header (such as X-Real-IP) that a
	// trusted proxy sets to the client IP. IP() then reads it instead of
	// X-Forwarded-For, only from TrustedProxies peers; a missing, repeated
	// or non-IP value falls back to the peer address.
	ClientIPHeader string
	// TrustForwardedProto makes IsHTTPS honour X-Forwarded-Proto: https,
	// only from TrustedProxies peers (TRUST_FORWARDED_PROTO).
	TrustForwardedProto bool
	// IPv6Prefix is the prefix length per-IP keys (ratelimit.ByIP) group
	// IPv6 clients by, since one client usually owns a whole /64; 0 or any
	// value outside 1-128 means 64. IPv4 (and IPv4-mapped IPv6) keys stay
	// per address.
	IPv6Prefix int
	// ProblemBaseURL prefixes RFC 9457 problem "type" URIs; empty means
	// "about:blank".
	ProblemBaseURL string
	// CheckResponses makes JSONWith validate outgoing data against its
	// schema (enabled outside production by the app).
	CheckResponses bool
	// Events receives a security.Event for every SecurityEvent (alerting)
	// and a security.Audit for every AuditEvent.
	Events EventDispatcher
	// Authorizer backs Ctx.Authorize and the "can" template function
	// (an authz.Gate). Without one, every authorization check denies.
	Authorizer Authorizer
	// ErrorPage lets a frontend adapter render DefaultErrorHandler's error
	// page in its own format (vukem.WithInertia installs
	// inertia.Inertia.ErrorPage, an Inertia page for Inertia requests).
	// Nil keeps the HTML page.
	ErrorPage ErrorPageFunc
}

// ErrorPageFunc renders an error page described by e. It reports whether
// it wrote a response; false falls back to the HTML page.
type ErrorPageFunc func(c *Ctx, e ErrorPage) bool

// ErrorPage is the client-safe description of an error that
// DefaultErrorHandler hands to Services.ErrorPage. Its text never holds a
// 5xx cause or message, nor a wrapped cause (CWE-209).
type ErrorPage struct {
	// Status is the HTTP status.
	Status int
	// Title is the status text, or "Validation failed" for a 4xx with
	// field errors.
	Title string
	// Detail is the error's own (translated) message for 4xx; empty when
	// it would repeat Title, and always empty for 5xx.
	Detail string
	// Errors holds the field errors of a 4xx validation error (all
	// messages per field); nil otherwise.
	Errors map[string][]string
}

// Ctx is the per-request context. It must not be used after the handler
// returns or from other goroutines.
type Ctx struct {
	w *ResponseWriter
	// rw backs w when the writer is not already a *ResponseWriter, so a
	// request allocates one object instead of two.
	rw    ResponseWriter
	r     *http.Request
	s     *Services
	route string
}

// NewCtx returns a Ctx for w and r. Routers call it; tests may too.
func NewCtx(w http.ResponseWriter, r *http.Request, s *Services) *Ctx {
	if s == nil {
		s = &Services{}
	}

	c := &Ctx{r: r, s: s}

	if rw, ok := w.(*ResponseWriter); ok {
		c.w = rw
	} else {
		c.rw.ResponseWriter = w
		c.w = &c.rw
	}

	return c
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
	return IsJSONMediaType(c.r.Header.Get("Content-Type"))
}

// IsJSONMediaType reports whether a Content-Type value is application/json
// or an application/*+json type. Parameters are ignored, so a CORS-simple
// "text/plain; x=+json" body is never taken for JSON.
func IsJSONMediaType(contentType string) bool {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}

	return mt == "application/json" || strings.HasPrefix(mt, "application/") && strings.HasSuffix(mt, "+json")
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

// IP returns the client IP. X-Forwarded-For (or ClientIPHeader when set)
// is honoured only when the direct peer is a trusted proxy
// (docs/security.md, A01/A02).
func (c *Ctx) IP() string {
	host, _, err := net.SplitHostPort(c.r.RemoteAddr)
	if err != nil {
		host = c.r.RemoteAddr
	}

	if !c.trusted(host) {
		return host
	}

	if c.s.ClientIPHeader != "" {
		// One line holding one IP: anything else may carry client input.
		vals := c.r.Header.Values(c.s.ClientIPHeader)
		if len(vals) != 1 {
			return host
		}

		ip, ok := canonicalIP(vals[0])
		if !ok {
			return host
		}

		return ip
	}

	// Every header line counts: a proxy may append its own line instead of
	// extending the client's, so the first line alone is client-controlled.
	fwd := strings.Join(c.r.Header.Values("X-Forwarded-For"), ",")
	if fwd == "" {
		return host
	}

	// Walk right to left, skipping trusted proxies; an entry that is not an
	// IP means the chain cannot be trusted, so the peer address is used.
	parts := strings.Split(fwd, ",")
	for _, p := range slices.Backward(parts) {
		ip, ok := canonicalIP(p)
		if !ok {
			return host
		}

		if !c.trusted(ip) {
			return ip
		}
	}

	ip, _ := canonicalIP(parts[0])

	return ip
}

// canonicalIP parses s and returns it in one spelling (IPv4-mapped IPv6
// unmapped, IPv6 lowercased and compressed), so rate-limit keys for the
// same client never differ.
func canonicalIP(s string) (string, bool) {
	addr, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil {
		return "", false
	}

	return addr.Unmap().String(), true
}

// IsHTTPS reports whether the client reached the app over HTTPS: the
// connection is TLS, or TrustForwardedProto is set and a TrustedProxies
// peer sent a single X-Forwarded-Proto: https line (a second line may come
// from the client).
func (c *Ctx) IsHTTPS() bool {
	if c.r.TLS != nil {
		return true
	}

	if !c.s.TrustForwardedProto {
		return false
	}

	host, _, err := net.SplitHostPort(c.r.RemoteAddr)
	if err != nil {
		host = c.r.RemoteAddr
	}

	vals := c.r.Header.Values("X-Forwarded-Proto")

	return len(vals) == 1 && strings.TrimSpace(vals[0]) == "https" && c.trusted(host)
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
