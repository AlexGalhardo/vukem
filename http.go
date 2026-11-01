package vukem

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/netip"
	neturl "net/url"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AlexGalhardo/vukem/cache"
	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/csrf"
	"github.com/AlexGalhardo/vukem/devtools"
	"github.com/AlexGalhardo/vukem/i18n"
	"github.com/AlexGalhardo/vukem/inertia"
	"github.com/AlexGalhardo/vukem/middleware"
	"github.com/AlexGalhardo/vukem/openapi"
	"github.com/AlexGalhardo/vukem/ratelimit"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/session"
	"github.com/AlexGalhardo/vukem/web"
)

// HealthCheck reports whether a dependency (database, cache...) is healthy.
type HealthCheck func(ctx context.Context) error

// DefaultHealthCheckTimeout bounds a health check registered with
// AddHealthCheck.
const DefaultHealthCheckTimeout = 3 * time.Second

// errHealthCheckBusy reports a check whose previous run has not returned.
var errHealthCheckBusy = errors.New("vukem: health check: previous run still in progress")

type healthCheck struct {
	fn      HealthCheck
	timeout time.Duration
	// running is set while a goroutine executes fn, including one abandoned
	// at its timeout, so a hung dependency never accumulates goroutines.
	running atomic.Bool
}

type health struct {
	mu     sync.RWMutex
	checks map[string]*healthCheck
}

// namedHealthCheck is a check registered by WithHealthCheck, before there
// is an App to call AddHealthCheck on.
type namedHealthCheck struct {
	name  string
	check HealthCheck
}

// AddHealthCheck registers a readiness check, reported by ReadyHandler and
// GET /up?checks=1 and bounded by DefaultHealthCheckTimeout. A check with
// the same name replaces the previous one. WithHealthCheck is the same
// thing at build time.
//
// Make the check prove the dependency does what this app needs, not that
// something answers. The database check the database provider registers is
// a Ping, and a pool hands out a connection to a database whose migrations
// never ran, so a Ping passes while no query the app makes can succeed: add
// a check that reads a table the app owns. For a disk, use
// [storage.Check], because an existence check passes with credentials that
// cannot write.
func (a *App) AddHealthCheck(name string, check HealthCheck) {
	a.AddHealthCheckTimeout(name, DefaultHealthCheckTimeout, check)
}

// AddHealthCheckTimeout is AddHealthCheck with its own timeout (<= 0 means
// DefaultHealthCheckTimeout).
func (a *App) AddHealthCheckTimeout(name string, timeout time.Duration, check HealthCheck) {
	if timeout <= 0 {
		timeout = DefaultHealthCheckTimeout
	}

	a.health.mu.Lock()
	defer a.health.mu.Unlock()

	if a.health.checks == nil {
		a.health.checks = map[string]*healthCheck{}
	}

	a.health.checks[name] = &healthCheck{fn: check, timeout: timeout}
}

// CheckHealth runs every registered check in parallel, each bounded by its
// timeout, and returns each check's error by name (nil when healthy). The
// errors may hold hosts or credentials: log them, never send them to a
// client (ReadyHandler only reports "ok" or "fail").
func (a *App) CheckHealth(ctx context.Context) map[string]error {
	a.health.mu.RLock()
	checks := maps.Clone(a.health.checks)
	a.health.mu.RUnlock()

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results = make(map[string]error, len(checks))
	)

	for name, check := range checks {
		wg.Go(func() {
			err := runHealthCheck(ctx, check)

			mu.Lock()
			defer mu.Unlock()

			results[name] = err
		})
	}

	wg.Wait()

	return results
}

// runHealthCheck returns when the check does or its timeout expires, so a
// check that ignores ctx cannot hang the probe (its goroutine is abandoned).
// While an abandoned run is still going, the check fails at once instead of
// starting another goroutine.
func runHealthCheck(ctx context.Context, check *healthCheck) error {
	if !check.running.CompareAndSwap(false, true) {
		return errHealthCheckBusy
	}

	ctx, cancel := context.WithTimeout(ctx, check.timeout)
	defer cancel()

	done := make(chan error, 1)

	go func() {
		var err error

		// Clear running before publishing the result: a probe that starts
		// right after this one returns must not see the finished run as busy.
		defer func() {
			if p := recover(); p != nil {
				err = fmt.Errorf("vukem: health check panicked: %v", p)
			}

			check.running.Store(false)
			done <- err
		}()

		err = check.fn(ctx)
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("vukem: health check: %w", ctx.Err())
	}
}

// ReadyHandler is a readiness probe: it runs every health check (see
// CheckHealth) and answers JSON {"status":"ok"|"fail","checks":{name:
// "ok"|"fail"}} with 200, or 503 when a check fails. Failures are logged;
// their details never reach the client. Mount it where the platform
// expects it, e.g. r.Handle("GET /ready", app.ReadyHandler()); GET /up
// stays the liveness probe.
func (a *App) ReadyHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.writeReadiness(w, r)
	})
}

type readiness struct {
	Status  string            `json:"status"`
	Version string            `json:"version,omitempty"`
	Checks  map[string]string `json:"checks"`
}

