package vukem

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/netip"
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

// AddHealthCheck registers a readiness check, reported by ReadyHandler and
// GET /up?checks=1 and bounded by DefaultHealthCheckTimeout. A check with
// the same name replaces the previous one.
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
		defer check.running.Store(false)
		defer func() {
			if p := recover(); p != nil {
				done <- fmt.Errorf("vukem: health check panicked: %v", p)
			}
		}()

		done <- check.fn(ctx)
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
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
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
	if err := json.NewEncoder(w).Encode(readiness{Status: status, Checks: checks}); err != nil {
		a.Log.DebugContext(ctx, "write readiness response", "err", err) // client gone
	}
}

// healthHandler is the liveness probe: 200 once the app booted. With
// ?checks=1 it answers like ReadyHandler.
func (a *App) healthHandler(c *web.Ctx) error {
	if !c.QueryBool("checks") {
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
// id, logging and security headers on the server; the body limit on
// routes. There are no sessions or CSRF: API clients authenticate with
// tokens (see auth.TokenGuard), and CORS is configured by the routes.
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

	r.UseServer(
		middleware.Recover(),
		middleware.RequestID(),
		middleware.Maintenance(maintenanceConfig(app)),
		middleware.Logger(app.Log),
		middleware.SecureHeaders(secure),
	)
	r.Use(middleware.BodyLimit(web.DefaultMaxBody))

	if localeMW != nil {
		r.Use(localeMW)
	}

	return nil
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

	return cfg
}

// DefaultMiddleware installs Vukem's recommended stacks on r:
//   - server: Recover, RequestID, Logger, SecureHeaders, MethodOverride;
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

	csrfMW, err := csrf.Middleware(csrf.Config{TrustedOrigins: origins, BypassPatterns: opts.CSRFBypass})
	if err != nil {
		return err
	}

	for _, p := range opts.CSRFBypass {
		app.Log.Warn("csrf bypass", "pattern", p)
	}

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
	}

	r.UseServer(
		middleware.Recover(),
		middleware.RequestID(),
		middleware.Maintenance(maintenanceConfig(app)),
		middleware.Logger(app.Log),
		middleware.SecureHeaders(secure),
		middleware.MethodOverride(),
	)

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
	}

	if debug && a.Kind == KindWeb {
		services.DebugOut = os.Stderr
	}

	r := router.New(services)
	if a.Config.IsProduction() && len(a.proxies) == 0 {
		r.UseServer(untrustedProxyWarning(a.Log))
	}
	r.Get("/up", a.healthHandler).Name("health")

	return r
}

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

	if !a.Config.IsProduction() || a.Config.H2C {
		var p http.Protocols
		p.SetHTTP1(true)
		p.SetUnencryptedHTTP2(true)
		srv.Server.Protocols = &p
	}

	return RunnerFunc(func(ctx context.Context) error {
		a.Log.InfoContext(ctx, "http server starting", "addr", a.Addr(), "env", string(a.Config.Env))
		return srv.Run(ctx)
	}), nil
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
			if _, err := os.Stat(*bundle); err != nil {
				return fmt.Errorf("ssr:start: bundle %s not found (run npm run build): %w", *bundle, err)
			}

			for {
				cmd := exec.CommandContext(ctx, *runtime, *bundle) //nolint:gosec // G204: developer-supplied flags
				cmd.Stdout, cmd.Stderr = app.stdout, os.Stderr
				cmd.WaitDelay = 5 * time.Second

				err := cmd.Run()

				// Stopped (Ctrl+C) or a clean exit (POST /shutdown): done.
				if err == nil || ctx.Err() != nil {
					return nil //nolint:nilerr // the process was killed on purpose
				}

				app.Log.WarnContext(ctx, "ssr: server exited, restarting", "err", err, "delay", *delay)

				select {
				case <-ctx.Done():
					return nil
				case <-time.After(*delay):
				}
			}
		},
	}
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

// OpenAPI returns the OpenAPI 3.1 document of the app's routes (title
// APP_NAME, version API_VERSION, filtered by OPENAPI_PREFIX).
func (a *App) OpenAPI() (*openapi.Document, error) {
	return a.openAPI(a.Config.OpenAPIPrefix)
}

func (a *App) openAPI(prefix string) (*openapi.Document, error) {
	if _, err := a.Router.Handler(); err != nil {
		return nil, err
	}

	return openapi.Generate(a.Router.Routes(), openapi.Info{
		Title:      a.Config.Name,
		Version:    a.Config.APIVersion,
		Servers:    []string{a.Config.URL},
		PathPrefix: prefix,
	}), nil
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

// RateLimiters returns a factory for the limiters routes use, chosen by
// RATE_LIMIT_STORE, so switching to a shared store needs no code change:
//
//	limiters, err := app.RateLimiters()
//	login := ratelimit.Middleware(limiters("login", 5, time.Minute), account.LoginThrottleKey)
//
// An unknown store is an error, not a silent fallback to memory, and so
// is RATE_LIMIT_STORE=cache over a store [cache.Shared] does not report
// as shared (the per-process memory cache, or a custom store or wrapper
// that does not declare cache.Sharer).
func (a *App) RateLimiters() (ratelimit.Factory, error) {
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
