package lumek

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/AlexGalhardo/golang-framework/config"
	"github.com/AlexGalhardo/golang-framework/router"
	"github.com/AlexGalhardo/golang-framework/session"
)

// Kind is the runtime in which the application is running. Providers can be
// limited to some kinds (see [KindFilter]).
type Kind string

// Runtime kinds.
const (
	KindWeb     Kind = "web"
	KindConsole Kind = "console"
	KindTest    Kind = "test"
)

// Config is the core application configuration, loaded from the environment.
type Config struct {
	Name            string        `env:"APP_NAME"         default:"Lumek"`
	Env             config.Env    `env:"APP_ENV"          default:"development"`
	Debug           bool          `env:"APP_DEBUG"`
	URL             string        `env:"APP_URL"          default:"http://localhost:3000"`
	Key             config.Secret `env:"APP_KEY"`
	Host            string        `env:"HOST"             default:"localhost"`
	Port            int           `env:"PORT"             default:"3000"`
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" default:"15s"`
	LogLevel        string        `env:"LOG_LEVEL"        default:"info"`
	LogFormat       string        `env:"LOG_FORMAT"`
}

// IsProduction reports whether APP_ENV is production.
func (c *Config) IsProduction() bool { return c.Env == config.Production }

// App is a Lumek application: configuration, logger, providers and the
// long-running runners (HTTP server, workers). Services added by providers
// live in typed fields of the structs that embed or reference the App.
type App struct {
	// Config is the core configuration.
	Config Config
	// Source gives access to every configuration value (for module configs).
	Source *config.Source
	// Key is the decoded APP_KEY; derive purpose-specific keys from it.
	Key config.Key
	// Log is the application logger.
	Log *slog.Logger
	// Kind is the runtime kind (web, console, test).
	Kind Kind
	// Router holds the HTTP routes; /up is registered by default.
	Router *router.Router

	providers []Provider
	runners   []namedRunner
	hooks     []func(context.Context) error
	commands  map[string]Command
	health    health
	stdout    io.Writer
	ready     chan<- string
	noHTTP    bool

	sessOnce  sync.Once
	sessions  *session.Manager
	sessStore session.Store
	sessErr   error

	mu       sync.Mutex
	booted   bool
	shutdown bool
}

// Option configures New.
type Option func(*options) error

type options struct {
	source    *config.Source
	fsys      fs.FS
	getenv    func(string) (string, bool)
	readFile  func(string) ([]byte, error)
	logger    *slog.Logger
	logOut    io.Writer
	kind      Kind
	providers []Provider
	runners   []namedRunner
	routes    []func(r *router.Router, app *App)
	stdout    io.Writer
	ready     chan<- string
	noHTTP    bool
}

// WithRoutes registers routes (and router middleware) after providers
// registered, so handlers can use the services they added.
func WithRoutes(fn func(r *router.Router, app *App)) Option {
	return func(o *options) error { o.routes = append(o.routes, fn); return nil }
}

// WithStdout sets where commands print (os.Stdout by default).
func WithStdout(w io.Writer) Option {
	return func(o *options) error { o.stdout = w; return nil }
}

// WithReady receives the HTTP listen address once the server is listening.
func WithReady(ch chan<- string) Option {
	return func(o *options) error { o.ready = ch; return nil }
}

// WithoutHTTP disables the HTTP server runner (workers-only processes).
func WithoutHTTP() Option {
	return func(o *options) error { o.noHTTP = true; return nil }
}

// WithSource uses an already resolved configuration source.
func WithSource(src *config.Source) Option {
	return func(o *options) error { o.source = src; return nil }
}

// WithEnv loads configuration from fsys (.env files) and getenv.
func WithEnv(fsys fs.FS, getenv func(string) (string, bool)) Option {
	return func(o *options) error { o.fsys, o.getenv = fsys, getenv; return nil }
}

// WithLogger sets the logger instead of building one from configuration.
func WithLogger(l *slog.Logger) Option {
	return func(o *options) error { o.logger = l; return nil }
}

// WithLogOutput sets where the default logger writes (stderr by default).
func WithLogOutput(w io.Writer) Option {
	return func(o *options) error { o.logOut = w; return nil }
}

// WithKind sets the runtime kind (web by default).
func WithKind(k Kind) Option {
	return func(o *options) error { o.kind = k; return nil }
}

// WithProviders appends providers, registered and booted in order.
func WithProviders(p ...Provider) Option {
	return func(o *options) error { o.providers = append(o.providers, p...); return nil }
}

// WithRunner adds a long-running component started by Run.
func WithRunner(name string, r Runner) Option {
	return func(o *options) error { o.runners = append(o.runners, namedRunner{name, r}); return nil }
}