func (a *App) writeReadiness(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	results := a.CheckHealth(ctx)

	checks := make(map[string]string, len(results))
	status, code := "ok", http.StatusOK

	for _, name := range slices.Sorted(maps.Keys(results)) {
		checks[name] = "ok"

		if err := results[name]; err != nil {
			checks[name] = "fail"
			status, code = "fail", http.StatusServiceUnavailable

			a.Log.WarnContext(ctx, "health check failed", "check", name, "err", err)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(readiness{Status: status, Version: a.Version, Checks: checks}); err != nil {
		a.Log.DebugContext(ctx, "write readiness response", "err", err) // client gone
	}
}

// healthHandler is the liveness probe: 200 once the app booted. With
// ?checks=1 it answers like ReadyHandler.
func (a *App) healthHandler(c *web.Ctx) error {
	if !c.QueryBool("checks") {
		// A cached liveness answer reports a process that may already be
		// gone, so no intermediary may keep it (writeReadiness does the
		// same for the readiness branch).
		c.Writer().Header().Set("Cache-Control", "no-store")

		if a.Version != "" {
			return c.JSON(http.StatusOK, web.Map{"status": "ok", "version": a.Version})
		}

		return c.JSON(http.StatusOK, web.Map{"status": "ok"})
	}

	a.writeReadiness(c.Writer(), c.Request())

	return nil
}

// SessionConfig is loaded from SESSION_* variables.
type SessionConfig struct {
	// Driver is "cookie" (encrypted cookie, default) or "memory"; the
	// database driver is installed by the database provider.
	Driver      string        `env:"SESSION_DRIVER"       default:"cookie"`
	Lifetime    time.Duration `env:"SESSION_LIFETIME"     default:"24h"`
	IdleTimeout time.Duration `env:"SESSION_IDLE_TIMEOUT" default:"2h"`
	CookieName  string        `env:"SESSION_COOKIE"       default:"vukem_session"`
	// Secure defaults to true in production or when APP_URL is https.
	Secure string `env:"SESSION_SECURE" default:"auto"`
	// RecordClient keeps the user agent and client network with each
	// logged-in server-side session (session.Config.RecordClient).
	RecordClient bool `env:"SESSION_RECORD_CLIENT" default:"false"`
}

// APIMiddleware installs the stack of JSON-only apps: recover, request
// id, security headers (the same set as DefaultMiddleware), maintenance
// mode and logging on the server; the body limit on routes. There are no
// sessions or CSRF: API clients authenticate with tokens (see
// auth.TokenGuard), and CORS is configured by the routes.
// APIMiddlewareWith customizes the stack.
func APIMiddleware(app *App, r *router.Router) error {
	return APIMiddlewareWith(app, r, APIMiddlewareOptions{})
}

// APIMiddlewareOptions customizes APIMiddlewareWith. The zero value
// installs the same stack as APIMiddleware.
type APIMiddlewareOptions struct {
	// SecureHeaders adjusts the SecureHeaders config after Vukem's defaults
	// are set, e.g. to add CSPSources or a ReportURI. The result is
	// validated at boot.
	SecureHeaders func(cfg *middleware.SecureHeadersConfig)
	// I18n configures the locale resolver installed when WithTranslations
	// was given. Nil resolves from the query and Accept-Language only (API
	// clients send no cookies or sessions). Validated at boot; needs
	// WithTranslations.
	I18n *i18n.MiddlewareConfig
}

// APIMiddlewareWith installs the APIMiddleware stack with opts, plus the
// i18n locale resolver when WithTranslations was given, so 422 messages
// follow the request locale.
func APIMiddlewareWith(app *App, r *router.Router, opts APIMiddlewareOptions) error {
	localeMW, err := localeMiddleware(app, opts.I18n,
		i18n.MiddlewareConfig{Order: []i18n.Source{i18n.SourceQuery, i18n.SourceHeader}})
	if err != nil {
		return err
	}

	secure := secureHeadersConfig(app)
	if opts.SecureHeaders != nil {
		opts.SecureHeaders(&secure)
	}

	if err := secure.Validate(); err != nil {
		return fmt.Errorf("vukem: secure headers: %w", err)
	}

	r.UseServer(serverMiddleware(app, secure, maintenanceConfig(app))...)
	useLocalePrefixes(app, r, opts.I18n)
	r.Use(middleware.BodyLimit(web.DefaultMaxBody))

	if localeMW != nil {
		r.Use(localeMW)
	}

	return nil
}

// useLocalePrefixes strips locale path prefixes on the server stack, after
// logging (which keeps the URL as requested) and before routing.
func useLocalePrefixes(app *App, r *router.Router, cfg *i18n.MiddlewareConfig) {
	if app.I18n != nil && cfg != nil && len(cfg.PathPrefixes) > 0 {
		r.UseServer(i18n.StripPathPrefix(cfg.PathPrefixes))
	}
}

// localeMiddleware builds the i18n resolver from cfg (def when nil); it is
// nil without WithTranslations, where a cfg is an error.
func localeMiddleware(app *App, cfg *i18n.MiddlewareConfig, def i18n.MiddlewareConfig) (router.Middleware, error) {
	if app.I18n == nil {
		if cfg != nil {
			return nil, errors.New("vukem: i18n middleware options have no effect without WithTranslations")
		}

		return nil, nil
	}

	if cfg != nil {
		def = *cfg
	}

	mw, err := i18n.Middleware(app.I18n, def)
	if err != nil {
		return nil, fmt.Errorf("vukem: i18n: %w", err)
	}

	return mw, nil
}

// Sessions returns the session manager, building it from SESSION_* on
// first use. Use SetSessionStore before the first call to change the store.
func (a *App) Sessions() (*session.Manager, error) {
	a.sessOnce.Do(func() {
		cfg, err := configLoad[SessionConfig](a)
		if err != nil {
			a.sessErr = err
			return
		}

		if a.sessStore == nil && a.sessStoreFunc != nil {
			a.sessStore, err = lazyStore(a, a.sessStoreFunc, "WithSessionStoreFunc", "SESSION_DRIVER="+cfg.Driver)
			if err != nil {
				a.sessErr = fmt.Errorf("vukem: session store: %w", err)
				return
			}
		}

		secure := a.Config.IsProduction() || strings.HasPrefix(a.Config.URL, "https://")
		if cfg.Secure != "auto" {
			secure = cfg.Secure == "true" || cfg.Secure == "1"
		}

		sc := session.Config{
			CookieName:   cfg.CookieName,
			Secure:       secure,
			IdleTimeout:  cfg.IdleTimeout,
			Lifetime:     cfg.Lifetime,
			RecordClient: cfg.RecordClient,
			Production:   a.Config.IsProduction(),
			Log:          a.Log,
			Store:        a.sessStore,
			Now:          a.now,
		}

		switch {
		case a.sessStore != nil:
		case cfg.Driver == "memory":
			sc.Store = session.NewMemoryStore()
		case cfg.Driver == "cookie":
			if sc.CookieKey, a.sessErr = a.DeriveKey("SESSION_DRIVER=cookie", 32); a.sessErr != nil {
				return
			}

			sc.PreviousCookieKeys = a.DerivePreviousKeys("SESSION_DRIVER=cookie", 32)
		case cfg.Driver == "database":
			sc.Store, a.sessErr = a.databaseSessionStore()
			if a.sessErr != nil {
				return
			}
		default:
			a.sessErr = fmt.Errorf("vukem: unknown SESSION_DRIVER %q", cfg.Driver)
			return
		}

		a.sessPruner, _ = sc.Store.(sessionPruner)
		a.sessions, a.sessErr = session.New(sc)
	})

	return a.sessions, a.sessErr
}

// SetSessionStore makes sessions server-side with store (call it from a
// provider's Register, before Sessions is first used).
func (a *App) SetSessionStore(store session.Store) { a.sessStore = store }

// maintenanceConfig signs bypass cookies with a key derived from APP_KEY
// and still accepts those signed with APP_PREVIOUS_KEYS; without APP_KEY
// the bypass is disabled (the app stays down for everyone).
func maintenanceConfig(app *App) middleware.MaintenanceConfig {
	key, err := app.DeriveKey("maintenance", 32)

	var previous [][]byte
	if err == nil {
		previous = app.DerivePreviousKeys("maintenance", 32)
	}

	return middleware.MaintenanceConfig{
		Key:          key,
		PreviousKeys: previous,
		Secure:       app.Config.IsProduction() || strings.HasPrefix(app.Config.URL, "https://"),
	}
}

// serverMiddleware is the server stack DefaultMiddleware and APIMiddleware
// share. SecureHeaders runs before Maintenance, so the maintenance 503 (and
// its bypass redirect) carries the security headers like every other
// response; Maintenance keeps its place ahead of Logger.
func serverMiddleware(
	app *App, secure middleware.SecureHeadersConfig, maint middleware.MaintenanceConfig,
) []router.Middleware {
	return []router.Middleware{
		middleware.Recover(),
		middleware.RequestID(),
		middleware.SecureHeaders(secure),
		middleware.Maintenance(maint),
		middleware.LoggerWith(app.Log, middleware.LoggerOptions{TruncateIP: app.Config.LogTruncateIP}),
	}
}

// secureHeadersConfig honours X-Forwarded-Proto from TRUSTED_PROXIES when
// TRUST_FORWARDED_PROTO is set, forces HSTS in production with HSTS_FORCE,
// and allows the Vite dev server in the CSP while it runs (app.Vite only
// reports it outside production).
func secureHeadersConfig(app *App) middleware.SecureHeadersConfig {
	cfg := middleware.SecureHeadersConfig{
		TrustForwardedProto: app.Config.TrustForwardedProto,
		TrustedProxies:      app.proxies,
		// Outside production APP_URL is often localhost: a pinned HSTS
		// there breaks every other local http app.
		HSTSForce: app.Config.HSTSForce && app.Config.IsProduction(),
	}

	if app.Vite != nil {
		cfg.DevServer = app.Vite.HotURL
	}

	cfg.CSPSources = app.Config.Analytics.CSPSources()

	return cfg
}

// DefaultMiddleware installs Vukem's recommended stacks on r:
//   - server: Recover, RequestID, SecureHeaders, Maintenance, Logger,
//     MethodOverride;
//   - router: BodyLimit(10 MiB), sessions, CSRF (CrossOriginProtection,
//     trusted origins from CSRF_TRUSTED_ORIGINS), and the i18n locale
//     resolver when WithTranslations was given.
//
// It returns configuration errors (insecure production cookies, bad
// origins) so they fail at boot. Starters call it from bootstrap.
// DefaultMiddlewareWith customizes the stack.
func DefaultMiddleware(app *App, r *router.Router) error {
	return DefaultMiddlewareWith(app, r, MiddlewareOptions{})
}

// MiddlewareOptions customizes DefaultMiddlewareWith. The zero value
// installs the same stack as DefaultMiddleware.
type MiddlewareOptions struct {
	// SecureHeaders adjusts the SecureHeaders config after Vukem's defaults
	// (the Vite dev server) are set, e.g. to add CSPSources or a ReportURI.
	// The result is validated at boot.
	SecureHeaders func(cfg *middleware.SecureHeadersConfig)
	// Stateless installs no session middleware (cookieless apps). CSRF
	// protection stays on as Origin/Sec-Fetch-Site checks only: there is no
	// session token, so csrfToken and csrfField render empty, and session
	// features (Flash, session auth) fail with an error.
	Stateless bool
	// SessionBypass lists ServeMux patterns served without sessions. As in
	// ServeMux, a pattern ending in "/" ("/webhooks/") covers its whole
	// subtree; "POST /hooks/{id}" matches one method and path. It cannot be
	// combined with Stateless.
	SessionBypass []string
	// CSRFBypass lists ServeMux patterns (same matching as SessionBypass)
	// exempt from CSRF checks, e.g. signed webhooks. Each bypass is logged
	// at boot.
	CSRFBypass []string
	// CSRFTokenFallback also requires the session's CSRF token (the
	// csrfField template function, or the X-CSRF-Token header) on unsafe
	// requests that carry the session cookie but neither Sec-Fetch-Site nor
	// Origin, i.e. browsers too old for CrossOriginProtection (see
	// csrf.Config.TokenFallback). It needs sessions: combined with
	// Stateless it is a boot error.
	CSRFTokenFallback bool
	// I18n configures the locale resolver installed when WithTranslations
	// was given (nil: i18n.MiddlewareConfig defaults), e.g. its sources and
	// their order, or the selectable locales. It is validated at boot and
	// needs WithTranslations.
	I18n *i18n.MiddlewareConfig
}

// DefaultMiddlewareWith installs the DefaultMiddleware stacks with opts.
func DefaultMiddlewareWith(app *App, r *router.Router, opts MiddlewareOptions) error {
	if opts.Stateless && len(opts.SessionBypass) > 0 {
		return errors.New("vukem: SessionBypass has no effect with Stateless (no sessions anywhere)")
	}

	if opts.Stateless && opts.CSRFTokenFallback {
		return errors.New("vukem: CSRFTokenFallback needs sessions; it cannot be combined with Stateless")
	}

	localeMW, err := localeMiddleware(app, opts.I18n, i18n.MiddlewareConfig{})
	if err != nil {
		return err
	}

	secure := secureHeadersConfig(app)
	if opts.SecureHeaders != nil {
		opts.SecureHeaders(&secure)
	}

	if err := secure.Validate(); err != nil {
		return fmt.Errorf("vukem: secure headers: %w", err)
	}

	var origins []string
	for o := range strings.SplitSeq(app.Source.Get("CSRF_TRUSTED_ORIGINS"), ",") {
		if o = strings.TrimSpace(o); o != "" {
			origins = append(origins, o)
		}
	}

	csrfCfg := csrf.Config{TrustedOrigins: origins, BypassPatterns: opts.CSRFBypass}
	routerMW := []router.Middleware{middleware.BodyLimit(web.DefaultMaxBody)}

	if !opts.Stateless {
		sessions, err := app.Sessions()
		if err != nil {
			return err
		}

		sessMW, err := bypass(opts.SessionBypass, sessions.Middleware())
		if err != nil {
			return fmt.Errorf("vukem: session bypass: %w", err)
		}

		routerMW = append(routerMW, sessMW)
		csrfCfg.TokenFallback = opts.CSRFTokenFallback
		csrfCfg.SessionCookie = sessions.CookieName()
	}

	csrfMW, err := csrf.Middleware(csrfCfg)
	if err != nil {
		return err
	}

	for _, p := range opts.CSRFBypass {
		app.Log.Warn("csrf bypass", "pattern", p)
	}

	r.UseServer(append(serverMiddleware(app, secure, maintenanceConfig(app)), middleware.MethodOverride())...)
	useLocalePrefixes(app, r, opts.I18n)

	r.Use(append(routerMW, csrfMW)...)

	if localeMW != nil {
		r.Use(localeMW)
	}

	return nil
}

// bypassMark is the handler registered for bypass patterns; comparing it
// tells a real match from ServeMux's redirect and 405 handlers.
type bypassMark struct{}

func (*bypassMark) ServeHTTP(http.ResponseWriter, *http.Request) {}

// bypass skips mw for requests matching one of the ServeMux patterns.
func bypass(patterns []string, mw router.Middleware) (router.Middleware, error) {
	if len(patterns) == 0 {
		return mw, nil
	}

	mux, mark := http.NewServeMux(), &bypassMark{}
	for _, p := range patterns {
		if err := handlePattern(mux, p, mark); err != nil {
			return nil, err
		}
	}

	return func(next http.Handler) http.Handler {
		wrapped := mw(next)

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if h, _ := mux.Handler(r); h == http.Handler(mark) {
				next.ServeHTTP(w, r)
				return
			}

			wrapped.ServeHTTP(w, r)
		})
	}, nil
}

