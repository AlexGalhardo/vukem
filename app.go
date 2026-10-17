package vukem

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
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
	// only read the route table (codegen:routes, codegen:client) run in it.
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
	// that still decrypt app.Crypt tokens after a key rotation.
	PreviousKeys    config.Secret `env:"APP_PREVIOUS_KEYS"`
	Host            string        `env:"HOST"              default:"localhost"`
	Port            int           `env:"PORT"              default:"3000"`
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT"  default:"15s"`
	// H2C serves HTTP/2 without TLS (prior knowledge, for gRPC clients) in
	// production too, behind a proxy that speaks h2c. It is always on
	// outside production.
	H2C       bool   `env:"HTTP_H2C"`
	LogLevel  string `env:"LOG_LEVEL"  default:"info"`
	LogFormat string `env:"LOG_FORMAT"`
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

	sessOnce  sync.Once
	sessions  *session.Manager
	sessStore session.Store
	// sessStoreFunc builds sessStore on first use (WithSessionStoreFunc).
	sessStoreFunc func(*App) (session.Store, error)
	// sessPruner is the session store when it keeps expired rows (database).
	sessPruner sessionPruner
	sessErr    error
	mail       mailState
	cacheOnce  sync.Once
	cache      *cache.Cache
	cacheErr   error

	// Telegram is the Telegram log handler when TELEGRAM_* is set (nil
	// otherwise); use it for Contact and Send.
	Telegram *telegram.Handler

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

	now func() time.Time
	// proxies is the parsed TRUSTED_PROXIES.
	proxies []*net.IPNet

	dbOnce     sync.Once
	db         *database.DB
	dbErr      error
	drivers    []database.Driver
	migrations []schema.Migration
	seeders    []Seeder

	mu       sync.Mutex
	booted   bool
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
	views         fs.FS
	vite          fs.FS
	inertia       *inertia.Config
	lang          fs.FS
	now           func() time.Time
	migrations    []schema.Migration
	seeders       []Seeder
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
	stdout        io.Writer
	stderr        io.Writer
	stdin         io.Reader
	ready         chan<- string
	noHTTP        bool
	devtools      bool
}

// WithRoutes registers routes (and router middleware) after providers
// registered, so handlers can use the services they added. An error stops
// New.
func WithRoutes(fn func(r *router.Router, app *App) error) Option {
	return func(o *options) error { o.routes = append(o.routes, fn); return nil }
}

// WithViews renders html/template views from fsys (os.DirFS("resources/views")
// or an embed.FS). Views reload on every request outside production.
func WithViews(fsys fs.FS) Option {
	return func(o *options) error { o.views = fsys; return nil }
}

// WithInertia enables the Inertia adapter (app.Inertia); add
// app.Inertia.Middleware to the routes that render pages. Version and the
// SSR dev endpoint default to app.Vite when WithVite is given.
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
// APP_LOCALE (default "en") as the fallback locale. Views get the "t" and
// "locale" functions, and DefaultMiddleware installs i18n.Middleware.
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

	key, err := checkConfig(&cfg, src.EnvSet())
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

	enc, err := newCrypt(key, cfg.PreviousKeys)
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

	tg, err := telegramHandler(src, &cfg)
	if err != nil {
		return nil, err
	}

	if tg != nil {
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

	if qcfg.Connection == "memory" && app.Kind == KindWeb {
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

	return app, nil
}

// checkConfig enforces secure defaults at boot (fail closed). envSet
// reports whether APP_ENV was given explicitly (config.Source.EnvSet).
func checkConfig(cfg *Config, envSet bool) (config.Key, error) {
	var errs []error

	// A missing key is allowed only with an explicit APP_ENV=development or
	// test (a forgotten APP_ENV in production must not boot keyless); every
	// key-derived feature then fails closed (Key.Derive panics, Crypt is nil,
	// cookie sessions refuse to start). A malformed key is always an error.
	key, err := config.ParseKey(cfg.Key)
	if err != nil && (!errors.Is(err, config.ErrKeyMissing) || !envSet || cfg.IsProduction()) {
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

// newCrypt builds app.Crypt: the "crypt" key derived from APP_KEY encrypts,
// keys derived from APP_PREVIOUS_KEYS only decrypt. A malformed previous key
// is a boot error. Without APP_KEY it returns nil (see crypt.ErrNoKey).
func newCrypt(key config.Key, previous config.Secret) (*crypt.Encrypter, error) {
	var old [][]byte

	for i, s := range strings.Split(previous.Reveal(), ",") {
		if s = strings.TrimSpace(s); s == "" {
			continue
		}

		k, err := config.ParseKey(config.NewSecret(s))
		if err != nil {
			return nil, fmt.Errorf("vukem: APP_PREVIOUS_KEYS entry %d: %w", i+1, err)
		}

		old = append(old, k.Derive("crypt", crypt.KeySize))
	}

	if key.IsZero() {
		return nil, nil
	}

	return crypt.New(key.Derive("crypt", crypt.KeySize), old...)
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
			return fmt.Errorf("vukem: boot %T: %w", p, err)
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
					firstErr = fmt.Errorf("vukem: runner %s: %w", r.name, err)
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
		if !ok || !runsIn(p, a.Kind) || a.Kind == KindWarmup {
			continue
		}

		if err := s.Shutdown(ctx, a); err != nil {
			errs = append(errs, fmt.Errorf("vukem: shutdown %T: %w", p, err))
		}
	}

	return errors.Join(errs...)
}
