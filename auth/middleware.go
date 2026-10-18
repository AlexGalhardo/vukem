package auth

import (
	"context"
	"errors"
	"net/http"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/web"
)

// IntendedKey is the session key holding the URL a guest tried to open;
// login handlers redirect there (web.SafeRedirect) after success.
const IntendedKey = "url.intended"

func ctxOf(w http.ResponseWriter, r *http.Request) *web.Ctx {
	return web.NewCtx(w, r, web.ServicesFrom(r.Context()))
}

// Required lets the request through when any guard authenticates it. JSON
// and API clients get 401 (with WWW-Authenticate: Bearer when a token guard
// is involved); browsers are redirected to the route named "login", and the
// intended URL is remembered for GET requests. When every guard is an
// [APIGuard] (token and JWT guards), nobody is redirected: the 401 is always
// problem+json, whatever the Accept header or session cookie says; the
// same holds for any guards under [ProblemJSON], which must be mounted
// before Required (Required mounted first never sees it and redirects).
func Required(guards ...Checker) func(http.Handler) http.Handler {
	allAPIGuards := allAPI(guards)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c := ctxOf(w, r)
			apiOnly := allAPIGuards || isProblemJSON(r.Context())

			for _, g := range guards {
				if authenticate, ok := failClosed(g); ok {
					err := authenticate(c)
					if err == nil {
						next.ServeHTTP(w, c.Request())
						return
					}

					// Fail closed without treating the caller as a guest.
					if !errors.Is(err, ErrUnauthenticated) {
						fail(c, apiOnly, err)
						return
					}

					continue
				}

				if g.Check(c) {
					next.ServeHTTP(w, c.Request())
					return
				}
			}

			if apiOnly || c.WantsJSON() || !hasSession(c) {
				fail(c, apiOnly, ErrUnauthenticated.WithHeader("WWW-Authenticate", `Bearer realm="api"`))
				return
			}

			if r.Method == http.MethodGet && !c.IsHTMX() {
				c.Session().Put(IntendedKey, r.URL.RequestURI())
			}

			target := "/login"
			if u, err := c.URL("login"); err == nil {
				target = u
			}

			_ = c.Redirect(target)
		})
	}
}

// APIGuard is a guard for API clients that authenticate every request with
// a credential they carry (TokenGuard, jwt.Guard). Required and Ability
// answer problem+json and never redirect when their guards are APIGuards.
// A custom guard opts in by implementing APIOnly; for a route group that
// mixes in a session guard or custom middleware, use [ProblemJSON].
type APIGuard interface {
	Checker
	// APIOnly marks the guard; it is never called.
	APIOnly()
}

func allAPI(guards []Checker) bool {
	for _, g := range guards {
		if _, ok := g.(APIGuard); !ok {
			return false
		}
	}

	return len(guards) > 0
}

// fail renders err; for API guards always as problem+json, because API
// routes must not answer HTML when a browser (or a client sending Accept:
// text/html) calls them. Only the error path sees the forced Accept: the
// next handler keeps the client's header.
func fail(c *web.Ctx, apiOnly bool, err error) {
	if !apiOnly || c.WantsJSON() {
		c.HandleError(err)
		return
	}

	jsonCtx(c, web.ServicesFrom(c.Context())).HandleError(err)
}

// jsonCtx returns a copy of c whose request accepts only JSON, served by s.
func jsonCtx(c *web.Ctx, s *web.Services) *web.Ctx {
	r := c.Request().Clone(c.Context())
	r.Header.Set("Accept", "application/json")

	jc := web.NewCtx(c.Writer(), r, s)
	jc.SetRouteName(c.RouteName())

	return jc
}

type problemJSONKey struct{}

func isProblemJSON(ctx context.Context) bool {
	on, _ := ctx.Value(problemJSONKey{}).(bool)
	return on
}

// ProblemJSON is route-group middleware for APIs whose authentication is
// not only [APIGuard]s: a session fallback next to a token guard, or a
// custom authenticator or scope check. Mount it first, before every
// middleware it should affect (g.Use(auth.ProblemJSON, auth.Required(api,
// web))): middleware that runs before it is not affected, so Required
// mounted ahead of it still redirects browsers. Below it, Required,
// Ability and Verified never redirect to the login or verification pages,
// and every 401 and 403 that middleware renders (the auth middleware, or
// custom middleware rejecting through web.Fail or web.ServicesFrom) is an
// RFC 9457 problem+json, whatever the Accept header says, like the
// built-in API guards. That covers any 401 or 403 from middleware below it,
// not only authentication: a CSRF or rate-limit 403 is problem+json too.
// Only the error path sees the forced Accept: other
// statuses and the handlers keep negotiating with the client's header.
// Handler errors go through the router's error handler as usual; an API
// handler that returns a 401 or 403 should answer JSON itself.
func ProblemJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		orig := web.ServicesFrom(r.Context())

		handle := orig.Errors
		if handle == nil {
			handle = web.DefaultErrorHandler
		}

		s := *orig
		s.Errors = func(c *web.Ctx, err error) {
			if st := errs.Status(err); (st == http.StatusUnauthorized || st == http.StatusForbidden) &&
				!c.WantsJSON() {
				c = jsonCtx(c, orig)
			}

			handle(c, err)
		}

		ctx := context.WithValue(web.WithServices(r.Context(), &s), problemJSONKey{}, true)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// errChecker is a guard that reports why authentication failed.