// handlePattern turns ServeMux's panic on a bad or duplicate pattern into
// an error.
func handlePattern(mux *http.ServeMux, pattern string, h http.Handler) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("pattern %q: %v", pattern, p)
		}
	}()

	mux.Handle(pattern, h)

	return nil
}

func (a *App) newRouter() *router.Router {
	debug := a.Config.Debug && !a.Config.IsProduction()
	services := &web.Services{
		Log:            a.Log,
		Debug:          debug,
		ProblemBaseURL: a.Source.Get("APP_PROBLEM_BASE_URL"),
		CheckResponses: !a.Config.IsProduction(),
		TrustedProxies: a.proxies,
		ClientIPHeader: a.Config.ClientIPHeader,
		IPv6Prefix:     a.Config.RateLimitIPv6Prefix,

		TrustForwardedProto: a.Config.TrustForwardedProto,
	}

	if debug && a.Kind == KindWeb {
		services.DebugOut = os.Stderr
	}

	r := router.New(services)
	if a.Config.IsProduction() && len(a.proxies) == 0 {
		r.UseServer(untrustedProxyWarning(a.Log))
	}
	r.Get("/up", a.healthHandler).Name(reservedRouteName("health"))

	return r
}

// reservedRoutePrefix namespaces the route names the framework registers
// itself. Without it they compete with the app's: GET /up was named
// "health", so an app registering its own GET /health under that obvious
// name made the router refuse to boot on a duplicate name, and the error
// named the app's route — the one that looked wrong. Nothing in the docs
// or the type system said the name was taken, and the two paths differ, so
// there was no hint at the call site either (A100).
const reservedRoutePrefix = "vukem."

