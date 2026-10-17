package vukem

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/AlexGalhardo/vukem/cache"
	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/events"
	"github.com/AlexGalhardo/vukem/flags"
	"github.com/AlexGalhardo/vukem/logging/telegram"
	"github.com/AlexGalhardo/vukem/mail"
	"github.com/AlexGalhardo/vukem/session"
	"github.com/AlexGalhardo/vukem/web"
)

// WithListeners registers event listeners on App.Events (generated apps
// pass listeners.All(); vukem make:listener appends there).
func WithListeners(regs ...events.Registration) Option {
	return func(o *options) error { o.listeners = append(o.listeners, regs...); return nil }
}

// WithCacheStore replaces the store chosen by CACHE_STORE (e.g. Redis:
// vukem.WithCacheStore(redisClient.CacheStore("cache:"))). The last of
// WithCacheStore and WithCacheStoreFunc wins.
func WithCacheStore(s cache.Store) Option {
	return func(o *options) error { o.cacheStore, o.cacheFunc = s, nil; return nil }
}

// ErrUseDefault is returned by a WithCacheStoreFunc, WithSessionStoreFunc
// or WithQueueDriverFunc function (directly or wrapped) to use the store
// CACHE_STORE, SESSION_DRIVER or QUEUE_CONNECTION configures instead, e.g.
// when REDIS_URL is not set. The function must return it with a nil store.
// Any other error, and a nil store with a nil error, fail instead: a
// service that is configured but broken never falls back silently.
var ErrUseDefault = errors.New("vukem: use the configured default store")

// WithCacheStoreFunc replaces the store chosen by CACHE_STORE with one
// built by fn on the first app.Cache() call, after configuration loaded,
// so fn can read app.Source (e.g. REDIS_URL from .env or vukemtest). fn
// returns ErrUseDefault to keep the CACHE_STORE store. Any other error
// from fn, or a nil store, is returned by app.Cache(). fn may
// register cleanup with app.OnShutdown, but must not call app.Cache() (or
// anything that does, such as app.RateLimiters()): fn runs inside Cache's
// sync.Once and that call would deadlock.
func WithCacheStoreFunc(fn func(app *App) (cache.Store, error)) Option {
	return func(o *options) error { o.cacheFunc, o.cacheStore = fn, nil; return nil }
}

// WithSessionStore replaces the store chosen by SESSION_DRIVER (e.g.
// Redis: vukem.WithSessionStore(redisClient.SessionStore(""))). The last
// of WithSessionStore and WithSessionStoreFunc wins.
func WithSessionStore(s session.Store) Option {
	return func(o *options) error { o.sessionStore, o.sessionFunc = s, nil; return nil }
}

// WithSessionStoreFunc replaces the store chosen by SESSION_DRIVER with one
// built by fn on the first app.Sessions() call (DefaultMiddleware makes it
// at boot), after configuration loaded. fn returns ErrUseDefault to keep
// the SESSION_DRIVER store (cookie sessions by default). Any other error
// from fn, or a nil store, is returned by app.Sessions(). SetSessionStore
// still takes precedence.
// fn must not call app.Sessions() (or DefaultMiddleware): it runs inside
// Sessions' sync.Once and that call would deadlock.
func WithSessionStoreFunc(fn func(app *App) (session.Store, error)) Option {
	return func(o *options) error { o.sessionFunc, o.sessionStore = fn, nil; return nil }
}

// CacheConfig is read from CACHE_* variables.
type CacheConfig struct {
	// Store is "memory" (default) or "database" (table CACHE_TABLE).
	Store  string `env:"CACHE_STORE"  default:"memory"`
	Prefix string `env:"CACHE_PREFIX" default:"vukem:"`
	Table  string `env:"CACHE_TABLE"  default:"cache"`
	// MaxEntries bounds the memory store.
	MaxEntries int `env:"CACHE_MAX_ENTRIES" default:"10000"`
}

