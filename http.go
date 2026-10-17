package vukem

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/csrf"
	"github.com/AlexGalhardo/vukem/i18n"
	"github.com/AlexGalhardo/vukem/middleware"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/session"
	"github.com/AlexGalhardo/vukem/web"
)

// HealthCheck reports whether a dependency (database, cache...) is healthy.
type HealthCheck func(ctx context.Context) error

// DefaultHealthCheckTimeout bounds a health check registered with
// AddHealthCheck.
const DefaultHealthCheckTimeout = 3 * time.Second

type healthCheck struct {
	fn      HealthCheck
	timeout time.Duration
}

type health struct {
	mu     sync.RWMutex
	checks map[string]healthCheck
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
		a.health.checks = map[string]healthCheck{}
	}

	a.health.checks[name] = healthCheck{fn: check, timeout: timeout}
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
func runHealthCheck(ctx context.Context, check healthCheck) error {
	ctx, cancel := context.WithTimeout(ctx, check.timeout)
	defer cancel()

	done := make(chan error, 1)

	go func() {
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
}

// APIMiddleware installs the stack of JSON-only apps: recover, request
// id, logging and security headers on the server; the body limit on
// routes. There are no sessions or CSRF: API clients authenticate with
// tokens (see auth.TokenGuard), and CORS is configured by the routes.
func APIMiddleware(app *App, r *router.Router) error {
	r.UseServer(
		middleware.Recover(),
		middleware.RequestID(),
		middleware.Maintenance(maintenanceConfig(app)),
		middleware.Logger(app.Log),
		middleware.SecureHeaders(middleware.SecureHeadersConfig{}),
	)
	r.Use(middleware.BodyLimit(web.DefaultMaxBody))

	return nil
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

		secure := a.Config.IsProduction() || strings.HasPrefix(a.Config.URL, "https://")
		if cfg.Secure != "auto" {
			secure = cfg.Secure == "true" || cfg.Secure == "1"
		}

		sc := session.Config{
			CookieName:  cfg.CookieName,
			Secure:      secure,
			IdleTimeout: cfg.IdleTimeout,
			Lifetime:    cfg.Lifetime,
			Production:  a.Config.IsProduction(),
			Log:         a.Log,
			Store:       a.sessStore,
			Now:         a.now,
		}

		switch {
		case a.sessStore != nil:
		case cfg.Driver == "memory":
			sc.Store = session.NewMemoryStore()
		case cfg.Driver == "cookie":
			if sc.CookieKey, a.sessErr = a.DeriveKey("SESSION_DRIVER=cookie", 32); a.sessErr != nil {
				return
			}
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

// maintenanceConfig signs bypass cookies with a key derived from APP_KEY;
// without one the bypass is disabled (the app stays down for everyone).
func maintenanceConfig(app *App) middleware.MaintenanceConfig {
	key, _ := app.DeriveKey("maintenance", 32)

	return middleware.MaintenanceConfig{
		Key:    key,
		Secure: app.Config.IsProduction() || strings.HasPrefix(app.Config.URL, "https://"),
	}
}

// secureHeadersConfig allows the Vite dev server in the CSP while it runs
// (app.Vite only reports it outside production).
func secureHeadersConfig(app *App) middleware.SecureHeadersConfig {
	if app.Vite == nil {
		return middleware.SecureHeadersConfig{}
	}

	return middleware.SecureHeadersConfig{DevServer: app.Vite.HotURL}
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
}

// DefaultMiddlewareWith installs the DefaultMiddleware stacks with opts.
func DefaultMiddlewareWith(app *App, r *router.Router, opts MiddlewareOptions) error {
	if opts.Stateless && len(opts.SessionBypass) > 0 {
		return errors.New("vukem: SessionBypass has no effect with Stateless (no sessions anywhere)")
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

	if app.I18n != nil {
		r.Use(i18n.Middleware(app.I18n, i18n.MiddlewareConfig{}))
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
	}

	if debug && a.Kind == KindWeb {
		services.DebugOut = os.Stderr
	}

	r := router.New(services)
	r.Get("/up", a.healthHandler).Name("health")

	return r
}

// Handler returns the compiled HTTP handler.
func (a *App) Handler() (http.Handler, error) { return a.Router.Handler() }

func (a *App) httpRunner() (Runner, error) {
	h, err := a.Handler()
	if err != nil {
		return nil, err
	}

	srv := NewHTTPServer(a.Addr(), h)
	srv.ShutdownTimeout = a.Config.ShutdownTimeout

	if !a.Config.IsProduction() || a.Config.H2C {
		var p http.Protocols
		p.SetHTTP1(true)
		p.SetUnencryptedHTTP2(true)
		srv.Server.Protocols = &p
	}
	srv.Ready = a.ready

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
