package i18n

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/AlexGalhardo/vukem/errs"
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
	// Order lists the sources consulted, first match wins. Nil means
	// query, cookie, session, header; a source left out is off, so
	// []Source{SourceQuery} never reads cookies, sessions or
	// Accept-Language.
	Order []Source
	// Locales restricts the locales a request can select to this subset of
	// the Bundle (empty: every catalog). It must include the default locale,
	// which stays the fallback.
	Locales []string
}

// Source is a place Middleware reads the request locale from.
type Source string

// Locale sources for MiddlewareConfig.Order.
const (
	SourceQuery   Source = "query"
	SourceCookie  Source = "cookie"
	SourceSession Source = "session"
	SourceHeader  Source = "header" // Accept-Language
)

var defaultOrder = []Source{SourceQuery, SourceCookie, SourceSession, SourceHeader}

// Validate reports an Order with no, unknown or repeated sources, and
// Locales that are not available in b or leave out its default locale.
// Middleware returns it as an error for such a config.
func (c MiddlewareConfig) Validate(b *Bundle) error {
	if c.Order != nil && len(c.Order) == 0 {
		return errors.New("i18n: middleware Order is empty (nil uses the default order)")
	}

	seen := map[Source]bool{}
	for _, s := range c.Order {
		if !slices.Contains(defaultOrder, s) {
			return fmt.Errorf("i18n: unknown locale source %q", s)
		}

		if seen[s] {
			return fmt.Errorf("i18n: locale source %q listed twice", s)
		}

		seen[s] = true
	}

	if len(c.Locales) == 0 {
		return nil
	}

	available := b.Locales()
	hasDefault := false

	for _, l := range c.Locales {
		canon, ok := Canonical(l)
		if !ok {
			return fmt.Errorf("i18n: invalid locale %q", l)
		}

		if canon != b.Default() && !slices.Contains(available, canon) {
			return fmt.Errorf("i18n: locale %q has no catalog", l)
		}

		hasDefault = hasDefault || canon == b.Default()
	}

	if !hasDefault {
		return fmt.Errorf("i18n: Locales must include the default locale %q", b.Default())
	}

	return nil
}

func (c MiddlewareConfig) withDefaults() MiddlewareConfig {
	c.Query = cmp.Or(c.Query, "lang")
	c.Cookie = cmp.Or(c.Cookie, "lang")
	c.SessionKey = cmp.Or(c.SessionKey, "locale")

	if c.Order == nil {
		c.Order = defaultOrder
	}

	return c
}

// Middleware resolves the request locale from, in order, the query
// parameter, the cookie, the session, the Accept-Language header (by
// quality, matched with Bundle.Match) and the default locale
// (MiddlewareConfig.Order changes the sources and their order). Values that
// are not well-formed locales of an available catalog are ignored. It
// stores the Bundle and locale in the request context (T, Locale, the view
// "t" function), localizes validation messages of web.Parse/Bind and the
// keyed errors of the framework (errs.WithTranslator, Bundle.Translator), sets
// Content-Language and adds "Vary: Cookie" when the cookie or session
// source was consulted and "Vary: Accept-Language" when the header was.
// (A locale from the query needs no Vary: the URL is the cache key.)
//
// It returns the MiddlewareConfig.Validate error when cfg is invalid.
func Middleware(b *Bundle, cfg MiddlewareConfig) (func(http.Handler) http.Handler, error) {
	if err := cfg.Validate(b); err != nil {
		return nil, err
	}

	cfg = cfg.withDefaults()

	match := b.Match
	if len(cfg.Locales) > 0 {
		allowed := map[string]bool{}
		for _, l := range cfg.Locales {
			canon, _ := Canonical(l) // Validate checked the tags
			allowed[canon] = true
		}

		match = func(tag string) (string, bool) { return b.match(tag, func(l string) bool { return allowed[l] }) }
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			locale, vary := resolve(b, cfg, match, r)

			w.Header().Set("Content-Language", locale)

			for _, h := range vary {
				w.Header().Add("Vary", h)
			}

			ctx := WithLocale(WithBundle(r.Context(), b), locale)
			ctx = web.WithIssueLocalizer(ctx, func(is z.Issues) z.Issues { return b.LocalizeIssues(locale, is) })
			ctx = errs.WithTranslator(ctx, b.Translator(locale))

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}, nil
}

// resolve returns the locale and the request headers it depended on.
func resolve(
	b *Bundle,
	cfg MiddlewareConfig,
	match func(string) (string, bool),
	r *http.Request,
) (string, []string) {
	var vary []string

	varyOn := func(h string) {
		if !slices.Contains(vary, h) {
			vary = append(vary, h)
		}
	}

	for _, src := range cfg.Order {
		var (
			l  string
			ok bool
		)

		switch src {
		case SourceQuery:
			if cfg.Query != "-" {
				l, ok = match(r.URL.Query().Get(cfg.Query))
			}
		case SourceCookie:
			if cfg.Cookie == "-" {
				break
			}

			varyOn("Cookie")

			if c, err := r.Cookie(cfg.Cookie); err == nil {
				l, ok = match(c.Value)
			}
		case SourceSession:
			s := web.SessionFrom(r.Context())
			if s == nil || cfg.SessionKey == "-" {
				break
			}

			varyOn("Cookie") // sessions are keyed by cookie

			if v, isString := s.Get(cfg.SessionKey).(string); isString {
				l, ok = match(v)
			}
		case SourceHeader:
			varyOn("Accept-Language")

			for _, tag := range parseAcceptLanguage(r.Header.Get("Accept-Language")) {
				if l, ok = match(tag); ok {
					break
				}
			}
		}

		if ok {
			return l, vary
		}
	}

	return b.Default(), vary
}

// NewMiddleware is Middleware.
//
// Deprecated: use Middleware, which now returns the error itself.
//
//go:fix inline
func NewMiddleware(bundle *Bundle, config MiddlewareConfig) (func(http.Handler) http.Handler, error) {
	// The parameters are not named b and cfg on purpose: with those names,
	// go fix was reported to rewrite NewMiddleware(b, MiddlewareConfig{})
	// into Middleware(MiddlewareConfig{}, cfg) (v0.26.0, B2; not reproduced
	// on Go 1.27.2). internal/tools/fixinline checks this wrapper.
	return Middleware(bundle, config)
}