// Cache returns the application cache, built from CACHE_* on first use.
// The database store creates its table if needed.
func (a *App) Cache() (*cache.Cache, error) {
	a.cacheOnce.Do(func() {
		cfg, err := configLoad[CacheConfig](a)
		if err != nil {
			a.cacheErr = err
			return
		}

		store := a.cacheStore
		if store == nil && a.cacheStoreFunc != nil {
			if store, err = lazyStore(a, a.cacheStoreFunc, "WithCacheStoreFunc", "CACHE_STORE="+cfg.Store); err != nil {
				a.cacheErr = fmt.Errorf("vukem: cache store: %w", err)
				return
			}
		}

		switch {
		case store != nil:
			a.cache = cache.New(store, cfg.Prefix)
		case cfg.Store == "memory":
			a.cache = cache.New(cache.NewMemory(cfg.MaxEntries), cfg.Prefix)
		case cfg.Store == "database":
			db, err := a.DB()
			if err != nil {
				a.cacheErr = err
				return
			}

			store, err := cache.NewDatabase(db, cfg.Table)
			if err != nil {
				a.cacheErr = err
				return
			}

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			if err := store.CreateTable(ctx); err != nil {
				a.cacheErr = fmt.Errorf("vukem: create cache table: %w", err)
				return
			}

			a.cache = cache.New(store, cfg.Prefix)
		default:
			a.cacheErr = fmt.Errorf("vukem: unknown CACHE_STORE %q (memory or database)", cfg.Store)
		}
	})

	return a.cache, a.cacheErr
}

// lazyStore runs a With*Func store function. It returns the zero value
// (no store: build the configured default) when fn returns ErrUseDefault,
// and an error for a nil store or a store returned together with
// ErrUseDefault, which would leave unclear which one the app uses.
func lazyStore[T comparable](a *App, fn func(*App) (T, error), option, def string) (T, error) {
	var zero T

	v, err := fn(a)

	switch {
	case isUseDefault(err) && v != zero:
		return zero, fmt.Errorf("%s returned a store together with ErrUseDefault", option)
	case isUseDefault(err):
		// In production a fallback usually means a missing or misspelled
		// REDIS_URL, so it must stand out.
		level := slog.LevelInfo
		if a.Config.IsProduction() {
			level = slog.LevelWarn
		}

		a.Log.Log(context.Background(), level, "vukem: "+option+" chose the configured default", "store", def)

		return zero, nil
	case err != nil:
		return zero, err
	case v == zero:
		return zero, fmt.Errorf(
			"%s returned a nil store (return vukem.ErrUseDefault for the configured default)",
			option,
		)
	}

	return v, nil
}

// isUseDefault reports whether err is ErrUseDefault or a chain of single
// wraps of it. errors.Is would also match inside errors.Join or a multi-%w
// error, which carries another, real failure that must not be swallowed.
func isUseDefault(err error) bool {
	for err != nil {
		if err == ErrUseDefault { //nolint:errorlint // the chain is walked by hand: errors.Is also matches joined errors
			return true
		}

		if _, multi := err.(interface{ Unwrap() []error }); multi {
			return false
		}

		err = errors.Unwrap(err)
	}

	return false
}

// WithFlags registers the application's feature flags (app.Flags). Invalid
// or duplicate names, bad FLAG_<NAME> values, FLAG_<NAME> for a locked flag
// and FLAG_ keys in .env files that name no flag stop New.
func WithFlags(list ...flags.AnyFlag) Option {
	return func(o *options) error { o.flags = append(o.flags, list...); return nil }
}

func checkFlags(list []flags.AnyFlag, src *config.Source) error {
	if len(list) == 0 {
		return nil
	}

	_, err := flags.New(flags.Config{Flags: list, Env: src})

	return err
}

// Flags returns the feature flags registered with WithFlags, built on first
// use: stored values live in the feature_flags table of DB() (vukem
// install:flags), cached in Cache() for flags.DefaultTTL. Without
// registered flags it touches neither the database nor the cache.
func (a *App) Flags() (*flags.Flags, error) {
	a.flagsOnce.Do(func() {
		cfg := flags.Config{Flags: a.flagDefs, Env: a.Source, Log: a.Log}

		if len(a.flagDefs) > 0 {
			db, err := a.DB()
			if err != nil {
				a.flagsErr = err
				return
			}

			c, err := a.Cache()
			if err != nil {
				a.flagsErr = err
				return
			}

			cfg.Store, cfg.Cache = flags.NewDatabaseStore(db, a.now), c
		}

		a.ff, a.flagsErr = flags.New(cfg)
	})

	return a.ff, a.flagsErr
}