// reservedRouteName is the framework's name for one of its own routes. An
// app's name never starts with the prefix unless it chooses to, and
// docs/http.md says so.
func reservedRouteName(name string) string { return reservedRoutePrefix + name }

// untrustedProxyWarning logs once when a request from a private, CGNAT or
// loopback peer carries X-Forwarded-For or X-Real-IP while TRUSTED_PROXIES
// is empty: the app most likely runs behind a proxy, so c.IP() is the
// proxy's address and every client shares one per-IP rate-limit bucket.
// The headers are only detected, never trusted.
func untrustedProxyWarning(log *slog.Logger) func(http.Handler) http.Handler {
	var warned atomic.Bool

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !warned.Load() {
				header := ""

				switch {
				case r.Header.Get("X-Forwarded-For") != "":
					header = "X-Forwarded-For"
				case r.Header.Get("X-Real-IP") != "":
					header = "X-Real-IP"
				}

				if header != "" && internalPeer(r.RemoteAddr) && warned.CompareAndSwap(false, true) {
					log.WarnContext(
						r.Context(),
						"requests from a proxy carry "+header+" but TRUSTED_PROXIES is empty: "+
							"c.IP() is the proxy address and every client shares one rate-limit bucket; "+
							"set TRUSTED_PROXIES (and CLIENT_IP_HEADER) to the proxy's network",
						"peer",
						r.RemoteAddr,
						"header",
						header,
					)
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

// internalPeer reports whether addr (host:port) is a private, CGNAT or
// loopback address, where reverse proxies usually connect from.
func internalPeer(addr string) bool {
	ap, err := netip.ParseAddrPort(addr)
	if err != nil {
		return false
	}

	ip := ap.Addr().Unmap()
	// 100.64.0.0/10 (RFC 6598 shared space, Railway's edge) is not IsPrivate.
	cgnat := ip.Is4() && ip.As4()[0] == 100 && ip.As4()[1]&0xc0 == 64

	return ip.IsPrivate() || ip.IsLoopback() || cgnat
}

// Handler returns the compiled HTTP handler. It fails with ErrNotBooted
// while routes from WithRoutesAfterBoot wait for Boot.
func (a *App) Handler() (http.Handler, error) {
	if a.pendingBootRoutes() {
		return nil, ErrNotBooted
	}

	return a.Router.Handler()
}

// httpServer is the server httpRunner runs for h, with the app's limits.
func (a *App) httpServer(h http.Handler) *HTTPServer {
	srv := NewHTTPServer(a.Addr(), h)
	srv.ShutdownTimeout = a.Config.ShutdownTimeout
	srv.Server.MaxHeaderBytes = a.Config.MaxHeaderBytes
	srv.Ready = a.ready

	return srv
}

func (a *App) httpRunner() (Runner, error) {
	h, err := a.Handler()
	if err != nil {
		return nil, err
	}

	srv := a.httpServer(h)

	tlsCfg, err := a.serverTLS()
	if err != nil {
		return nil, err
	}

	var p http.Protocols

	p.SetHTTP1(true)

	switch {
	case tlsCfg != nil:
		p.SetHTTP2(true)
		srv.Server.TLSConfig = tlsCfg
	case !a.Config.IsProduction() || a.Config.H2C:
		p.SetUnencryptedHTTP2(true)
	}

	srv.Server.Protocols = &p

	redirect, err := a.tlsRedirectServer(tlsCfg != nil)
	if err != nil {
		return nil, err
	}

	return RunnerFunc(func(ctx context.Context) error {
		a.Log.InfoContext(
			ctx,
			"http server starting",
			"addr",
			a.Addr(),
			"env",
			string(a.Config.Env),
			"tls",
			tlsCfg != nil,
		)

		if redirect == nil {
			return srv.Run(ctx)
		}

		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		errc := make(chan error, 1)

		go func() { errc <- redirect.Run(ctx); cancel() }()

		err := srv.Run(ctx)
		cancel()

		return errors.Join(err, <-errc)
	}), nil
}

// serverTLS is the TLS config of the main server, or nil for plain HTTP:
// TLS_CERT_FILE/TLS_KEY_FILE (reloaded on change) or WithTLSConfig, never
// below TLS 1.2.
func (a *App) serverTLS() (*tls.Config, error) {
	certFile, keyFile := a.Config.TLSCertFile, a.Config.TLSKeyFile
	if a.tlsConfig == nil && certFile == "" && keyFile == "" {
		return nil, nil
	}

	if (certFile == "") != (keyFile == "") {
		return nil, errors.New("vukem: set both TLS_CERT_FILE and TLS_KEY_FILE")
	}

	if a.Config.H2C {
		return nil, errors.New("vukem: HTTP_H2C is plaintext HTTP/2 and cannot be combined with TLS")
	}

	cfg := &tls.Config{}
	if a.tlsConfig != nil {
		cfg = a.tlsConfig.Clone()
	}

	switch {
	case cfg.MinVersion == 0:
		cfg.MinVersion = tls.VersionTLS12
	case cfg.MinVersion < tls.VersionTLS12:
		return nil, errors.New("vukem: WithTLSConfig: MinVersion below TLS 1.2")
	}

	hasCert := len(cfg.Certificates) > 0 || cfg.GetCertificate != nil || cfg.GetConfigForClient != nil

	switch {
	case certFile != "" && hasCert:
		return nil, errors.New("vukem: TLS_CERT_FILE and a WithTLSConfig certificate are exclusive")
	case certFile != "":
		r := &certReloader{certFile: certFile, keyFile: keyFile, log: a.Log}
		if _, err := r.load(); err != nil {
			return nil, err
		}

		cfg.GetCertificate = r.get
	case !hasCert:
		return nil, errors.New(
			"vukem: WithTLSConfig: no certificate (Certificates, GetCertificate or GetConfigForClient)",
		)
	}

	return cfg, nil
}

// tlsRedirectServer is the plain HTTP server of TLS_REDIRECT_ADDR, or nil.
// It redirects to APP_URL, never to the request's Host header.
func (a *App) tlsRedirectServer(tlsOn bool) (*HTTPServer, error) {
	if a.Config.TLSRedirectAddr == "" {
		return nil, nil
	}

	base, err := neturl.Parse(a.Config.URL)
	if !tlsOn || err != nil || base.Scheme != "https" || base.Host == "" {
		return nil, errors.New(
			"vukem: TLS_REDIRECT_ADDR needs TLS_CERT_FILE/TLS_KEY_FILE (or WithTLSConfig) and an https APP_URL",
		)
	}

	origin := base.Scheme + "://" + base.Host
	srv := NewHTTPServer(a.Config.TLSRedirectAddr, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		//nolint:gosec // G710: the origin is APP_URL, only the path and query come from the request
		http.Redirect(w, r, origin+r.URL.RequestURI(), http.StatusPermanentRedirect)
	}))
	srv.ShutdownTimeout = a.Config.ShutdownTimeout

	return srv, nil
}

// certReloader serves a PEM key pair and reloads it when either file's
// modification time moves, checking at most once a second; a pair that
// fails to load keeps the previous certificate.
type certReloader struct {
	certFile, keyFile string
	log               *slog.Logger

	mu      sync.Mutex
	cert    *tls.Certificate
	mod     time.Time
	checked time.Time
}

func (r *certReloader) get(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if time.Since(r.checked) < time.Second {
		return r.cert, nil
	}

	r.checked = time.Now()

	if cert, err := r.load(); err != nil {
		r.log.Error("tls: certificate reload failed, keeping the previous one", "err", err)
	} else if cert != nil {
		r.cert = cert
	}

	return r.cert, nil
}

// load reads the pair when it changed since the last load (nil, nil when
// unchanged).
func (r *certReloader) load() (*tls.Certificate, error) {
	var mod time.Time

	for _, f := range []string{r.certFile, r.keyFile} {
		fi, err := os.Stat(f)
		if err != nil {
			return nil, fmt.Errorf("vukem: tls: %w", err)
		}

		if fi.ModTime().After(mod) {
			mod = fi.ModTime()
		}
	}

	if r.cert != nil && mod.Equal(r.mod) {
		return nil, nil
	}

	cert, err := tls.LoadX509KeyPair(r.certFile, r.keyFile)
	if err != nil {
		return nil, fmt.Errorf("vukem: tls: %w", err)
	}

	r.cert, r.mod = &cert, mod

	return &cert, nil
}

// BuildFunc builds the application; bootstrap.New implements it.
type BuildFunc func(opts ...Option) (*App, error)

// RunMain is the testable body of main: it builds the app with the given
// environment and runs the command in args (default "serve").
func RunMain(ctx context.Context, args []string, getenv func(string) (string, bool), build BuildFunc,
	opts ...Option,
) error {
	cmd, rest := "serve", []string(nil)
	if len(args) > 1 {
		cmd, rest = args[1], args[2:]
	}

	if cmd == keyGenerateName {
		return runKeyGenerate(rest, opts)
	}

	base := []Option{WithEnv(osDirFS("."), getenv)}
	switch {
	case isWarmupCommand(cmd):
		base = append(base, WithKind(KindWarmup))
	case cmd != "serve":
		base = append(base, WithKind(KindConsole))
	}

	app, err := build(append(base, opts...)...)
	if err != nil {
		return err
	}

	switch cmd {
	case "serve":
		if err := serveFlags(app, rest); err != nil {
			return err
		}

		return app.Run(ctx)
	default:
		return runCommand(ctx, app, cmd, rest)
	}
}

// Main runs the application from os.Args and the process environment and
// returns the exit code. Use it as: func main() { os.Exit(vukem.Main(bootstrap.New)) }.
func Main(build BuildFunc) int {
	ctx, stop := signalContext()
	defer stop()

	if err := RunMain(ctx, osArgs(), lookupEnv, build); err != nil {
		if errors.Is(err, errUsage) {
			return 2
		}

		fmt.Fprintln(stderr(), err)

		return 1
	}

	return 0
}

// sessionPruner is a session store that keeps expired rows until pruned.
type sessionPruner interface {
	Prune(ctx context.Context) (int64, error)
}

// databaseSessionStore stores sessions in the app database (SQLite or a
// driver such as Postgres or MySQL), creating the sessions table when
// missing. Expired rows are deleted by the hourly session:prune task.
func (a *App) databaseSessionStore() (session.Store, error) {
	db, err := a.DB()
	if err != nil {
		return nil, err
	}

	var opts []session.SQLOption

	switch db.Dialect().Name() {
	case "postgres":
		opts = append(opts, session.WithDollarPlaceholders())
	case "mysql":
		opts = append(opts, session.WithMySQL())
	}

	store, err := session.NewSQLStore(db.SQL(), opts...)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := store.CreateTable(ctx); err != nil {
		return nil, fmt.Errorf("vukem: create sessions table: %w", err)
	}

	return store, nil
}

func configLoad[T any](a *App) (T, error) { return config.Load[T](a.Source) }

func ssrCommands() []Command { return []Command{ssrStartCommand(), ssrCheckCommand()} }

// ssrStartCommand supervises the Inertia SSR server: it runs the bundle with
// node and restarts it when it crashes, until the command is stopped.
func ssrStartCommand() Command {
	var (
		bundle, runtime *string
		delay           *time.Duration
	)

	return CommandFunc{
		CommandName: "ssr:start",
		Desc:        "Run the Inertia SSR server (node bootstrap/ssr/ssr.js) and restart it if it crashes",
		DefineFlags: func(fs *flag.FlagSet) {
			bundle = fs.String("bundle", "bootstrap/ssr/ssr.js", "SSR bundle built by vite build --ssr")
			runtime = fs.String("runtime", "node", "JavaScript runtime that runs the bundle")
			delay = fs.Duration("restart-delay", time.Second, "wait before restarting a crashed server")
		},
		Fn: func(ctx context.Context, app *App, _ []string) error {
			if err := checkSSRBundle("ssr:start", *bundle); err != nil {
				return err
			}

			return app.superviseSSR(ctx, ssrProcess{*bundle, *runtime, *delay, false, ssrMinUptime})
		},
	}
}

// ssrProcess is how ssr:start and `serve -with-ssr` run the SSR server.
type ssrProcess struct {
	bundle, runtime string
	delay           time.Duration
	// always restarts after a clean exit too: under serve the app keeps
	// rendering pages, so the server must come back.
	always bool
	// minUptime is ssrMinUptime outside tests.
	minUptime time.Duration
}

// ssrMinUptime separates a bundle that never served (exits at once) from a
// server stopped with POST /shutdown.
const ssrMinUptime = 2 * time.Second

// errSSRNoServer reports an SSR bundle that exited without serving.
var errSSRNoServer = errors.New("ssr:start: the SSR bundle did not start a server " +
	"(call createInertiaApp at the top level of resources/js/ssr.ts(x), do not export it, and rebuild)")

func checkSSRBundle(cmd, bundle string) error {
	if _, err := os.Stat(bundle); err != nil {
		return fmt.Errorf("%s: SSR bundle %s not found (run npm run build): %w", cmd, bundle, err)
	}

	return nil
}

// superviseSSR runs the SSR bundle and restarts it when it exits, until ctx
// is done.
func (a *App) superviseSSR(ctx context.Context, p ssrProcess) error {
	for {
		cmd := exec.CommandContext(ctx, p.runtime, p.bundle) //nolint:gosec // G204: developer-supplied flags
		cmd.Stdout, cmd.Stderr = a.stdout, os.Stderr
		cmd.WaitDelay = 5 * time.Second

		start := time.Now()
		err := cmd.Run()

		// A bundle that exits cleanly at once never started a server (an
		// entry that exports createInertiaApp instead of calling it): fail
		// loudly rather than exit 0 with no output.
		if ctx.Err() == nil && err == nil && !p.always && time.Since(start) < p.minUptime {
			return fmt.Errorf("%w: %s exited at once", errSSRNoServer, p.bundle)
		}

		// Stopped (Ctrl+C), or a clean exit (POST /shutdown) under ssr:start.
		if ctx.Err() != nil || err == nil && !p.always {
			return nil //nolint:nilerr // the process was killed on purpose
		}

		a.Log.WarnContext(ctx, "ssr: server exited, restarting", "err", err, "delay", p.delay)

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(p.delay):
		}
	}
}

