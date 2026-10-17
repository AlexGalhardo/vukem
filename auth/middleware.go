package auth

import (
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
// intended URL is remembered for GET requests.
func Required(guards ...Checker) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c := ctxOf(w, r)

			for _, g := range guards {
				if g.Check(c) {
					next.ServeHTTP(w, c.Request())
					return
				}
			}

			if c.WantsJSON() || !hasSession(c) {
				err := ErrUnauthenticated.WithHeader("WWW-Authenticate", `Bearer realm="api"`)
				c.HandleError(err)

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
// does); JSON clients, and apps without that route, get 403. Place it
// after Required.
func Verified(guard Checker) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c := ctxOf(w, r)

			if !guard.Check(c) {
				c.HandleError(errs.New(http.StatusForbidden, "Your email address is not verified."))
				return
			}

			if v, ok := c.Context().Value(userKey{guard.Name()}).(Verifiable); ok && v.EmailVerified() {
				next.ServeHTTP(w, c.Request())
				return
			}

			if !c.WantsJSON() && hasSession(c) {
				if u, err := c.URL("verification.notice"); err == nil {
					_ = c.Redirect(u)
					return
				}
			}

			c.HandleError(errs.New(http.StatusForbidden, "Your email address is not verified."))
		})
	}
}

// Ability requires the request's access token (from guard) to grant every
// ability; 403 otherwise. Place it after Required.
func Ability(guard Checker, abilities ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c := ctxOf(w, r)

			if !guard.Check(c) {
				c.HandleError(ErrUnauthenticated)
				return
			}

			for _, a := range abilities {
				if !TokenCan(c, guard.Name(), a) {
					web.SecurityEvent(c.Request(), "authz.denied", "ability", a)
					c.HandleError(errs.New(http.StatusForbidden, "This token cannot "+a+"."))

					return
				}
			}

			next.ServeHTTP(w, c.Request())
		})
	}
}
