package i18n

import (
	"context"
	"net/http"
	"strings"
)

type pathPrefixKey struct{}

// pathPrefix is what StripPathPrefix found: the stripped "/segment" and its
// locale.
type pathPrefix struct {
	prefix, locale string
}

// StripPathPrefix returns server middleware for locale URL prefixes such as
// /pt-br/games: when the first path segment is a key of prefixes (exact,
// case-sensitive), it removes that segment from the request path (/pt-br
// becomes /), so routes are declared once, and records it for SourcePath,
// PathPrefix and LocalizePath. Install it before routing (router.UseServer);
// vukem.DefaultMiddlewareWith does so when MiddlewareConfig.PathPrefixes is
// set. Other requests pass through unchanged.
func StripPathPrefix(prefixes map[string]string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seg, rest, ok := firstSegment(r.URL.Path)
			locale, known := prefixes[seg]

			if !ok || !known {
				next.ServeHTTP(w, r)
				return
			}

			u := *r.URL
			u.Path = rest

			if u.RawPath != "" {
				// The segment is plain text, so its escaped form is the same.
				u.RawPath = strings.TrimPrefix(u.RawPath, "/"+seg)
				if u.RawPath == "" || u.RawPath == u.Path {
					u.RawPath = ""
				}
			}

			r = r.WithContext(context.WithValue(r.Context(), pathPrefixKey{}, pathPrefix{"/" + seg, locale}))
			r.URL = &u

			next.ServeHTTP(w, r)
		})
	}
}

// firstSegment splits "/seg/rest" into "seg" and "/rest" ("/" when nothing
// follows the segment).
func firstSegment(p string) (seg, rest string, ok bool) {
	if !strings.HasPrefix(p, "/") {
		return "", "", false
	}

	seg, after, found := strings.Cut(p[1:], "/")
	if seg == "" {
		return "", "", false
	}

	if !found {
		return seg, "/", true
	}

	return seg, "/" + after, true
}

// PathPrefix returns the locale prefix StripPathPrefix removed from the
// request carrying ctx ("/pt-br"), or "".
func PathPrefix(ctx context.Context) string {
	p, _ := ctx.Value(pathPrefixKey{}).(pathPrefix)
	return p.prefix
}

// LocalizePath adds the request's locale prefix (see PathPrefix) to an
// absolute path, so links stay in the visitor's locale: "/games" becomes
// "/pt-br/games" and "/" becomes "/pt-br". Without a prefix it returns path.
func LocalizePath(ctx context.Context, path string) string {
	prefix := PathPrefix(ctx)
	if prefix == "" {
		return path
	}

	if path == "/" || path == "" {
		return prefix
	}

	return prefix + path
}

func pathLocale(ctx context.Context) string {
	p, _ := ctx.Value(pathPrefixKey{}).(pathPrefix)
	return p.locale
}
