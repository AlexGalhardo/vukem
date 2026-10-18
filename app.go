package vukem

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AlexGalhardo/vukem/cache"
	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/crypt"
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/devtools"
	"github.com/AlexGalhardo/vukem/events"
	"github.com/AlexGalhardo/vukem/flags"
	"github.com/AlexGalhardo/vukem/i18n"
	"github.com/AlexGalhardo/vukem/inertia"
	"github.com/AlexGalhardo/vukem/logging"
	"github.com/AlexGalhardo/vukem/logging/telegram"
	"github.com/AlexGalhardo/vukem/mail"
	"github.com/AlexGalhardo/vukem/queue"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/schedule"
	"github.com/AlexGalhardo/vukem/schema"
	"github.com/AlexGalhardo/vukem/session"
	"github.com/AlexGalhardo/vukem/sse"
	"github.com/AlexGalhardo/vukem/view"
	"github.com/AlexGalhardo/vukem/vite"
)

// Kind is the runtime in which the application is running. Providers can be
// limited to some kinds (see [KindFilter]).
type Kind string

// Runtime kinds.
const (
	KindWeb     Kind = "web"
	KindConsole Kind = "console"
	KindTest    Kind = "test"
	// KindWarmup builds the app (config, Register, routes) but never runs
	// providers' Boot or Shutdown, so no connection is opened. Commands that
	// only read the route table (codegen:routes, codegen:client,
	// codegen:types, openapi:export) run in it, and it tolerates a missing
	// APP_KEY. Boot and Run return ErrWarmupRun; never expose its Router.
	KindWarmup Kind = "warmup"
)

// Config is the core application configuration, loaded from the environment.
type Config struct {
	Name  string        `env:"APP_NAME"  default:"Vukem"`
	Env   config.Env    `env:"APP_ENV"   default:"development"`
	Debug bool          `env:"APP_DEBUG"`
	URL   string        `env:"APP_URL"   default:"http://localhost:3000"`
	Key   config.Secret `env:"APP_KEY"`
	// PreviousKeys are former APP_KEY values (comma-separated, same format)
	// that still open what they sealed or signed after a key rotation:
	// app.Crypt tokens (encrypted cookies and columns, queued mail), cookie
	// sessions, the maintenance bypass and, through DerivePreviousKeys,
	// signed URLs and social login state. New values always use APP_KEY.
	PreviousKeys    config.Secret `env:"APP_PREVIOUS_KEYS"`
	Host            string        `env:"HOST"              default:"localhost"`
	Port            int           `env:"PORT"              default:"3000"`
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT"  default:"15s"`
	// H2C serves HTTP/2 without TLS (prior knowledge, for gRPC clients) in
	// production too, behind a proxy that speaks h2c. It is always on
	// outside production.
	H2C bool `env:"HTTP_H2C"`
	// MaxHeaderBytes caps the request line plus headers the HTTP server
	// reads (4096 to 1048576 bytes, Go's own default; outside is a boot
	// error). Larger requests get 431.
	MaxHeaderBytes int    `env:"HTTP_MAX_HEADER_BYTES" default:"65536"`
	LogLevel       string `env:"LOG_LEVEL"             default:"info"`
	LogFormat      string `env:"LOG_FORMAT"`
	// LogDedupWindow collapses repeated identical ERROR records of the
	// default logger into one summary per window (a Go duration; "0"
	// disables it). Empty means 1m in production and off elsewhere.
	LogDedupWindow string `env:"LOG_DEDUP_WINDOW"`
	// APIVersion is info.version of the OpenAPI document.
	APIVersion string `env:"API_VERSION" default:"1.0.0"`
	// OpenAPIPrefix keeps only routes under it in the document (e.g. /api).
	OpenAPIPrefix string `env:"OPENAPI_PREFIX"`
	// OpenAPIInProduction serves /openapi.json in production too.
	OpenAPIInProduction bool `env:"OPENAPI_IN_PRODUCTION"`
	// Devtools enables the /_vukem dashboard (refused in production).
	Devtools bool `env:"DEVTOOLS"`
	// DevtoolsInsecureAllowRemote serves the dashboard to non-loopback
	// clients (containers, remote VMs). Anyone who reaches the app then
	// sees its requests, queries, logs and mail.
	DevtoolsInsecureAllowRemote bool `env:"DEVTOOLS_INSECURE_ALLOW_REMOTE"`
	// TrustedProxies lists the proxies (comma-separated CIDRs or IPs) whose
	// X-Forwarded-For is honoured by c.IP(). Empty trusts nobody; an
	// invalid entry is a boot error.
	TrustedProxies string `env:"TRUSTED_PROXIES"`
	// TrustForwardedProto treats X-Forwarded-Proto: https from a trusted
	// proxy as HTTPS (HSTS). It requires TRUSTED_PROXIES.
	TrustForwardedProto bool `env:"TRUST_FORWARDED_PROTO"`
	// HSTSForce sends HSTS on every response in production, for TLS
	// proxies whose range cannot be put in TRUSTED_PROXIES. It requires an
	// https APP_URL in every environment but is applied only in production
	// (docs/security.md, A02).
	HSTSForce bool `env:"HSTS_FORCE"`
	// ClientIPHeader names a single-IP header set by the trusted proxy
	// (such as X-Real-IP) that c.IP() reads instead of X-Forwarded-For.
	// It requires TRUSTED_PROXIES.
	ClientIPHeader string `env:"CLIENT_IP_HEADER"`
	// RateLimitIPv6Prefix is the prefix length ratelimit.ByIP groups IPv6
	// clients by (1-128): one client usually owns a whole /64 and can
	// rotate addresses inside it. IPv4 keys stay per address.
	RateLimitIPv6Prefix int `env:"RATE_LIMIT_IPV6_PREFIX" default:"64"`
}

// IsProduction reports whether APP_ENV is production.
func (c *Config) IsProduction() bool { return c.Env == config.Production }