// serveFlags parses the flags of the serve command: -with-ssr adds the
// "ssr" runner, which supervises the SSR server like ssr:start.
func serveFlags(app *App, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(app.stderr)

	withSSR := fs.Bool("with-ssr", false, "also run and supervise the Inertia SSR server (like ssr:start)")
	bundle := fs.String("ssr-bundle", "bootstrap/ssr/ssr.js", "SSR bundle built by vite build --ssr")
	runtime := fs.String("ssr-runtime", "node", "JavaScript runtime that runs the SSR bundle")
	delay := fs.Duration("ssr-restart-delay", time.Second, "wait before restarting the SSR server")

	if err := fs.Parse(args); err != nil {
		return errUsage
	}

	if !*withSSR {
		return nil
	}

	if err := checkSSRBundle("serve -with-ssr", *bundle); err != nil {
		return err
	}

	p := ssrProcess{*bundle, *runtime, *delay, true, ssrMinUptime}
	app.AddRunner("ssr", RunnerFunc(func(ctx context.Context) error { return app.superviseSSR(ctx, p) }))

	return nil
}

func ssrCheckCommand() Command {
	var url *string

	return CommandFunc{
		CommandName: "ssr:check",
		Desc:        "Check that the Inertia SSR server answers /health (exit 1 otherwise)",
		DefineFlags: func(fs *flag.FlagSet) {
			url = fs.String("url", inertia.DefaultSSRURL, "SSR server URL")
		},
		Fn: func(ctx context.Context, app *App, _ []string) error {
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			req, err := http.NewRequestWithContext(
				ctx,
				http.MethodGet,
				strings.TrimSuffix(*url, "/")+"/health",
				http.NoBody,
			)
			if err != nil {
				return err
			}

			res, err := http.DefaultClient.Do(req)
			if err != nil {
				return fmt.Errorf("ssr:check: %w", err)
			}
			defer res.Body.Close()

			if res.StatusCode != http.StatusOK {
				return errors.New("ssr:check: " + res.Status)
			}

			_, err = fmt.Fprintln(app.stdout, "SSR server is healthy")

			return err
		},
	}
}