type errChecker interface {
	authenticate(c *web.Ctx) error
}

// FailClosedChecker is a guard that tells a missing or bad credential
// apart from a check that could not run. Authenticate returns nil when the
// request is authenticated, an error wrapping [ErrUnauthenticated] for a
// missing, wrong or expired credential, and any other error (a store or
// key-set outage, ideally an errs.HTTPError with status 503) when it could
// not decide. Required renders that error and stops: it neither tries the
// next guard nor treats the caller as a guest. Guards outside this package
// (jwt.Guard, custom guards) implement it to fail closed like
// SessionGuard and TokenGuard.
type FailClosedChecker interface {
	Checker
	Authenticate(c *web.Ctx) error
}

func failClosed(g Checker) (func(*web.Ctx) error, bool) {
	switch ec := g.(type) {
	case errChecker:
		return ec.authenticate, true
	case FailClosedChecker:
		return ec.Authenticate, true
	}

	return nil, false
}

func hasSession(c *web.Ctx) bool { return c.Session() != nil }

// Optional authenticates the request when a guard can (so handlers,
// policies and templates see the user) and never blocks it.
func Optional(guards ...Checker) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c := ctxOf(w, r)

			for _, g := range guards {
				if g.Check(c) {
					break
				}
			}

			next.ServeHTTP(w, c.Request())
		})
	}
}

// Guest sends authenticated users to the route named "home" (or "/"); use
// it on login and register pages.
func Guest(guards ...Checker) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c := ctxOf(w, r)

			for _, g := range guards {
				if g.Check(c) {
					target := "/"
					if u, err := c.URL("home"); err == nil {
						target = u
					}

					_ = c.Redirect(target)

					return
				}
			}

			next.ServeHTTP(w, c.Request())
		})
	}
}

// Verifiable is a user whose email address can be verified.
type Verifiable interface {
	EmailVerified() bool
}

// Verified requires the user authenticated by guard to have a verified
// email. Signed-in browsers without one are redirected to the route named
// "verification.notice" when the app has it (the install:auth scaffold
// does); JSON clients, routes under [ProblemJSON] and apps without that
// route get 403. Place it after Required.
func Verified(guard Checker) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c := ctxOf(w, r)

			if !guard.Check(c) {
				c.HandleError(errUnverified())
				return
			}

			if v, ok := c.Context().Value(userKey{guard.Name()}).(Verifiable); ok && v.EmailVerified() {
				next.ServeHTTP(w, c.Request())
				return
			}

			if !c.WantsJSON() && !isProblemJSON(r.Context()) && hasSession(c) {
				if u, err := c.URL("verification.notice"); err == nil {
					_ = c.Redirect(u)
					return
				}
			}

			c.HandleError(errUnverified())
		})
	}
}

// Ability requires the request's access token (from guard) to grant every
// ability; 403 otherwise (problem+json when guard is an [APIGuard] or the
// route is under [ProblemJSON]). Place it after Required.
func Ability(guard Checker, abilities ...string) func(http.Handler) http.Handler {
	apiGuard := allAPI([]Checker{guard})

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c := ctxOf(w, r)
			apiOnly := apiGuard || isProblemJSON(r.Context())

			if !guard.Check(c) {
				fail(c, apiOnly, ErrUnauthenticated)
				return
			}

			for _, a := range abilities {
				if !TokenCan(c, guard.Name(), a) {
					web.SecurityEvent(c.Request(), "authz.denied", "ability", a)
					fail(c, apiOnly,
						errs.NewKey(
							http.StatusForbidden,
							"auth.token_ability",
							"This token cannot "+a+".",
							map[string]any{"ability": a},
						),
					)

					return
				}
			}

			next.ServeHTTP(w, c.Request())
		})
	}
}

func errUnverified() error {
	return errs.NewKey(http.StatusForbidden, "auth.unverified", "Your email address is not verified.", nil)
}