// App is a Vukem application: configuration, logger, providers and the
// long-running runners (HTTP server, workers). Services added by providers
// live in typed fields of the structs that embed or reference the App.
type App struct {
	// Config is the core configuration.
	Config Config
	// Source gives access to every configuration value (for module configs).
	Source *config.Source
	// Key is the decoded APP_KEY; derive purpose-specific keys from it
	// (DeriveKey returns an error instead of panicking when it is unset).
	// It is zero only outside production when APP_KEY is unset.
	Key config.Key
	// Crypt encrypts with a key derived from APP_KEY and still decrypts
	// values sealed with APP_PREVIOUS_KEYS. DB() hands it to orm.Encrypted.
	// It is nil when Key is zero; its methods then fail with crypt.ErrNoKey.
	Crypt *crypt.Encrypter
	// Log is the application logger.
	Log *slog.Logger
	// Kind is the runtime kind (web, console, test).
	Kind Kind
	// Router holds the HTTP routes; /up is registered by default.
	Router *router.Router
	// Views renders templates when WithViews was given (nil otherwise).
	Views *view.Engine
	// Vite renders asset tags when WithVite was given (nil otherwise).
	Vite *vite.Vite
	// Inertia is the Inertia adapter when WithInertia was given (nil
	// otherwise).
	Inertia *inertia.Inertia
	// I18n holds the translations when WithTranslations was given (nil
	// otherwise); DefaultMiddleware then resolves the request locale.
	I18n *i18n.Bundle

	providers []Provider
	runners   []namedRunner
	hooks     []func(context.Context) error
	commands  map[string]Command
	health    health
	stdout    io.Writer
	stderr    io.Writer
	stdin     io.Reader
	ready     chan<- string
	noHTTP    bool

	// bootRoutes run at the end of Boot (WithRoutesAfterBoot).
	bootRoutes []func(r *router.Router, app *App) error

	sessOnce  sync.Once
	sessions  *session.Manager
	sessStore session.Store
	// previous are the decoded APP_PREVIOUS_KEYS (DerivePreviousKeys).
	previous []config.Key
	// sessStoreFunc builds sessStore on first use (WithSessionStoreFunc).
	sessStoreFunc func(*App) (session.Store, error)
	// sessPruner is the session store when it keeps expired rows (database).
	sessPruner sessionPruner
	sessErr    error
	mail       mailState
	inst       instruments
	cacheOnce  sync.Once
	cache      *cache.Cache
	cacheErr   error
	flagDefs   []flags.AnyFlag
	flagsOnce  sync.Once
	ff         *flags.Flags
	flagsErr   error

	// Telegram is the Telegram log handler when TELEGRAM_* is set (nil
	// otherwise); use it for Contact and Send.
	Telegram *telegram.Handler
	// telegramLogs reports whether log records are forwarded to Telegram
	// (TELEGRAM_LOG).
	telegramLogs bool

	// Events is the application event bus (WithListeners registers listeners).
	Events *events.Bus

	// Devtools is the development dashboard when WithDevtools or
	// DEVTOOLS=true enabled it (nil otherwise).
	Devtools *devtools.Devtools

	jobs        []queue.Registration
	jobFuncs    []func(*App) []queue.Registration
	queueDriver queue.Driver
	cacheStore  cache.Store
	// Built on first use of Queue() / Cache() (WithQueueDriverFunc,
	// WithCacheStoreFunc).
	queueDriverFunc func(*App) (queue.Driver, error)
	cacheStoreFunc  func(*App) (cache.Store, error)
	schedules       []func(*schedule.Schedule, *App)
	scheduleOnce    sync.Once
	schedule        *schedule.Schedule
	scheduleErr     error

	queueOnce sync.Once
	queue     *queue.Queue
	queueErr  error
	// queueDB is the database queue driver Boot prepared (tables created).
	queueDB *queue.Database
	// queueFunc* hold the queueDriverFunc result, resolved once at Boot
	// (QUEUE_CONNECTION=database) or by the first Queue call.
	queueFuncOnce   sync.Once
	queueFuncDriver queue.Driver
	queueFuncErr    error

	now func() time.Time
	// proxies is the parsed TRUSTED_PROXIES.
	proxies []*net.IPNet

	dbOnce     sync.Once
	db         *database.DB
	dbErr      error
	drivers    []database.Driver
	queryObs   []database.QueryObserver
	migrations []schema.Migration
	seeders    []AppSeeder

	mu     sync.Mutex
	booted bool
	// bootMu serializes Boot, so a concurrent call waits for the first
	// one and gets its error.
	bootMu   sync.Mutex
	bootErr  error
	shutdown bool
}

// Option configures New.
type Option func(*options) error

type options struct {
	source        *config.Source
	fsys          fs.FS
	getenv        func(string) (string, bool)
	readFile      func(string) ([]byte, error)
	logger        *slog.Logger
	logOut        io.Writer
	kind          Kind
	providers     []Provider
	runners       []namedRunner
	routes        []func(r *router.Router, app *App) error
	bootRoutes    []func(r *router.Router, app *App) error
	views         fs.FS
	vite          fs.FS
	inertia       *inertia.Config
	lang          fs.FS
	now           func() time.Time
	migrations    []schema.Migration
	seeders       []AppSeeder
	mailTransport mail.Transport
	listeners     []events.Registration
	jobs          []queue.Registration
	jobFuncs      []func(*App) []queue.Registration
	queueDriver   queue.Driver
	cacheStore    cache.Store
	sessionStore  session.Store
	queueFunc     func(*App) (queue.Driver, error)
	cacheFunc     func(*App) (cache.Store, error)
	sessionFunc   func(*App) (session.Store, error)
	schedules     []func(*schedule.Schedule, *App)
	drivers       []database.Driver
	queryObs      []database.QueryObserver
	stdout        io.Writer
	stderr        io.Writer
	stdin         io.Reader
	ready         chan<- string
	noHTTP        bool
	devtools      bool
	telegram      []func(*telegram.Config)
	flags         []flags.AnyFlag
}

// WithRoutes registers routes (and router middleware) after providers
// registered, so handlers can use the services they added. An error stops
// New.
func WithRoutes(fn func(r *router.Router, app *App) error) Option {
	return func(o *options) error { o.routes = append(o.routes, fn); return nil }
}