// WithOpenAPI makes App.OpenAPI (and so /openapi.json, openapi:export,
// codegen:types, codegen:client and vukemtest.AssertMatchesOpenAPI) build
// the document from info, which is where an app declares its description
// and its security schemes: Build rejects any scheme an operation names
// (api.Op.Security) that the document does not declare, and only "bearer"
// is predefined.
//
//	vukem.WithOpenAPI(openapi.Info{
//		Description: "Shop API",
//		SecuritySchemes: map[string]openapi.Schema{
//			"apiKey": {"type": "apiKey", "in": "header", "name": "X-API-Key"},
//		},
//	})
//
// An empty Title, Version or Servers keeps the configured value (APP_NAME,
// API_VERSION, OPENAPI_SERVERS or APP_URL); an empty StripPrefix keeps
// OPENAPI_STRIP_PREFIX; an empty PathPrefix keeps OPENAPI_PREFIX, and the
// prefix of ServeOpenAPIWith wins over both. Info.Docs gives one document
// per path prefix its own Servers and StripPrefix.
func WithOpenAPI(info openapi.Info) Option {
	return func(o *options) error { o.openAPI = &info; return nil }
}

// OpenAPI returns the OpenAPI 3.1 document of the app's routes (title
// APP_NAME, version API_VERSION, filtered by OPENAPI_PREFIX, with the
// description and security schemes of WithOpenAPI).
func (a *App) OpenAPI() (*openapi.Document, error) {
	return a.openAPI(a.Config.OpenAPIPrefix)
}

