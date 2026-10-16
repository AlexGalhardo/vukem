package vukem

import (
	"context"
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

// healthTimeout bounds every health check run by /up?checks=1.
const healthTimeout = 3 * time.Second

type health struct {
	mu     sync.RWMutex
	checks map[string]HealthCheck
}

// AddHealthCheck registers a check reported by GET /up?checks=1.
func (a *App) AddHealthCheck(name string, check HealthCheck) {
	a.health.mu.Lock()
	defer a.health.mu.Unlock()

	if a.health.checks == nil {
		a.health.checks = map[string]HealthCheck{}
	}

	a.health.checks[name] = check
}

// healthHandler answers 200 when the app booted. With ?checks=1 it runs
// every check (in parallel, with a timeout) and answers 503 if one fails;
// error details are not exposed, only "fail".
func (a *App) healthHandler(c *web.Ctx) error {
	if !c.QueryBool("checks") {
		return c.JSON(http.StatusOK, web.Map{"status": "ok"})
	}

	a.health.mu.RLock()
	checks := maps.Clone(a.health.checks)
	a.health.mu.RUnlock()

	ctx, cancel := context.WithTimeout(c.Context(), healthTimeout)
	defer cancel()

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results = map[string]string{}
		failed  bool
	)

	for _, name := range slices.Sorted(maps.Keys(checks)) {
		wg.Go(func() {
			status := "ok"
			if err := checks[name](ctx); err != nil {
				status = "fail"

				a.Log.WarnContext(ctx, "health check failed", "check", name, "err", err)
			}

			mu.Lock()
			defer mu.Unlock()

			results[name] = status
			failed = failed || status != "ok"
		})
	}

	wg.Wait()

	status, code := "ok", http.StatusOK
	if failed {
		status, code = "fail", http.StatusServiceUnavailable
	}

	c.SetHeader("Cache-Control", "no-store")

	return c.JSON(code, web.Map{"status": status, "checks": results})
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
			sc.CookieKey = a.Key.Derive("session.cookie", 32)
		case cfg.Driver == "database":
			sc.Store, a.sessErr = a.databaseSessionStore()
			if a.sessErr != nil {
				return
			}
		default:
			a.sessErr = fmt.Errorf("vukem: unknown SESSION_DRIVER %q", cfg.Driver)
			return
		}

		a.sessions, a.sessErr = session.New(sc)
	})

	return a.sessions, a.sessErr
}

// SetSessionStore makes sessions server-side with store (call it from a
// provider's Register, before Sessions is first used).
func (a *App) SetSessionStore(store session.Store) { a.sessStore = store }

// maintenanceConfig signs bypass cookies with a key derived from APP_KEY.
func maintenanceConfig(app *App) middleware.MaintenanceConfig {
	return middleware.MaintenanceConfig{
		Key:    app.Key.Derive("maintenance", 32),
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
func DefaultMiddleware(app *App, r *router.Router) error {
	r.UseServer(
		middleware.Recover(),
		middleware.RequestID(),
		middleware.Maintenance(maintenanceConfig(app)),
		middleware.Logger(app.Log),
		middleware.SecureHeaders(secureHeadersConfig(app)),
		middleware.MethodOverride(),
	)

	sessions, err := app.Sessions()
	if err != nil {
		return err
	}

	var origins []string
	for o := range strings.SplitSeq(app.Source.Get("CSRF_TRUSTED_ORIGINS"), ",") {
		if o = strings.TrimSpace(o); o != "" {
			origins = append(origins, o)
		}
	}

	csrfMW, err := csrf.Middleware(csrf.Config{TrustedOrigins: origins})
	if err != nil {
		return err
	}

	r.Use(middleware.BodyLimit(web.DefaultMaxBody), sessions.Middleware(), csrfMW)

	if app.I18n != nil {
		r.Use(i18n.Middleware(app.I18n, i18n.MiddlewareConfig{}))
	}

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

// databaseSessionStore stores sessions in the app database (SQLite or a
// driver such as Postgres or MySQL), creating the sessions table when missing.
//
// shortcut: expired rows are only pruned by SQLStore.Prune; schedule it
// once the scheduler lands (BETA B.1.4).
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