// WithRoutesAfterBoot registers routes at the end of Boot, after every
// provider's Boot, so handlers can use clients a provider opens in Boot (a
// database or object-store client). The routes exist only once the app
// booted: Run, commands and vukemtest boot first; Handler fails with
// [ErrNotBooted] before that, and Boot fails with [ErrRoutesCompiled] when
// the router was compiled earlier. Global router middleware applies to
// them; groups made in WithRoutes do not. In [KindWarmup], which never
// boots, they run at the end of New so the route table is complete; booted
// services are then still unset (check app.Kind) and a panic is returned
// as an error.
func WithRoutesAfterBoot(fn func(r *router.Router, app *App) error) Option {
	return func(o *options) error { o.bootRoutes = append(o.bootRoutes, fn); return nil }
}

// ErrRoutesCompiled is returned by Boot when routes from
// WithRoutesAfterBoot are pending but the router was already compiled
// (Handler, Router.URL or a request before Boot), so they would never be
// served.
var ErrRoutesCompiled = errors.New("vukem: router compiled before Boot, so routes after boot would be lost; " +
	"call Boot before Handler/Router.URL; avoid URL() inside WithRoutes")

// ErrNotBooted is returned by Handler when routes from WithRoutesAfterBoot
// are pending because Boot has not run: serving then would answer 404 for
// them.
var ErrNotBooted = errors.New("vukem: routes after boot are pending; call Boot before Handler")

// WithViews renders html/template views from fsys (os.DirFS("resources/views")
// or an embed.FS). Views reload on every request outside production.
func WithViews(fsys fs.FS) Option {
	return func(o *options) error { o.views = fsys; return nil }
}

// WithInertia enables the Inertia adapter (app.Inertia); add
// app.Inertia.Middleware to the routes that render pages. Errors on Inertia
// requests render inertia.Config.ErrorComponent (default "Error", see
// inertia.Inertia.ErrorPage): New installs it as
// app.Router.Services().ErrorPage when no hook is set yet, before the
// routes run, so routes may replace it or set it to nil to keep the HTML
// error page. Outside production a web app warns once at boot when the
// project (the WithEnv file system, else the working directory) has
// resources/js/pages but no page for that component. Version
// and the SSR dev endpoint default to app.Vite when WithVite is given.
func WithInertia(cfg inertia.Config) Option {
	return func(o *options) error { o.inertia = &cfg; return nil }
}

// WithVite enables the "vite" and "viteReactRefresh" view functions over the
// public directory fsys (os.DirFS("public") or an embed.FS). The dev server
// "hot" file is honoured only outside production.
func WithVite(fsys fs.FS) Option {
	return func(o *options) error { o.vite = fsys; return nil }
}

// WithTranslations loads the JSON catalogs at the root of fsys
// (os.DirFS("resources/lang") or an embed.FS) into App.I18n, with
// APP_LOCALE (default "en") as the fallback locale, plus the framework's
// built-in messages for keys the catalogs do not define
// (i18n.Bundle.AddBuiltin). Views get the "t" and "locale" functions, and
// DefaultMiddleware installs i18n.Middleware.
func WithTranslations(fsys fs.FS) Option {
	return func(o *options) error { o.lang = fsys; return nil }
}

// WithClock sets the clock used by sessions and other time-based features
// (tests use vukemtest.FakeClock).
func WithClock(now func() time.Time) Option {
	return func(o *options) error { o.now = now; return nil }
}

// WithStdout sets where commands print (os.Stdout by default).
func WithStdout(w io.Writer) Option {
	return func(o *options) error { o.stdout = w; return nil }
}

// WithStderr sets where commands print diagnostics (os.Stderr by
// default).
func WithStderr(w io.Writer) Option {
	return func(o *options) error { o.stderr = w; return nil }
}

// WithStdin sets where commands read input (os.Stdin by default).
func WithStdin(r io.Reader) Option {
	return func(o *options) error { o.stdin = r; return nil }
}

// WithReady receives the HTTP listen address once the server is listening.
func WithReady(ch chan<- string) Option {
	return func(o *options) error { o.ready = ch; return nil }
}