func (a *App) openAPI(prefix string) (*openapi.Document, error) {
	if _, err := a.Router.Handler(); err != nil {
		return nil, err
	}

	info := openapi.Info{}
	if a.openAPIInfo != nil {
		info = *a.openAPIInfo
	}

	if info.Title == "" {
		info.Title = a.Config.Name
	}

	if info.Version == "" {
		info.Version = a.Config.APIVersion
	}

	doc, perDoc := info.Docs[strings.TrimSuffix(prefix, "/")]
	if perDoc {
		info.Servers = doc.Servers
		info.StripPrefix = doc.StripPrefix
	}

	if len(info.Servers) == 0 {
		info.Servers = a.Config.OpenAPIServers
	}

	if len(info.Servers) == 0 {
		info.Servers = []string{a.Config.URL}
	}

	if info.StripPrefix == "" && !perDoc {
		info.StripPrefix = a.Config.OpenAPIStripPrefix
	}

	if prefix != "" {
		info.PathPrefix = prefix
	}

	info.RequiredOutputs = info.RequiredOutputs || a.Config.OpenAPIRequiredOutputs
	info.TypedOnly = info.TypedOnly || a.Config.OpenAPITypedOnly

	return openapi.Build(a.Router.Routes(), info)
}

// OpenAPIOptions tunes [ServeOpenAPIWith].
type OpenAPIOptions struct {
	// PathPrefix keeps only routes under it, e.g. "/api/v1" (default
	// Config.OpenAPIPrefix, OPENAPI_PREFIX).
	PathPrefix string
	// InProduction serves this document in production too. Set it for a
	// public API; OPENAPI_IN_PRODUCTION=true does the same for every
	// document and API reference page.
	InProduction bool
}

