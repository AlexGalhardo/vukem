package inertia

import (
	"net/http"

	"github.com/AlexGalhardo/vukem/i18n"
)

// I18n shares the request's translations with every page, so Vue, React or
// Svelte components translate with the same catalogs as the server. Put it
// after i18n.Middleware and the adapter's Middleware. It shares:
//
//	locale    the resolved locale ("pt-BR")
//	locales   the locales that have a catalog, for a language switcher
//	messages  Bundle.Messages(locale, prefixes...) as a once prop keyed by
//	          locale, so the client receives the catalog again only when
//	          the locale changes
//
// Pass prefixes (for example "ui.", "nav.") to keep server-only messages
// out of the page. Without a Bundle in the context it shares nothing.
func I18n(prefixes ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

			if b := i18n.BundleFrom(ctx); b != nil {
				locale := i18n.Locale(ctx)
				if locale == "" {
					locale = b.Default()
				}

				Share(ctx, "locale", locale)
				Share(ctx, "locales", b.Locales())
				Share(ctx, "messages", Once(b.Messages(locale, prefixes...)).As("i18n:"+locale))
			}

			next.ServeHTTP(w, r)
		})
	}
}
