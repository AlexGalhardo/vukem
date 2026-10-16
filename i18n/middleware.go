package i18n

import (
	"context"
	"net/http"

	"github.com/AlexGalhardo/vukem/web"
	"github.com/AlexGalhardo/vukem/z"
)

type (
	bundleKey struct{}
	localeKey struct{}
)

// WithBundle stores b in ctx for T and the view "t" function.
func WithBundle(ctx context.Context, b *Bundle) context.Context {
	return context.WithValue(ctx, bundleKey{}, b)
}

// BundleFrom returns the Bundle stored in ctx, or nil.
func BundleFrom(ctx context.Context) *Bundle {
	b, _ := ctx.Value(bundleKey{}).(*Bundle)
	return b
}

// WithLocale stores the request locale in ctx.
func WithLocale(ctx context.Context, locale string) context.Context {
	return context.WithValue(ctx, localeKey{}, locale)
}

// Locale returns the locale stored in ctx (by Middleware or WithLocale),
// or "".
func Locale(ctx context.Context) string {
	l, _ := ctx.Value(localeKey{}).(string)
	return l
}

// T translates key with the Bundle and locale of ctx (the Bundle's default
// locale when none was resolved); without a Bundle it returns key. See
// Bundle.T for args.
//
//	c.Flash("status", i18n.T(c.Context(), "posts.created"))
func T(ctx context.Context, key string, args ...any) string {
	b := BundleFrom(ctx)
	if b == nil {
		return key
	}

	locale := Locale(ctx)
	if locale == "" {
		locale = b.Default()
	}

	return b.T(locale, key, args...)
}

// MiddlewareConfig configures Middleware. Empty names use the defaults;
// "-" disables a source.
type MiddlewareConfig struct {
	// Query is the query parameter that picks the locale (default "lang").
	Query string
	// Cookie is the cookie that holds the locale (default "lang").
	Cookie string
	// SessionKey is the session value that holds the locale (default
	// "locale"); used when a session middleware runs before this one.
	SessionKey string
}

func (c MiddlewareConfig) withDefaults() MiddlewareConfig {
	c.Query = orDefault(c.Query, "lang")
	c.Cookie = orDefault(c.Cookie, "lang")
	c.SessionKey = orDefault(c.SessionKey, "locale")

	return c
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}

	return v
}

// Middleware resolves the request locale from, in order, the query
// parameter, the cookie, the session, the Accept-Language header (by
// quality, matched with Bundle.Match) and the default locale. Values that
// are not well-formed locales of an available catalog are ignored. It
// stores the Bundle and locale in the request context (T, Locale, the view
// "t" function), localizes validation messages of web.Parse/Bind, sets
// Content-Language and adds "Vary: Accept-Language" when the header was
// consulted.
func Middleware(b *Bundle, cfg MiddlewareConfig) func(http.Handler) http.Handler {
	cfg = cfg.withDefaults()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			locale, usedHeader := resolve(b, cfg, r)

			w.Header().Set("Content-Language", locale)

			if usedHeader {
				w.Header().Add("Vary", "Accept-Language")
			}

			ctx := WithLocale(WithBundle(r.Context(), b), locale)
			ctx = web.WithIssueLocalizer(ctx, func(is z.Issues) z.Issues { return b.LocalizeIssues(locale, is) })

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func resolve(b *Bundle, cfg MiddlewareConfig, r *http.Request) (string, bool) {
	if cfg.Query != "-" {
		if l, ok := b.Match(r.URL.Query().Get(cfg.Query)); ok {
			return l, false
		}
	}

	if cfg.Cookie != "-" {
		if c, err := r.Cookie(cfg.Cookie); err == nil {
			if l, ok := b.Match(c.Value); ok {
				return l, false
			}
		}
	}

	if s := web.SessionFrom(r.Context()); s != nil && cfg.SessionKey != "-" {
		if v, ok := s.Get(cfg.SessionKey).(string); ok {
			if l, ok := b.Match(v); ok {
				return l, false
			}
		}
	}

	for _, tag := range parseAcceptLanguage(r.Header.Get("Accept-Language")) {
		if l, ok := b.Match(tag); ok {
			return l, true
		}
	}

	return b.Default(), true
}