// WithDevtools enables the development dashboard at /_vukem (same as
// DEVTOOLS=true). New fails when APP_ENV=production.
func WithDevtools() Option {
	return func(o *options) error { o.devtools = true; return nil }
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

	cfg.Devtools = cfg.Devtools || o.devtools

	if err := checkFlags(o.flags, src); err != nil {
		return nil, err
	}

	key, err := checkConfig(&cfg, src.EnvSet(), o.kind == KindWarmup)
	if err != nil {
		return nil, err
	}

	proxies, err := parseTrustedProxies(cfg.TrustedProxies)
	if err != nil {
		return nil, err
	}

	if cfg.TrustForwardedProto && len(proxies) == 0 {
		return nil, errors.New("vukem: TRUST_FORWARDED_PROTO requires TRUSTED_PROXIES")
	}

	if cfg.HSTSForce && !strings.HasPrefix(cfg.URL, "https://") {
		return nil, errors.New("vukem: HSTS_FORCE requires an https APP_URL")
	}

	if cfg.ClientIPHeader, err = clientIPHeader(cfg.ClientIPHeader, len(proxies) > 0); err != nil {
		return nil, err
	}

	if cfg.MaxHeaderBytes < minMaxHeaderBytes || cfg.MaxHeaderBytes > http.DefaultMaxHeaderBytes {
		return nil, fmt.Errorf("vukem: HTTP_MAX_HEADER_BYTES %d must be between %d and %d",
			cfg.MaxHeaderBytes, minMaxHeaderBytes, http.DefaultMaxHeaderBytes)
	}

	if cfg.RateLimitIPv6Prefix < 1 || cfg.RateLimitIPv6Prefix > 128 {
		return nil, fmt.Errorf("vukem: RATE_LIMIT_IPV6_PREFIX %d must be between 1 and 128", cfg.RateLimitIPv6Prefix)
	}

	previous, err := parsePreviousKeys(cfg.PreviousKeys)
	if err != nil {
		return nil, err
	}

	enc, err := newCrypt(key, previous)
	if err != nil {
		return nil, err
	}

	dedupWindow, err := logDedupWindow(&cfg)
	if err != nil {
		return nil, err
	}

	logger := o.logger
	ownLogger := logger == nil

	if ownLogger {
		out := o.logOut
		if out == nil {
			out = os.Stderr
		}

		logger = newLogger(&cfg, out)
	}

	tg, tgLogs, err := telegramHandler(src, &cfg, o.telegram)
	if err != nil {
		return nil, err
	}

	if tgLogs {
		logger = slog.New(slog.NewMultiHandler(logger.Handler(), tg))
	}

	// Dedup sits in front of Telegram too, so an error burst is one chat
	// message plus a summary. A WithLogger logger is used as given.
	var dedup *logging.Dedup
	if ownLogger && dedupWindow > 0 {
		dedup = logging.NewDedup(logger.Handler(), logging.DedupOptions{Window: dedupWindow})
		logger = slog.New(dedup)
	}

	stdout := o.stdout
	if stdout == nil {
		stdout = os.Stdout
	}

	errOut := o.stderr
	if errOut == nil {
		errOut = os.Stderr
	}

	stdin := o.stdin
	if stdin == nil {
		stdin = os.Stdin
	}

	now := o.now
	if now == nil {
		now = time.Now
	}

	var (
		dt     *devtools.Devtools
		routes *router.Router
	)

	if cfg.Devtools {
		dt = devtools.New(devtools.Config{
			InsecureAllowRemote: cfg.DevtoolsInsecureAllowRemote,
			Level:               logLevel(&cfg),
			Routes:              func() []router.RouteInfo { return routes.Routes() },
			Now:                 now,
		})
		logger = slog.New(slog.NewMultiHandler(logger.Handler(), dt.LogHandler()))
	}

	app := &App{
		Config:    cfg,
		Source:    src,
		Key:       key,
		Crypt:     enc,
		previous:  previous,
		Log:       logger,
		Kind:      o.kind,
		providers: o.providers,
		runners:   o.runners,
		stdout:    stdout,
		stderr:    errOut,
		stdin:     stdin,
		ready:     o.ready,
		noHTTP:    o.noHTTP,
		drivers:   o.drivers,
		queryObs:  o.queryObs,
		now:       now,
		proxies:   proxies,

		migrations:  o.migrations,
		seeders:     o.seeders,
		jobs:        o.jobs,
		jobFuncs:    o.jobFuncs,
		queueDriver: o.queueDriver,
		cacheStore:  o.cacheStore,
		sessStore:   o.sessionStore,

		queueDriverFunc: o.queueFunc,
		cacheStoreFunc:  o.cacheFunc,
		sessStoreFunc:   o.sessionFunc,
		schedules:       o.schedules,
		mail:            mailState{transport: o.mailTransport},
		flagDefs:        o.flags,
	}
	if key.IsZero() && cfg.Env == config.Development {
		app.Log.Warn("APP_KEY is not set: sessions, encryption and signed URLs fail until you run `vukem key:generate`",
			"env", string(cfg.Env))
	}

	app.Router = app.newRouter()

	if catchAll := slices.ContainsFunc(proxies, func(n *net.IPNet) bool {
		ones, _ := n.Mask.Size()
		return ones == 0
	}); catchAll {
		app.Log.Warn("TRUSTED_PROXIES trusts every address: any client can set its own c.IP(); " +
			"trust only the proxy's network")
	}

	if cfg.HSTSForce && !cfg.IsProduction() {
		app.Log.Warn("HSTS_FORCE is ignored outside production: HSTS is not forced", "env", string(cfg.Env))
	}
	routes = app.Router

	if dt != nil {
		app.enableDevtools(dt)
	}

	if tg != nil {
		app.Telegram = tg
		app.telegramLogs = tgLogs
		app.OnShutdown(tg.Close)
	}

	if dedup != nil {
		// Registered after Telegram so it runs first: the pending summaries
		// still reach the chat.
		app.OnShutdown(dedup.Close)
	}

	app.Events = events.New(app.Log, 0)
	app.Events.Register(o.listeners...)

	if dt != nil {
		app.Events.SetObserver(dt)
	}
	app.Router.Services().Events = app.Events
	app.Events.SetQueue(func() (events.Enqueuer, error) { return app.Queue() })
	app.OnShutdown(app.Events.Shutdown)

	qcfg, err := configLoad[QueueConfig](app)
	if err != nil {
		return nil, err
	}

	if app.Kind == KindWeb && qcfg.inServe(o.queueDriver != nil || o.queueFunc != nil) {
		app.AddRunner("queue", app.queueRunner(qcfg))
	}

	scfg, err := configLoad[ScheduleConfig](app)
	if err != nil {
		return nil, err
	}

	if scfg.InServe && app.Kind == KindWeb {
		app.AddRunner("schedule", app.scheduleRunner())
	}

	if o.vite != nil {
		if app.Vite, err = vite.New(vite.Config{FS: o.vite, Dev: !cfg.IsProduction()}); err != nil {
			return nil, err
		}
	}

	if o.lang != nil {
		if app.I18n, err = i18n.Load(o.lang, i18n.Options{Default: app.Source.Get("APP_LOCALE")}); err != nil {
			return nil, err
		}

		if err := app.I18n.AddBuiltin(); err != nil {
			return nil, err
		}
	}

	if o.inertia != nil {
		icfg := *o.inertia
		if icfg.Version == nil && app.Vite != nil {
			icfg.Version = app.Vite.Version
		}

		if icfg.SSR != nil && icfg.SSR.HotURL == nil && app.Vite != nil {
			ssr := *icfg.SSR
			ssr.HotURL = app.Vite.HotURL
			icfg.SSR = &ssr
		}

		if app.Inertia, err = inertia.New(icfg); err != nil {
			return nil, err
		}

		// Set only when nil, so a hook installed earlier is kept; routes run
		// later and may replace it.
		if app.Router.Services().ErrorPage == nil {
			app.Router.Services().ErrorPage = app.Inertia.ErrorPage
		}

		// Without WithEnv the project is the working directory, as for config.
		project := o.fsys
		if project == nil {
			project = os.DirFS(".")
		}

		if comp := cmp.Or(icfg.ErrorComponent, "Error"); !cfg.IsProduction() && app.Kind == KindWeb &&
			inertiaPageMissing(project, comp) {
			app.Log.Warn("inertia error page not found: errors on Inertia visits will not render",
				"component", comp, "want", inertiaPagesDir+"/"+comp+".{tsx,vue,svelte}",
				"fix", "add the page, set inertia.Config.ErrorComponent, or set app.Router.Services().ErrorPage = nil")
		}
	}

	if o.views != nil {
		engine, err := view.New(view.Config{
			FS: o.views, Reload: !cfg.IsProduction(), URLs: app.Router, Vite: app.Vite, I18n: app.I18n,
		})
		if err != nil {
			return nil, err
		}

		app.Views = engine
		app.Router.Services().Views = engine
	}

	for _, p := range app.providers {
		if !runsIn(p, app.Kind) {
			continue
		}

		if err := p.Register(app); err != nil {
			return nil, fmt.Errorf("vukem: register %T: %w", p, err)
		}
	}

	for _, fn := range o.routes {
		if err := fn(app.Router, app); err != nil {
			return nil, fmt.Errorf("vukem: routes: %w", err)
		}
	}

	app.bootRoutes = o.bootRoutes
	if app.Kind == KindWarmup {
		if err := app.addBootRoutes(); err != nil {
			return nil, err
		}
	}

	return app, nil
}