// New builds an App: it loads and validates configuration and registers
// providers. It refuses insecure configuration (see docs/security.md, A02).
func New(opts ...Option) (*App, error) {
	o := options{kind: KindWeb}

	for _, opt := range opts {
		if err := opt(&o); err != nil {
			return nil, err
		}
	}

	src := o.source
	if src == nil {
		fsys, getenv := o.fsys, o.getenv
		if fsys == nil {
			fsys = os.DirFS(".")
		}

		if getenv == nil {
			getenv = os.LookupEnv
		}

		readFile := o.readFile
		if readFile == nil {
			readFile = os.ReadFile
		}

		var err error
		if src, err = config.LoadSource(fsys, getenv, readFile); err != nil {
			return nil, err
		}
	}

	cfg, err := config.Load[Config](src)
	if err != nil {
		return nil, err
	}

	key, err := checkConfig(&cfg)
	if err != nil {
		return nil, err
	}

	logger := o.logger
	if logger == nil {
		out := o.logOut
		if out == nil {
			out = os.Stderr
		}

		logger = newLogger(&cfg, out)
	}

	stdout := o.stdout
	if stdout == nil {
		stdout = os.Stdout
	}

	app := &App{
		Config:    cfg,
		Source:    src,
		Key:       key,
		Log:       logger,
		Kind:      o.kind,
		providers: o.providers,
		runners:   o.runners,
		stdout:    stdout,
		ready:     o.ready,
		noHTTP:    o.noHTTP,
	}
	app.Router = app.newRouter()

	for _, p := range app.providers {
		if !runsIn(p, app.Kind) {
			continue
		}

		if err := p.Register(app); err != nil {
			return nil, fmt.Errorf("lumek: register %T: %w", p, err)
		}
	}

	for _, fn := range o.routes {
		fn(app.Router, app)
	}

	return app, nil
}

// checkConfig enforces secure defaults at boot (fail closed).
func checkConfig(cfg *Config) (config.Key, error) {
	var errs []error

	key, err := config.ParseKey(cfg.Key)
	if err != nil {
		errs = append(errs, err)
	}

	if cfg.IsProduction() && cfg.Debug {
		errs = append(errs, errors.New("lumek: APP_DEBUG must be false when APP_ENV=production"))
	}

	if cfg.Port < 0 || cfg.Port > 65535 {
		errs = append(errs, fmt.Errorf("lumek: PORT %d is out of range", cfg.Port))
	}

	return key, errors.Join(errs...)
}

func newLogger(cfg *Config, out io.Writer) *slog.Logger {
	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
		level = slog.LevelInfo
	}

	if cfg.Debug {
		level = min(level, slog.LevelDebug)
	}

	hopts := &slog.HandlerOptions{Level: level}

	format := strings.ToLower(cfg.LogFormat)
	if format == "" {
		format = "text"
		if cfg.IsProduction() {
			format = "json"
		}
	}

	if format == "json" {
		return slog.New(slog.NewJSONHandler(out, hopts))
	}

	return slog.New(slog.NewTextHandler(out, hopts))
}

// Addr returns the HTTP listen address from HOST and PORT.
func (a *App) Addr() string { return fmt.Sprintf("%s:%d", a.Config.Host, a.Config.Port) }

// AddRunner adds a long-running component. Call it from Register or Boot.
func (a *App) AddRunner(name string, r Runner) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.runners = append(a.runners, namedRunner{name, r})
}

// OnShutdown registers fn to run during Shutdown, in reverse registration
// order, after runners stop and before providers shut down.
func (a *App) OnShutdown(fn func(context.Context) error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.hooks = append(a.hooks, fn)
}

// Boot runs every provider's Boot hook once, in order.
func (a *App) Boot(ctx context.Context) error {
	a.mu.Lock()
	if a.booted {
		a.mu.Unlock()
		return nil
	}

	a.booted = true
	a.mu.Unlock()

	for _, p := range a.providers {
		b, ok := p.(Booter)
		if !ok || !runsIn(p, a.Kind) {
			continue
		}

		if err := b.Boot(ctx, a); err != nil {
			return fmt.Errorf("lumek: boot %T: %w", p, err)
		}
	}

	return nil
}

// Run boots the app, starts every runner and blocks until ctx is cancelled
// or a runner fails; then it shuts everything down gracefully within
// SHUTDOWN_TIMEOUT. A cancelled ctx is a clean exit (nil error).
func (a *App) Run(ctx context.Context) error {
	if err := a.Boot(ctx); err != nil {
		return err
	}

	if a.Kind == KindWeb && !a.noHTTP {
		r, err := a.httpRunner()
		if err != nil {
			return err
		}

		a.AddRunner("http", r)
	}

	a.mu.Lock()
	runners := slices.Clone(a.runners)
	a.mu.Unlock()

	runCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	var (
		wg       sync.WaitGroup
		errMu    sync.Mutex
		firstErr error
	)

	for _, r := range runners {
		wg.Go(func() {
			err := r.runner.Run(runCtx)
			if err != nil && !errors.Is(err, context.Canceled) {
				errMu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("lumek: runner %s: %w", r.name, err)
				}
				errMu.Unlock()
				cancel(err)
			}
		})
	}

	<-runCtx.Done()
	a.Log.InfoContext(ctx, "shutting down", "cause", context.Cause(runCtx))

	// Runners observe runCtx and stop on their own; wait for them first.
	cancel(nil)
	wg.Wait()

	shutCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), a.Config.ShutdownTimeout)
	defer stop()

	return errors.Join(firstErr, a.Shutdown(shutCtx))
}

// Shutdown runs shutdown hooks and providers' Shutdown in reverse order.
// It is idempotent.
func (a *App) Shutdown(ctx context.Context) error {
	a.mu.Lock()
	if a.shutdown {
		a.mu.Unlock()
		return nil
	}

	a.shutdown = true
	hooks := slices.Clone(a.hooks)
	a.mu.Unlock()

	var errs []error

	for _, fn := range slices.Backward(hooks) {
		if err := fn(ctx); err != nil {
			errs = append(errs, err)
		}
	}

	for _, p := range slices.Backward(a.providers) {
		s, ok := p.(Shutdowner)
		if !ok || !runsIn(p, a.Kind) {
			continue
		}

		if err := s.Shutdown(ctx, a); err != nil {
			errs = append(errs, fmt.Errorf("lumek: shutdown %T: %w", p, err))
		}
	}

	return errors.Join(errs...)
}
