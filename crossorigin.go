package vukem

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/middleware"
	"github.com/AlexGalhardo/vukem/web"
)

// CrossOriginRoute opens one route (or subtree) to browsers on other
// origins, for a public JSON endpoint such as a newsletter form on a
// marketing site. Unlike CSRF_TRUSTED_ORIGINS, which trusts an origin for
// every route, the trust stops at Pattern: the route is served without
// sessions (no cookie is read or set), CORS answers its preflight, and an
// unsafe request from an origin outside CORS.AllowedOrigins is rejected
// with 403 before it reaches the handler.
type CrossOriginRoute struct {
	// Pattern is a ServeMux pattern, with the same matching as
	// MiddlewareOptions.SessionBypass: "/embed/" covers a subtree and
	// "POST /subscribe/{list}" one method and path.
	Pattern string
	// CORS configures the allowed origins, methods and headers.
	// AllowedOrigins must list exact origins or "*"; AllowCredentials must
	// stay false because the route has no session to share.
	CORS middleware.CORSConfig
}

type crossOriginRoute struct {
	cors    middleware.Middleware
	origins []string
}

func (c *crossOriginRoute) allowed(origin string) bool {
	return slices.Contains(c.origins, "*") || slices.Contains(c.origins, origin)
}

func (*crossOriginRoute) ServeHTTP(http.ResponseWriter, *http.Request) {}

func validCrossOrigin(routes []CrossOriginRoute) error {
	for i := range routes {
		c := &routes[i]

		if c.CORS.AllowCredentials {
			return fmt.Errorf("vukem: cross-origin route %q cannot allow credentials: it has no session", c.Pattern)
		}

		if len(c.CORS.AllowedOrigins) == 0 {
			return fmt.Errorf("vukem: cross-origin route %q needs AllowedOrigins", c.Pattern)
		}

		for _, o := range c.CORS.AllowedOrigins {
			if u, err := url.Parse(o); o != "*" && (err != nil || (u.Scheme != "https" && u.Scheme != "http") ||
				u.Host == "" || u.Path != "" || u.RawQuery != "" || u.User != nil) {
				return fmt.Errorf(
					"vukem: cross-origin route %q: %q is not an origin like https://site.example",
					c.Pattern,
					o,
				)
			}
		}
	}

	return nil
}

// crossOriginMiddleware applies each route's CORS and origin allowlist to
// the requests its pattern matches. It is server middleware so a preflight
// (OPTIONS, which the route's method pattern would answer 405) is handled
// before routing.
func crossOriginMiddleware(routes []CrossOriginRoute) (middleware.Middleware, error) {
	mux := http.NewServeMux()

	for i := range routes {
		c := &routes[i]

		cors, err := middleware.CORS(c.CORS)
		if err != nil {
			return nil, fmt.Errorf("vukem: cross-origin route %q: %w", c.Pattern, err)
		}

		if err := handlePattern(
			mux,
			c.Pattern,
			&crossOriginRoute{cors: cors, origins: c.CORS.AllowedOrigins},
		); err != nil {
			return nil, fmt.Errorf("vukem: cross-origin route: %w", err)
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			probe := r
			if m := r.Header.Get("Access-Control-Request-Method"); r.Method == http.MethodOptions && m != "" {
				probe = r.Clone(r.Context())
				probe.Method = m
			}

			h, _ := mux.Handler(probe)

			route, ok := h.(*crossOriginRoute)
			if !ok {
				next.ServeHTTP(w, r)
				return
			}

			route.cors(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !crossOriginAllowed(r, route) {
					web.SecurityEvent(r, "csrf.rejected", "reason", "origin not allowed for route",
						"origin", r.Header.Get("Origin"), "sec_fetch_site", r.Header.Get("Sec-Fetch-Site"))
					web.Fail(w, r, errs.New(http.StatusForbidden, "Cross-site request blocked."))

					return
				}

				next.ServeHTTP(w, r)
			})).ServeHTTP(w, r)
		})
	}, nil
}

// crossOriginAllowed is the CSRF decision for a route that skips the global
// check: safe methods pass (CORS limits who may read the answer); an
// unsafe request needs a same-origin, a non-browser (no Fetch Metadata and
// no Origin) or an allowed-origin sender. Browsers always send Origin on a
// cross-origin unsafe request, so Sec-Fetch-Site without it fails closed.
func crossOriginAllowed(r *http.Request, route *crossOriginRoute) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}

	site, origin := r.Header.Get("Sec-Fetch-Site"), r.Header.Get("Origin")
	if site == "same-origin" || site == "none" {
		return true
	}

	if origin == "" {
		return site == ""
	}

	if route.allowed(origin) {
		return true
	}

	u, err := url.Parse(origin)

	return err == nil && site == "" && u.Host == r.Host
}