// checkConfig enforces secure defaults at boot (fail closed). envSet
// reports whether APP_ENV was given explicitly (config.Source.EnvSet);
// warmup whether the app is built in KindWarmup.
func checkConfig(cfg *Config, envSet, warmup bool) (config.Key, error) {
	var errs []error

	// A missing key is allowed only with an explicit APP_ENV=development or
	// test (a forgotten APP_ENV in production must not boot keyless); every
	// key-derived feature then fails closed (Key.Derive panics, Crypt is nil,
	// cookie sessions refuse to start). A warmup app (codegen) never boots
	// providers or serves, so it tolerates a missing key in any env; routes
	// that need the key at build time still fail closed. A malformed key is
	// always an error.
	key, err := config.ParseKey(cfg.Key)
	keyOptional := warmup || (envSet && !cfg.IsProduction())

	if err != nil && (!errors.Is(err, config.ErrKeyMissing) || !keyOptional) {
		errs = append(errs, err)
	}

	// Any other value would silently skip every production check below.
	switch cfg.Env {
	case config.Development, config.Test, config.Production:
	default:
		errs = append(errs, fmt.Errorf("vukem: APP_ENV %q must be development, test or production", cfg.Env))
	}

	if cfg.IsProduction() && cfg.Debug {
		errs = append(errs, errors.New("vukem: APP_DEBUG must be false when APP_ENV=production"))
	}

	if cfg.IsProduction() && cfg.Devtools {
		errs = append(errs, errors.New("vukem: devtools (WithDevtools, DEVTOOLS) must be off when APP_ENV=production"))
	}

	if cfg.Port < 0 || cfg.Port > 65535 {
		errs = append(errs, fmt.Errorf("vukem: PORT %d is out of range", cfg.Port))
	}

	return key, errors.Join(errs...)
}

// parseTrustedProxies parses TRUSTED_PROXIES: comma-separated CIDRs or
// bare IPs (a single host). Empty entries are skipped; anything else that
// does not parse refuses the boot, since a dropped entry would silently
// change which client IPs rate limits and logs see.
func parseTrustedProxies(s string) ([]*net.IPNet, error) {
	var nets []*net.IPNet

	for entry := range strings.SplitSeq(s, ",") {
		if entry = strings.TrimSpace(entry); entry == "" {
			continue
		}

		if _, n, err := net.ParseCIDR(entry); err == nil {
			nets = append(nets, n)
			continue
		}

		ip := net.ParseIP(entry)
		if ip == nil {
			return nil, fmt.Errorf("vukem: TRUSTED_PROXIES entry %q is not an IP or CIDR", entry)
		}

		bits := 128
		if ip4 := ip.To4(); ip4 != nil {
			ip, bits = ip4, 32
		}

		nets = append(nets, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
	}

	return nets, nil
}

// headerToken matches an HTTP field name (RFC 9110 token).
var headerToken = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")

// clientIPHeader validates CLIENT_IP_HEADER and returns it canonicalized.
// List headers are refused: their leftmost entries come from the client.
func clientIPHeader(name string, haveProxies bool) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", nil
	}

	if !headerToken.MatchString(name) {
		return "", fmt.Errorf("vukem: CLIENT_IP_HEADER %q is not a valid header name", name)
	}

	name = http.CanonicalHeaderKey(name)
	if name == "X-Forwarded-For" || name == "Forwarded" {
		return "", fmt.Errorf("vukem: CLIENT_IP_HEADER %q is a list; leave it empty to use X-Forwarded-For", name)
	}

	if !haveProxies {
		return "", errors.New("vukem: CLIENT_IP_HEADER requires TRUSTED_PROXIES")
	}

	return name, nil
}

// parsePreviousKeys decodes APP_PREVIOUS_KEYS (comma-separated APP_KEY
// values; blanks are skipped). A malformed entry is a boot error.
func parsePreviousKeys(previous config.Secret) ([]config.Key, error) {
	var keys []config.Key

	for i, s := range strings.Split(previous.Reveal(), ",") {
		if s = strings.TrimSpace(s); s == "" {
			continue
		}

		k, err := config.ParseKey(config.NewSecret(s))
		if err != nil {
			return nil, fmt.Errorf("vukem: APP_PREVIOUS_KEYS entry %d: %w", i+1, err)
		}

		keys = append(keys, k)
	}

	return keys, nil
}

// newCrypt builds app.Crypt: the "crypt" key derived from APP_KEY encrypts,
// keys derived from APP_PREVIOUS_KEYS only decrypt. Without APP_KEY it
// returns nil (see crypt.ErrNoKey).
func newCrypt(key config.Key, previous []config.Key) (*crypt.Encrypter, error) {
	if key.IsZero() {
		return nil, nil
	}

	return crypt.New(key.Derive("crypt", crypt.KeySize), derivePrevious(previous, "crypt", crypt.KeySize)...)
}