// MailConfig is read from MAIL_* (and SMTP_* / RESEND_* / MAILTRAP_*) variables.
type MailConfig struct {
	// Mailer is "smtp", "resend", "mailtrap", "log" or "array" (tests). Production
	// must choose explicitly; elsewhere it defaults to "log".
	Mailer string `env:"MAIL_MAILER"`
	From   string `env:"MAIL_FROM"   default:"Vukem <hello@example.com>"`

	SMTPHost     string        `env:"SMTP_HOST"     default:"localhost"`
	SMTPPort     int           `env:"SMTP_PORT"     default:"587"`
	SMTPUsername string        `env:"SMTP_USERNAME"`
	SMTPPassword config.Secret `env:"SMTP_PASSWORD"`
	// SMTPInsecure allows servers without STARTTLS (local catchers only).
	SMTPInsecure bool `env:"SMTP_INSECURE"`

	ResendAPIKey config.Secret `env:"RESEND_API_KEY"`

	MailtrapAPIToken config.Secret `env:"MAILTRAP_API_TOKEN"`
	// MailtrapInboxID > 0 sends to that Email Sandbox inbox instead of delivering.
	MailtrapInboxID int64 `env:"MAILTRAP_INBOX_ID"`
}

type mailState struct {
	once      sync.Once
	mailer    *mail.Mailer
	err       error
	transport mail.Transport
}

// WithMailTransport replaces the transport chosen by MAIL_MAILER.
func WithMailTransport(t mail.Transport) Option {
	return func(o *options) error { o.mailTransport = t; return nil }
}

// Mailer returns the app mailer, built from MAIL_* on first use. Views
// come from the app's view engine (WithViews).
func (a *App) Mailer() (*mail.Mailer, error) {
	a.mail.once.Do(func() {
		cfg, err := configLoad[MailConfig](a)
		if err != nil {
			a.mail.err = err
			return
		}

		t := a.mail.transport
		if t == nil {
			if t, err = a.mailTransport(cfg); err != nil {
				a.mail.err = err
				return
			}
		}

		if a.Devtools != nil {
			t = a.Devtools.MailTransport(t)
		}

		t = a.wrapMail(t)

		var views web.Renderer
		if a.Views != nil {
			views = a.Views
		}

		a.mail.mailer = mail.New(t, cfg.From, views)
		a.mail.mailer.SetEnqueue(a.enqueueMail)
		a.mail.mailer.SetEnqueueTx(a.enqueueMailTx)
	})

	return a.mail.mailer, a.mail.err
}

func (a *App) mailTransport(cfg MailConfig) (mail.Transport, error) {
	switch cfg.Mailer {
	case "":
		if a.Config.IsProduction() {
			return nil, errors.New("vukem: set MAIL_MAILER in production (smtp, resend, mailtrap or log)")
		}

		return mail.Log{L: a.Log}, nil
	case "log":
		return mail.Log{L: a.Log}, nil
	case "array":
		return mail.NewArray(), nil
	case "smtp":
		return mail.NewSMTP(mail.SMTPConfig{
			Host: cfg.SMTPHost, Port: cfg.SMTPPort, Username: cfg.SMTPUsername, Password: cfg.SMTPPassword,
			InsecureSkipTLS: cfg.SMTPInsecure,
		}), nil
	case "resend":
		if cfg.ResendAPIKey.IsZero() {
			return nil, errors.New("vukem: MAIL_MAILER=resend needs RESEND_API_KEY")
		}

		return mail.NewResend(mail.ResendConfig{APIKey: cfg.ResendAPIKey}), nil
	case "mailtrap":
		if cfg.MailtrapAPIToken.IsZero() {
			return nil, errors.New("vukem: MAIL_MAILER=mailtrap needs MAILTRAP_API_TOKEN")
		}

		if cfg.MailtrapInboxID < 0 {
			return nil, errors.New("vukem: MAILTRAP_INBOX_ID must be a positive sandbox inbox id")
		}

		// Staging also runs APP_ENV=production, so a sandbox inbox is allowed
		// but never silent.
		if cfg.MailtrapInboxID > 0 && a.Config.IsProduction() {
			a.Log.Warn("mail goes to the Mailtrap sandbox; recipients receive nothing",
				"mailer", "mailtrap", "inbox_id", cfg.MailtrapInboxID)
		}

		return mail.NewMailtrap(mail.MailtrapConfig{APIToken: cfg.MailtrapAPIToken, InboxID: cfg.MailtrapInboxID}), nil
	default:
		return nil, fmt.Errorf("vukem: unknown MAIL_MAILER %q", cfg.Mailer)
	}
}

