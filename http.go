package lumek

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/AlexGalhardo/golang-framework/middleware"
	"github.com/AlexGalhardo/golang-framework/router"
	"github.com/AlexGalhardo/golang-framework/web"
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

// DefaultMiddleware installs Lumek's recommended stacks on r: server stack
// Recover, RequestID, Logger, SecureHeaders, MethodOverride; router stack
// BodyLimit(10 MiB). Starters call it from bootstrap; customise freely.
func DefaultMiddleware(app *App, r *router.Router) {
	r.UseServer(
		middleware.Recover(),
		middleware.RequestID(),
		middleware.Logger(app.Log),
		middleware.SecureHeaders(middleware.SecureHeadersConfig{}),
		middleware.MethodOverride(),
	)
	r.Use(middleware.BodyLimit(web.DefaultMaxBody))
}

func (a *App) newRouter() *router.Router {
	services := &web.Services{
		Log:            a.Log,
		Debug:          a.Config.Debug && !a.Config.IsProduction(),
		ProblemBaseURL: a.Source.Get("APP_PROBLEM_BASE_URL"),
		CheckResponses: !a.Config.IsProduction(),
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
	if cmd != "serve" {
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
// returns the exit code. Use it as: func main() { os.Exit(lumek.Main(bootstrap.New)) }.
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