func derivePrevious(previous []config.Key, purpose string, n int) [][]byte {
	if len(previous) == 0 {
		return nil
	}

	out := make([][]byte, len(previous))
	for i, k := range previous {
		out[i] = k.Derive(purpose, n)
	}

	return out
}

// logLevel is LOG_LEVEL (default info), lowered to debug by APP_DEBUG.
func logLevel(cfg *Config) slog.Level {
	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
		level = slog.LevelInfo
	}

	if cfg.Debug {
		level = min(level, slog.LevelDebug)
	}

	return level
}

// logDedupWindow is LOG_DEDUP_WINDOW: empty means
// logging.DefaultDedupWindow in production and off elsewhere.
func logDedupWindow(cfg *Config) (time.Duration, error) {
	if cfg.LogDedupWindow == "" {
		if cfg.IsProduction() {
			return logging.DefaultDedupWindow, nil
		}

		return 0, nil
	}

	d, err := time.ParseDuration(cfg.LogDedupWindow)
	if err != nil || d < 0 {
		return 0, fmt.Errorf(
			"vukem: LOG_DEDUP_WINDOW %q: want a duration such as 1m, or 0 to disable",
			cfg.LogDedupWindow,
		)
	}

	return d, nil
}

func newLogger(cfg *Config, out io.Writer) *slog.Logger {
	hopts := &slog.HandlerOptions{Level: logLevel(cfg)}

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

// Stdout is where commands print results (WithStdout, os.Stdout by
// default). Commands write here instead of os.Stdout so tests can capture
// or silence them.
func (a *App) Stdout() io.Writer { return a.stdout }

// Stderr is where commands print diagnostics (WithStderr, os.Stderr by
// default).
func (a *App) Stderr() io.Writer { return a.stderr }

// Stdin is where commands read input (WithStdin, os.Stdin by default).
func (a *App) Stdin() io.Reader { return a.stdin }

// DeriveKey returns an n-byte key for purpose derived from APP_KEY (see
// config.Key.Derive). It fails with config.ErrKeyMissing when the app
// booted without APP_KEY, so a key-derived feature fails closed.
func (a *App) DeriveKey(purpose string, n int) ([]byte, error) {
	if a.Key.IsZero() {
		return nil, fmt.Errorf("vukem: %s: %w", purpose, config.ErrKeyMissing)
	}

	return a.Key.Derive(purpose, n), nil
}

// DerivePreviousKeys returns the n-byte keys for purpose derived from each
// APP_PREVIOUS_KEYS entry, in order, or nil when none is set. Pass them to
// the verify-only side of a key-derived feature (signed.Signer and
// social.Social WithPreviousKeys) so values written before an APP_KEY
// rotation keep working until the old key is removed. They must never sign
// or encrypt.
func (a *App) DerivePreviousKeys(purpose string, n int) [][]byte {
	return derivePrevious(a.previous, purpose, n)
}

// Now returns the current time from the app clock (see WithClock).
func (a *App) Now() time.Time { return a.now() }

// Addr returns the HTTP listen address from HOST and PORT. IPv6 hosts
// ("::", "fd12::1") are bracketed; a host already in brackets is accepted.
func (a *App) Addr() string {
	host := strings.TrimSuffix(strings.TrimPrefix(a.Config.Host, "["), "]")
	return net.JoinHostPort(host, strconv.Itoa(a.Config.Port))
}

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

// Boot runs every provider's Boot hook once, in order, then the
// WithRoutesAfterBoot functions. Boot is not retryable: later calls return
// the first call's error (nil after a successful boot). A KindWarmup app
// never boots: Boot returns [ErrWarmupRun].
func (a *App) Boot(ctx context.Context) error {
	if a.Kind == KindWarmup {
		return ErrWarmupRun
	}

	a.bootMu.Lock()
	defer a.bootMu.Unlock()

	a.mu.Lock()
	booted := a.booted
	a.booted = true
	a.mu.Unlock()

	if booted {
		return a.bootErr
	}

	a.bootErr = a.boot(ctx)

	return a.bootErr
}

func (a *App) boot(ctx context.Context) error {
	if err := a.checkMailConfig(); err != nil {
		return err
	}

	if err := a.prepareDatabaseQueue(ctx); err != nil {
		return err
	}

	for _, p := range a.providers {
		b, ok := p.(Booter)
		if !ok || !runsIn(p, a.Kind) {
			continue
		}

		if err := b.Boot(ctx, a); err != nil {
			return fmt.Errorf("vukem: boot %T: %w", p, err)
		}
	}

	return a.addBootRoutes()
}

// addBootRoutes runs the WithRoutesAfterBoot functions once.
func (a *App) addBootRoutes() error {
	a.mu.Lock()
	fns := a.bootRoutes
	a.bootRoutes = nil
	a.mu.Unlock()

	if len(fns) == 0 {
		return nil
	}

	if a.Router.Compiled() {
		return ErrRoutesCompiled
	}

	for _, fn := range fns {
		if err := a.runBootRoutes(fn); err != nil {
			return err
		}
	}

	return nil
}

// runBootRoutes runs fn. In warmup a panic (typically a nil client that
// only Boot sets) becomes an error naming the cause, so codegen:routes
// reports it instead of crashing.
func (a *App) runBootRoutes(fn func(r *router.Router, app *App) error) (err error) {
	if a.Kind == KindWarmup {
		defer func() {
			if v := recover(); v != nil {
				err = fmt.Errorf("vukem: routes after boot panicked: %v "+
					"(route registration must not use booted services during warmup; "+
					"check app.Kind == vukem.KindWarmup)", v)
			}
		}()
	}

	if err := fn(a.Router, a); err != nil {
		return fmt.Errorf("vukem: routes after boot: %w", err)
	}

	return nil
}

// pendingBootRoutes reports whether WithRoutesAfterBoot functions have not
// run yet.
func (a *App) pendingBootRoutes() bool {
	a.mu.Lock()
	defer a.mu.Unlock()

	return len(a.bootRoutes) > 0
}

// ErrWarmupRun is returned by Boot (and so Run) on a KindWarmup app: it is
// built only to read its routes and may lack APP_KEY, so it never opens
// connections or serves. Never expose a warmup app's Router or Handler.
var ErrWarmupRun = errors.New("vukem: a warmup app cannot boot or run")

// Run boots the app, starts every runner and blocks until ctx is cancelled
// or a runner fails; then it shuts everything down gracefully within
// SHUTDOWN_TIMEOUT. A cancelled ctx is a clean exit (nil error). Runners
// that have not returned SHUTDOWN_TIMEOUT (plus three seconds) after the
// cancel are logged and left behind: shutdown hooks and providers run
// anyway, so one runner ignoring ctx cannot hold the process forever.
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
		running  = map[string]int{}
	)

	for _, r := range runners {
		running[r.name]++
	}

	for _, r := range runners {
		wg.Go(func() {
			err := r.runner.Run(runCtx)

			errMu.Lock()
			defer errMu.Unlock()

			running[r.name]--
			if running[r.name] == 0 {
				delete(running, r.name)
			}

			if err != nil && !errors.Is(err, context.Canceled) {
				if firstErr == nil {
					firstErr = fmt.Errorf("vukem: runner %s: %w", r.name, err)
				}

				cancel(err)
			}
		})
	}

	<-runCtx.Done()
	a.Log.InfoContext(ctx, "shutting down", "cause", context.Cause(runCtx))

	// Runners observe runCtx and stop on their own; wait for them first.
	cancel(nil)

	if !waitTimeout(&wg, a.Config.ShutdownTimeout+runnerGrace) {
		errMu.Lock()
		stuck := slices.Sorted(maps.Keys(running))
		errMu.Unlock()

		a.Log.ErrorContext(ctx, "runners still running after SHUTDOWN_TIMEOUT: shutting down without them",
			"runners", stuck, "shutdown_timeout", a.Config.ShutdownTimeout)
	}

	shutCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), a.Config.ShutdownTimeout)
	defer stop()

	errMu.Lock()
	runErr := firstErr
	errMu.Unlock()

	return errors.Join(runErr, a.Shutdown(shutCtx))
}