// TelegramConfig is read from TELEGRAM_* variables.
type TelegramConfig struct {
	BotToken config.Secret `env:"TELEGRAM_BOT_TOKEN"`
	ChatID   string        `env:"TELEGRAM_CHAT_ID"`
	// MinLevel is debug, info, warn or error (default error).
	MinLevel string `env:"TELEGRAM_MIN_LEVEL" default:"error"`
	ThreadID int    `env:"TELEGRAM_THREAD_ID"`
	// Log forwards log records at MinLevel and above to the chat (default
	// true); false keeps app.Telegram for explicit Send and Contact only.
	Log bool `env:"TELEGRAM_LOG" default:"true"`
	// RateLimit caps forwarded log records per minute (0: no cap).
	RateLimit int `env:"TELEGRAM_RATE_LIMIT"`
	// APIURL is the Bot API base URL, for a self-hosted Bot API server or
	// a fake in tests. It must be https outside development and test.
	APIURL string `env:"TELEGRAM_API_URL" default:"https://api.telegram.org"`
}

// WithTelegram adjusts the Telegram handler config after it is read from
// TELEGRAM_* (for example Filter, OnError, ChatIDs or Silent). It has no
// effect unless TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID are set.
func WithTelegram(fn func(cfg *telegram.Config)) Option {
	return func(o *options) error { o.telegram = append(o.telegram, fn); return nil }
}

// telegramAPIURL validates TELEGRAM_API_URL and drops a trailing slash.
// The bot token is sent in the request path, so the URL must be https
// outside development and test; errors never echo the value.
func telegramAPIURL(raw string, env config.Env) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		u.ForceQuery {
		return "", errors.New("vukem: TELEGRAM_API_URL must be an http(s) URL without credentials, query or fragment")
	}

	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && (env == config.Development || env == config.Test):
	default:
		return "", errors.New("vukem: TELEGRAM_API_URL must use https (http only in development and test)")
	}

	return strings.TrimRight(raw, "/"), nil
}

// telegramHandler returns the Telegram handler when the bot token and chat
// are configured, or nil (logging to Telegram is optional), and whether
// log records are forwarded to it.
func telegramHandler(
	src *config.Source,
	cfg *Config,
	configure []func(*telegram.Config),
) (*telegram.Handler, bool, error) {
	tc, err := config.Load[TelegramConfig](src)
	if err != nil {
		return nil, false, err
	}

	if tc.BotToken.IsZero() || tc.ChatID == "" {
		return nil, false, nil
	}

	var level slog.Level
	if err := level.UnmarshalText([]byte(strings.ToUpper(tc.MinLevel))); err != nil {
		return nil, false, fmt.Errorf("vukem: TELEGRAM_MIN_LEVEL: %w", err)
	}

	if tc.RateLimit < 0 {
		return nil, false, fmt.Errorf("vukem: TELEGRAM_RATE_LIMIT must be >= 0, got %d", tc.RateLimit)
	}

	baseURL, err := telegramAPIURL(tc.APIURL, cfg.Env)
	if err != nil {
		return nil, false, err
	}

	tg := telegram.Config{
		BotToken: tc.BotToken, ChatID: tc.ChatID, MinLevel: level, ThreadID: tc.ThreadID,
		AppName: cfg.Name, Env: string(cfg.Env), RateLimit: tc.RateLimit, RatePeriod: time.Minute,
		BaseURL: baseURL,
	}

	for _, fn := range configure {
		fn(&tg)
	}

	return telegram.NewHandler(tg), tc.Log, nil
}