// ServeOpenAPI registers GET path (e.g. "/openapi.json") serving the
// document. In production it registers nothing (and returns nil) unless
// OPENAPI_IN_PRODUCTION=true: the document maps the whole API surface.
func ServeOpenAPI(app *App, r *router.Router, path string) *router.Route {
	route := ServeOpenAPIWith(app, r, path, OpenAPIOptions{})
	if route != nil {
		route.Name("openapi")
	}

	return route
}

// ServeOpenAPIWith is [ServeOpenAPI] with options: a document per path
// prefix (one per API version) and a per-document production opt-in. The
// route is unnamed, so several documents can coexist; name it if needed.
//
//	vukem.ServeOpenAPIWith(app, r, "/api/v1/openapi.json",
//		vukem.OpenAPIOptions{PathPrefix: "/api/v1", InProduction: true})
func ServeOpenAPIWith(app *App, r *router.Router, path string, opts OpenAPIOptions) *router.Route {
	if app.Config.IsProduction() && !app.Config.OpenAPIInProduction && !opts.InProduction {
		return nil
	}

	prefix := opts.PathPrefix
	if prefix == "" {
		prefix = app.Config.OpenAPIPrefix
	}

	return r.Get(path, func(c *web.Ctx) error {
		doc, err := app.openAPI(prefix)
		if err != nil {
			return err
		}

		return c.JSON(200, doc)
	}).Doc(openapi.Skip{})
}

// RateLimitConfig is read from RATE_LIMIT_* variables.
type RateLimitConfig struct {
	// Store is "memory" (default: GCRA per process) or "cache" (fixed
	// windows counted in App.Cache, which must be shared by every server:
	// CACHE_STORE=database or a Redis store set with WithCacheStore).
	Store string `env:"RATE_LIMIT_STORE" default:"memory"`
}

// WithRateLimiters makes App.RateLimiters return f instead of the factory
// RATE_LIMIT_STORE chooses: a shared limiter of your own in production, or
// a fake in tests (an always-allow limiter, or ratelimit.NewMemory with
// tight limits). f must not be nil.
func WithRateLimiters(f ratelimit.Factory) Option {
	return func(o *options) error {
		if f == nil {
			return errors.New("vukem: WithRateLimiters needs a non-nil ratelimit.Factory")
		}

		o.rateLimiters = f

		return nil
	}
}

// RateLimiters returns a factory for the limiters routes use, chosen by
// RATE_LIMIT_STORE, so switching to a shared store needs no code change:
//
//	limiters, err := app.RateLimiters()
//	loginIP, _ := auth.ThrottleKeys("email")
//	login := ratelimit.Middleware(limiters("login", 5, time.Minute), loginIP)
//
// An unknown store is an error, not a silent fallback to memory, and so
// is RATE_LIMIT_STORE=cache over a store [cache.Shared] does not report
// as shared (the per-process memory cache, or a custom store or wrapper
// that does not declare cache.Sharer). WithRateLimiters replaces the
// factory.
func (a *App) RateLimiters() (ratelimit.Factory, error) {
	if a.rateLimiters != nil {
		return a.rateLimiters, nil
	}

	cfg, err := configLoad[RateLimitConfig](a)
	if err != nil {
		return nil, err
	}

	switch cfg.Store {
	case "memory":
		return func(_ string, limit int, window time.Duration) ratelimit.Limiter {
			return ratelimit.NewMemory(limit, window)
		}, nil
	case "cache":
		shared, err := a.cacheShared()
		if err != nil {
			return nil, err
		}

		if !shared {
			return nil, errors.New("vukem: RATE_LIMIT_STORE=cache needs a cache shared by every server " +
				"(CACHE_STORE=database or vukem.WithCacheStore with Redis; a custom store or wrapper must implement " +
				"cache.Sharer); the memory cache keeps one budget per process")
		}

		c, err := a.Cache()
		if err != nil {
			return nil, err
		}

		return func(name string, limit int, window time.Duration) ratelimit.Limiter {
			return ratelimit.NewWindow(c, name, limit, window)
		}, nil
	default:
		return nil, fmt.Errorf("vukem: unknown RATE_LIMIT_STORE %q (memory or cache)", cfg.Store)
	}
}

// cacheShared reports whether App.Cache reaches every server
// ([cache.Shared]): CACHE_STORE=database, or a store set with
// WithCacheStore / WithCacheStoreFunc that declares cache.Sharer (Redis).
// It resolves the store through Cache, so a lazy store is read under its
// sync.Once.
func (a *App) cacheShared() (bool, error) {
	c, err := a.Cache()
	if err != nil {
		return false, err
	}

	return cache.Shared(c.Store()), nil
}

// enableDevtools wires the dashboard: its recorder is the outermost server
// middleware (so timings cover the whole stack), the route middleware
// captures the matched pattern, and the routes live under /_vukem. The
// database, queue and mailer pick it up when they are first built; the
// logger and event bus are wired in New.
func (a *App) enableDevtools(dt *devtools.Devtools) {
	a.Devtools = dt

	a.Router.UseServer(dt.Middleware())
	a.Router.Use(dt.RouteMiddleware())
	dt.Register(a.Router)
}
