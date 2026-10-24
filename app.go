package vukem

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"
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
	"github.com/AlexGalhardo/vukem/openapi"
	"github.com/AlexGalhardo/vukem/queue"
	"github.com/AlexGalhardo/vukem/ratelimit"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/schedule"
	"github.com/AlexGalhardo/vukem/schema"
	"github.com/AlexGalhardo/vukem/session"
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

	// Telegram is the Telegram log handler; use it for Contact and Send.
	// It is never nil: without TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID it
	// is disabled (Configured reports false, Send drops, SendSync returns
	// telegram.ErrDisabled).
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
	// rateLimiters replaces the RATE_LIMIT_STORE factory (WithRateLimiters).
	rateLimiters ratelimit.Factory
	// openAPIInfo is the document base App.OpenAPI builds from
	// (WithOpenAPI); nil falls back to the configuration alone.
	openAPIInfo  *openapi.Info
	schedules    []func(*schedule.Schedule, *App)
	scheduleOnce sync.Once
	schedule     *schedule.Schedule
	scheduleErr  error

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

// readConfigFile reads the file an "@file:" configuration value names
// (Docker/Kubernetes secrets; symlinks are followed, a relative path is
// resolved against the working directory). Only regular files are read:
// the path is checked with os.Stat before it is opened, because opening a
// FIFO or a terminal (/dev/stdin, a named pipe) blocks; the opened file
// must be the one checked (os.SameFile), and at most one byte past
// config's 1 MiB cap is read, so /dev/zero cannot exhaust memory.
func readConfigFile(name string) ([]byte, error) {
	info, err := os.Stat(name)
	if err != nil {
		return nil, err
	}

	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", name)
	}

	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	opened, err := f.Stat()
	if err != nil {
		return nil, err
	}

	if !os.SameFile(info, opened) {
		return nil, fmt.Errorf("%s changed while it was opened", name)
	}

	return io.ReadAll(io.LimitReader(f, 1<<20+1))
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
	healthChecks  []namedHealthCheck
	routes        []func(r *router.Router, app *App) error
	bootRoutes    []func(r *router.Router, app *App) error
	views         fs.FS
	viewFuncs     template.FuncMap
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
	rateLimiters  ratelimit.Factory
	openAPI       *openapi.Info
	eventsFake    *events.Fake
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
			readFile = readConfigFile
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

	key, proxies, previous, err := checkBootConfig(&cfg, src, &o)
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
		rateLimiters:    o.rateLimiters,
		openAPIInfo:     o.openAPI,
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

	// Before the providers run, so a provider's AddHealthCheck with the
	// same name replaces a build-time one rather than the other way round.
	for _, hc := range o.healthChecks {
		app.AddHealthCheck(hc.name, hc.check)
	}

	if dt != nil {
		app.enableDevtools(dt)
	}

	app.Telegram = tg
	app.telegramLogs = tgLogs

	if tg.Configured() {
		app.OnShutdown(tg.Close)
	}

	if dedup != nil {
		// Registered after Telegram so it runs first: the pending summaries
		// still reach the chat.
		app.OnShutdown(dedup.Close)
	}

	app.Events = events.New(app.Log, 0)
	app.Events.Register(o.listeners...)

	if o.eventsFake != nil {
		app.Events.RecordTo(o.eventsFake)
	}

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

	if o.views == nil && len(o.viewFuncs) > 0 {
		return nil, errors.New("vukem: WithViewFuncs needs WithViews")
	}

	if o.views != nil {
		engine, err := view.New(view.Config{
			FS: o.views, Reload: !cfg.IsProduction(), URLs: app.Router, Vite: app.Vite, I18n: app.I18n,
			Funcs: o.viewFuncs,
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