// runnerGrace lets runners that stop at SHUTDOWN_TIMEOUT themselves (the
// HTTP drain, the scheduler, the queue worker) return before Run stops
// waiting for them.
const runnerGrace = 3 * time.Second

// waitTimeout waits for wg up to d and reports whether it finished.
func waitTimeout(wg *sync.WaitGroup, d time.Duration) bool {
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-done:
		return true
	case <-t.C:
		return false
	}
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
		if !ok || !runsIn(p, a.Kind) || a.Kind == KindWarmup {
			continue
		}

		if err := s.Shutdown(ctx, a); err != nil {
			errs = append(errs, fmt.Errorf("vukem: shutdown %T: %w", p, err))
		}
	}

	return errors.Join(errs...)
}

// Provider registers services into the App. Register must not perform I/O;
// use Boot for work that needs other services.
type Provider interface {
	Register(app *App) error
}

// Booter is implemented by providers that need a boot phase. Boot runs after
// every provider registered.
type Booter interface {
	Boot(ctx context.Context, app *App) error
}

// Shutdowner is implemented by providers that release resources. Shutdown
// runs in reverse provider order.
type Shutdowner interface {
	Shutdown(ctx context.Context, app *App) error
}

// KindFilter limits a provider to some runtime kinds. Providers that do not
// implement it run everywhere.
type KindFilter interface {
	Kinds() []Kind
}

func runsIn(p Provider, k Kind) bool {
	f, ok := p.(KindFilter)
	if !ok {
		return true
	}

	return slices.Contains(f.Kinds(), k)
}

// ProviderFunc adapts a function to a Provider with only a Register phase.
type ProviderFunc func(app *App) error

// Register calls f.
func (f ProviderFunc) Register(app *App) error { return f(app) }

// Runner is a long-running component (HTTP server, queue worker, scheduler).
// Run blocks until ctx is done or the runner fails, and must return after
// releasing its resources.
type Runner interface {
	Run(ctx context.Context) error
}

// RunnerFunc adapts a function to a Runner.
type RunnerFunc func(ctx context.Context) error

// Run calls f.
func (f RunnerFunc) Run(ctx context.Context) error { return f(ctx) }

type namedRunner struct {
	name   string
	runner Runner
}

// inertiaPagesDir is where the starters keep Inertia pages.
const inertiaPagesDir = "resources/js/pages"

// inertiaPageMissing reports whether fsys uses the starters' pages
// directory but has no page for component. Other layouts are not checked.
func inertiaPageMissing(fsys fs.FS, component string) bool {
	if _, err := fs.Stat(fsys, inertiaPagesDir); err != nil {
		return false
	}

	for _, ext := range []string{".tsx", ".jsx", ".vue", ".svelte", ".ts", ".js"} {
		if _, err := fs.Stat(fsys, inertiaPagesDir+"/"+component+ext); err == nil {
			return false
		}
	}

	return true
}

// Server timeouts applied by NewHTTPServer. Zero timeouts are never used
// (Slowloris protection, docs/security.md A02).
const (
	DefaultReadHeaderTimeout = 10 * time.Second
	DefaultReadTimeout       = 30 * time.Second
	DefaultWriteTimeout      = 60 * time.Second
	DefaultIdleTimeout       = 120 * time.Second
	// DefaultMaxHeaderBytes is 64 KiB, not Go's 1 MiB
	// (http.DefaultMaxHeaderBytes, which is only the ceiling
	// HTTP_MAX_HEADER_BYTES accepts): it bounds the
	// memory each connection can make the server buffer, and is still far
	// above what browsers send and common proxies forward (nginx 8 KiB per
	// line, Node.js 16 KiB in total). HTTP_MAX_HEADER_BYTES overrides it.
	DefaultMaxHeaderBytes = 64 << 10
)

// minMaxHeaderBytes is the smallest HTTP_MAX_HEADER_BYTES accepted: below
// it ordinary browser requests with a few cookies fail.
const minMaxHeaderBytes = 4 << 10

// HTTPServer runs an *http.Server as a Runner with graceful shutdown.
type HTTPServer struct {
	Server *http.Server
	// ShutdownTimeout bounds the graceful drain of in-flight requests.
	ShutdownTimeout time.Duration
	// Listener, when set, is used instead of listening on Server.Addr.
	Listener net.Listener
	// Ready, when set, receives the bound address once listening.
	Ready chan<- string
}

// NewHTTPServer returns an HTTPServer for handler on addr with secure
// timeouts.
func NewHTTPServer(addr string, handler http.Handler) *HTTPServer {
	return &HTTPServer{
		Server: &http.Server{
			Addr:              addr,
			Handler:           handler,
			ReadHeaderTimeout: DefaultReadHeaderTimeout,
			ReadTimeout:       DefaultReadTimeout,
			WriteTimeout:      DefaultWriteTimeout,
			IdleTimeout:       DefaultIdleTimeout,
			MaxHeaderBytes:    DefaultMaxHeaderBytes,
		},
		ShutdownTimeout: 15 * time.Second,
	}
}

// Run listens and serves until ctx is done, then drains in-flight requests.
func (s *HTTPServer) Run(ctx context.Context) error {
	ln := s.Listener
	if ln == nil {
		var err error

		var lc net.ListenConfig
		if ln, err = lc.Listen(ctx, "tcp", s.Server.Addr); err != nil {
			return err
		}
	}

	// In-flight handlers observe the base context; it is not cancelled on
	// shutdown so requests can finish, but carries the app's values.
	// Server-sent event streams end when shutdown starts, so Shutdown does
	// not wait on connections that never finish by themselves.
	stopping := make(chan struct{})
	s.Server.RegisterOnShutdown(sync.OnceFunc(func() { close(stopping) }))

	base := sse.WithShutdown(context.WithoutCancel(ctx), stopping)
	s.Server.BaseContext = func(net.Listener) context.Context { return base }

	if s.Ready != nil {
		s.Ready <- ln.Addr().String()
	}

	errc := make(chan error, 1)

	go func() { errc <- s.Server.Serve(ln) }()

	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}

		return err
	case <-ctx.Done():
	}

	shutCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.ShutdownTimeout)
	defer cancel()

	if err := s.Server.Shutdown(shutCtx); err != nil {
		return err
	}

	if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	return nil
}

// Instrumentation is a set of neutral hook points that a provider
// (tracing, metrics) attaches to with App.Instrument. Core only reports to
// them and never imports an instrumentation library; drivers/otel turns
// the reports into spans and metrics. Nil fields are skipped.
type Instrumentation struct {
	// Queries is notified of every statement run through DB() and its
	// transactions, after devtools and WithQueryObservers.
	Queries database.QueryObserver
	// Jobs is notified of every job step of Queue(), next to devtools.
	Jobs queue.Observer
	// Mail wraps the transport of Mailer(). Wrappers apply in registration
	// order around the devtools-wrapped transport, so the last one
	// registered is the outermost.
	Mail func(mail.Transport) mail.Transport
}

// ErrInstrumentTooLate is returned by App.Instrument when a hook targets a
// service that was already built: it would never be called.
var ErrInstrumentTooLate = errors.New(
	"vukem: Instrument must run before DB(), Queue() or Mailer() is first used (call it from a provider's Register)",
)

type instruments struct {
	mu      sync.Mutex
	queries []database.QueryObserver
	jobs    []queue.Observer
	mail    []func(mail.Transport) mail.Transport

	dbBuilt, queueBuilt, mailBuilt bool
}

// Instrument attaches in's hooks to the database, queue and mailer. Call it
// from a provider's Register, before those services are first used; a hook
// for a service already built fails with ErrInstrumentTooLate and no hook
// of in is attached. Several calls add up.
func (a *App) Instrument(in Instrumentation) error {
	a.inst.mu.Lock()
	defer a.inst.mu.Unlock()

	if (in.Queries != nil && a.inst.dbBuilt) ||
		(in.Jobs != nil && a.inst.queueBuilt) ||
		(in.Mail != nil && a.inst.mailBuilt) {
		return ErrInstrumentTooLate
	}

	if in.Queries != nil {
		a.inst.queries = append(a.inst.queries, in.Queries)
	}

	if in.Jobs != nil {
		a.inst.jobs = append(a.inst.jobs, in.Jobs)
	}

	if in.Mail != nil {
		a.inst.mail = append(a.inst.mail, in.Mail)
	}

	return nil
}

// queryObservers freezes the query hooks and returns every observer of
// DB(): devtools, then WithQueryObservers, then Instrument's. DB() passes
// each to database.WithQueryObserver, which skips typed nils and contains
// a panicking observer without starving the others.
func (a *App) queryObservers() []database.QueryObserver {
	a.inst.mu.Lock()
	defer a.inst.mu.Unlock()

	a.inst.dbBuilt = true

	var obs []database.QueryObserver
	if a.Devtools != nil {
		obs = append(obs, a.Devtools)
	}

	obs = append(obs, a.queryObs...)

	return append(obs, a.inst.queries...)
}

// jobObservers freezes the job hooks and returns every observer of
// Queue(): devtools, then Instrument's. Queue() adds each with
// queue.AddObserver, which skips typed nils and contains a panicking
// observer without starving the others.
func (a *App) jobObservers() []queue.Observer {
	a.inst.mu.Lock()
	defer a.inst.mu.Unlock()

	a.inst.queueBuilt = true

	var obs []queue.Observer
	if a.Devtools != nil {
		obs = append(obs, a.Devtools)
	}

	return append(obs, a.inst.jobs...)
}

// wrapMail freezes the mail hooks and applies them to t.
func (a *App) wrapMail(t mail.Transport) mail.Transport {
	a.inst.mu.Lock()
	defer a.inst.mu.Unlock()

	a.inst.mailBuilt = true

	for _, wrap := range a.inst.mail {
		t = wrap(t)
	}

	return t
}
