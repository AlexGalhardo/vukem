package auth_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/auth/hash"
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/schema"
	"github.com/AlexGalhardo/vukem/vukemtest"
	"github.com/AlexGalhardo/vukem/web"
)

type User struct {
	orm.Model

	Email          string
	Password       string `json:"-"`
	VerifiedAt     orm.NullTime
	SessionVersion int64 `json:"-"`
}

func (u User) AuthID() string       { return strconv.FormatInt(u.ID, 10) }
func (u User) AuthPassword() string { return u.Password }
func (u User) EmailVerified() bool  { return u.VerifiedAt.Valid }

func (u User) AuthSessionVersion() int64 { return u.SessionVersion }

var migrations = []schema.Migration{{
	ID: "1_auth",
	Up: func(s *schema.Schema) error {
		if err := s.Create("users", func(t *schema.Table) {
			t.ID()
			t.String("email").Unique()
			t.String("password")
			t.Timestamp("verified_at").Nullable()
			t.BigInteger("session_version").Default(0)
			t.Timestamps()
		}); err != nil {
			return err
		}

		if err := s.Create("remember_tokens", func(t *schema.Table) {
			t.String("id").Unique()
			t.String("user_id")
			t.String("hash")
			t.Timestamp("expires_at")
		}); err != nil {
			return err
		}

		return s.Create("access_tokens", func(t *schema.Table) {
			t.String("id").Unique()
			t.String("user_id").Index()
			t.String("name")
			t.String("hash")
			t.Text("abilities")
			t.Timestamp("last_used_at").Nullable()
			t.Timestamp("expires_at").Nullable()
			t.Timestamp("revoked_at").Nullable()
			t.Timestamp("created_at")
		})
	},
	Down: func(*schema.Schema) error { return nil },
}}

// testApp wires both guards like an app's bootstrap would.
type testApp struct {
	*vukemtest.App

	web    *auth.SessionGuard[User]
	api    *auth.TokenGuard[User]
	key    *auth.TokenGuard[User]
	tokens *auth.Tokens
	hasher hash.Hasher
	// down makes the session guard's user lookups fail like a database
	// outage.
	down atomic.Bool
	// gone makes them answer not found the way custom providers often do,
	// with a wrapped sql.ErrNoRows.
	gone atomic.Bool
}

// flakyUsers fails ByID while down or gone is set.
type flakyUsers struct {
	*auth.ORMProvider[User]

	down, gone *atomic.Bool
}

func (f flakyUsers) ByID(ctx context.Context, id string) (User, error) {
	if f.down.Load() {
		return User{}, errors.New("database unavailable")
	}

	if f.gone.Load() {
		return User{}, fmt.Errorf("load user %s: %w", id, sql.ErrNoRows)
	}

	return f.ORMProvider.ByID(ctx, id)
}

func newApp(t *testing.T, hasher hash.Hasher, extra ...vukem.Option) *testApp {
	t.Helper()

	ta := &testApp{hasher: hasher}

	build := func(opts ...vukem.Option) (*vukem.App, error) {
		return vukem.New(append(append(opts, extra...), vukem.WithMigrations(migrations),
			vukem.WithRoutes(func(r *router.Router, app *vukem.App) error {
				if err := vukem.DefaultMiddleware(app, r); err != nil {
					return err
				}

				db, err := app.DB()
				if err != nil {
					return err
				}

				users := auth.NewORMProvider[User](db)
				ta.tokens = auth.NewTokens(db)
				ta.web = auth.NewSessionGuard(
					"web",
					flakyUsers{users, &ta.down, &ta.gone},
					hasher,
					auth.NewRememberStore(db),
				)
				ta.web.InsecureCookies = true
				ta.api = auth.NewTokenGuard("api", users, ta.tokens)
				// A second token guard wired the way an app that wants the
				// X-API-Key convention does.
				ta.key = auth.NewTokenGuard("key", users, ta.tokens).WithTokenHeaders("X-API-Key")

				r.Get("/", func(c *web.Ctx) error { return c.String(200, "home") }).Name("home")
				r.Get("/login", func(c *web.Ctx) error { return c.String(200, "login form") }).Name("login").
					Use(auth.Guest(ta.web))
				r.Post("/login", func(c *web.Ctx) error {
					in, err := c.Input()
					if err != nil {
						return err
					}

					creds := map[string]string{"email": str(in["email"]), "password": str(in["password"])}
					if _, err := ta.web.Attempt(c, creds, auth.Remember(str(in["remember"]) == "on")); err != nil {
						return err
					}

					return c.Redirect("/dashboard")
				})
				r.Post("/logout", func(c *web.Ctx) error { _ = ta.web.Logout(c); return c.Redirect("/") })
				// /sessions/end-others ends the user's other sessions the way a
				// 2FA scaffold does: bump the version, then keep this one.
				r.Post("/sessions/end-others", func(c *web.Ctx) error {
					u, err := auth.User[User](c, "web")
					if err != nil {
						return err
					}

					if _, err := orm.QueryOf[User](db).Where("id", u.ID).
						Update(c.Context(), orm.Set{"session_version": u.SessionVersion + 1}); err != nil {
						return err
					}

					if err := ta.web.RefreshSession(c); err != nil {
						return err
					}

					return c.Redirect("/dashboard")
				}).Use(auth.Required(ta.web))
				r.Get("/dashboard", func(c *web.Ctx) error {
					u, err := auth.User[User](c, "web")
					if err != nil {
						return err
					}

					return c.String(200, "hello "+u.Email)
				}).Use(auth.Required(ta.web))
				r.Get("/whoami", func(c *web.Ctx) error {
					if u, err := auth.User[User](c, "web"); err == nil {
						return c.String(200, u.Email)
					}

					return c.String(200, "guest")
				}).Use(auth.Optional(ta.web))
				r.Get("/verified", func(c *web.Ctx) error { return c.String(200, "ok") }).
					Use(auth.Required(ta.web), auth.Verified(ta.web))
				r.Get("/email/verify", func(c *web.Ctx) error { return c.String(200, "check your inbox") }).
					Name("verification.notice")

				r.Group("/v1", func(g *router.Group) {
					g.Use(auth.Required(ta.api))
					g.Get("/me", func(c *web.Ctx) error { return c.NoContent() })
					g.Get("/accept", func(c *web.Ctx) error { return c.String(200, c.Header("Accept")) }).
						Use(auth.Ability(ta.api, "posts:read"))
					g.Delete("/posts", func(c *web.Ctx) error { return c.NoContent() }).
						Use(auth.Ability(ta.api, "posts:delete"))
				})

				// /papi mixes a session fallback with tokens and custom
				// middleware; auth.ProblemJSON makes every 401/403 a problem.
				r.Group("/papi", func(g *router.Group) {
					g.Use(auth.ProblemJSON, auth.Required(ta.api, ta.web))
					g.Get("/me", func(c *web.Ctx) error { return c.String(200, c.Header("Accept")) })
					g.Get("/verified", func(c *web.Ctx) error { return c.NoContent() }).
						Use(auth.Verified(ta.web))
					g.Delete("/posts", func(c *web.Ctx) error { return c.NoContent() }).
						Use(auth.Ability(ta.web, "posts:delete"))
					g.Get("/scope", func(c *web.Ctx) error { return c.NoContent() }).
						Use(func(http.Handler) http.Handler {
							return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
								web.Fail(w, r, errs.New(http.StatusForbidden, "This key lacks the admin scope."))
							})
						})
					g.Get("/conflict", func(c *web.Ctx) error { return c.NoContent() }).
						Use(func(http.Handler) http.Handler {
							return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
								web.Fail(w, r, errs.New(http.StatusConflict, "Busy."))
							})
						})
				})

				// Mounted after Required, ProblemJSON comes too late.
				r.Get("/papi-late", func(c *web.Ctx) error { return c.NoContent() }).
					Use(auth.Required(ta.api, ta.web), auth.ProblemJSON)

				// /keyapi takes the token from X-API-Key as well.
				r.Group("/keyapi", func(g *router.Group) {
					g.Use(auth.Required(ta.key))
					g.Get("/me", func(c *web.Ctx) error {
						u, err := auth.User[User](c, "key")
						if err != nil {
							return err
						}

						return c.String(200, u.Email)
					})
				})

				r.Group("/api", func(g *router.Group) {
					g.Use(auth.Required(ta.api, ta.web))
					g.Get("/me", func(c *web.Ctx) error {
						u, err := auth.User[User](c, "api")
						if err != nil {
							u, err = auth.User[User](c, "web")
						}

						if err != nil {
							return err
						}

						return c.JSON(200, map[string]any{"email": u.Email})
					})
					g.Delete("/posts", func(c *web.Ctx) error { return c.NoContent() }).
						Use(auth.Ability(ta.api, "posts:delete"))
				})

				return nil
			}))...)
	}

	ta.App = vukemtest.New(t, build)

	return ta
}

func str(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case []any:
		if len(s) > 0 {
			s0, _ := s[0].(string)
			return s0
		}
	}

	return ""
}

// mustDB is DB without the error check, for tests that cannot run without it.
func (ta *testApp) mustDB(t *testing.T) *database.DB {
	t.Helper()

	db, err := ta.DB()
	if err != nil {
		t.Fatal(err)
	}

	return db
}

func (ta *testApp) user(t *testing.T, email, password string, verified bool) User {
	t.Helper()

	h, err := ta.hasher.Make(password)
	if err != nil {
		t.Fatal(err)
	}

	u := User{Email: email, Password: h}
	if verified {
		u.VerifiedAt.Valid, u.VerifiedAt.V = true, time.Now()
	}

	db, err := ta.DB()
	if err != nil {
		t.Fatal(err)
	}

	if err := orm.Create(t.Context(), db, &u); err != nil {
		t.Fatal(err)
	}

	return u
}

func TestSessionLoginLogout(t *testing.T) {
	t.Parallel()

	ta := newApp(t, hash.Fake())
	ta.user(t, "ana@example.com", "s3cret-pass", false)

	c := ta.Client(t)

	// Guests are sent to login and the intended URL is remembered.
	c.Get("/dashboard").AssertRedirect("/login").AssertSessionHas(auth.IntendedKey)

	c.WithHeader("Referer", "/login").
		PostForm("/login", url.Values{"email": {"ana@example.com"}, "password": {"wrong"}}).
		AssertRedirect("/login").
		AssertSessionHasErrors("email")

	before := sessionCookie(c)

	c.PostForm("/login", url.Values{"email": {"ana@example.com"}, "password": {"s3cret-pass"}}).
		AssertRedirect("/dashboard").
		AssertSessionHas(web.SessionUserKey) // server-side stores index the session by user

	if after := sessionCookie(c); after == before {
		t.Fatal("session cookie unchanged on login")
	}

	c.Get("/dashboard").AssertOK().AssertSee("hello ana@example.com")
	c.Get("/login").AssertRedirect("/")                // Guest
	c.Get("/verified").AssertRedirect("/email/verify") // unverified browsers go to the notice

	c.PostForm("/logout", nil).AssertRedirect("/").AssertSessionMissing(web.SessionUserKey)
	c.Get("/dashboard").AssertRedirect("/login")

	// JSON clients get 401 instead of a redirect.
	ta.Client(t).GetJSON("/dashboard").AssertUnauthorized().AssertHeader("WWW-Authenticate", `Bearer realm="api"`)
}

// Sessions logged in before the user key existed get it on their next
// authenticated request, so RevokeOthers reaches them.
func TestSessionUserKeyBackfilled(t *testing.T) {
	t.Parallel()

	ta := newApp(t, hash.Fake())
	u := ta.user(t, "bea@example.com", "s3cret-pass", false)

	tests := []struct {
		name string
		data map[string]any
	}{
		{"missing", map[string]any{"auth_web": u.AuthID()}},
		{"other guard logged out", map[string]any{"auth_web": u.AuthID(), web.SessionUserKey: "admin:99"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m, err := ta.Sessions()
			if err != nil {
				t.Fatal(err)
			}

			c := withSession(ta.Client(t), m.Cookies(t.Context(), tt.data))

			got := c.Get("/whoami").AssertOK().AssertSee("bea@example.com").Session()[web.SessionUserKey]
			if want := "web:" + u.AuthID(); got != want {
				t.Fatalf("session user = %v, want %q", got, want)
			}
		})
	}

	// While another guard is still logged in, its owner is kept.
	m, err := ta.Sessions()
	if err != nil {
		t.Fatal(err)
	}

	c := withSession(ta.Client(t), m.Cookies(t.Context(), map[string]any{
		"auth_web": u.AuthID(), "auth_admin": "99", web.SessionUserKey: "admin:99",
	}))

	if got := c.Get("/whoami").AssertOK().Session()[web.SessionUserKey]; got != "admin:99" {
		t.Fatalf("session user with another guard logged in = %v, want admin:99", got)
	}
}

func withSession(c *vukemtest.Client, cookies []*http.Cookie) *vukemtest.Client {
	for _, ck := range cookies {
		c.WithCookie(ck.Name, ck.Value)
	}

	return c
}

func sessionCookie(c *vukemtest.Client) string { return c.CookieValue("vukem_session") }

func TestSecurity_A07_LoginErrorsDoNotRevealUsers(t *testing.T) {
	t.Parallel()

	ta := newApp(t, hash.Fake())
	ta.user(t, "ana@example.com", "s3cret-pass", false)

	// Unknown emails and wrong passwords give the same error.
	for _, email := range []string{"ana@example.com", "nobody@example.com"} {
		ta.Client(t).AcceptJSON().PostJSON("/login", map[string]any{"email": email, "password": "nope"}).
			AssertJSONValidationErrors("email").
			AssertJSONPath("errors.email.0", "These credentials do not match our records.")
	}
}

// countingHasher counts Verify calls of the wrapped hasher.
type countingHasher struct {
	hash.Hasher

	verifies atomic.Int32
}

func (h *countingHasher) Verify(hashed, password string) (bool, error) {
	h.verifies.Add(1)
	return h.Hasher.Verify(hashed, password)
}

// An unknown email costs one password hash comparison too, so response
// time does not reveal which accounts exist.
func TestSecurity_A07_DummyHashOnUnknownUser(t *testing.T) {
	t.Parallel()

	h := &countingHasher{Hasher: hash.Fake()}
	ta := newApp(t, h)
	ta.user(t, "ana@example.com", "s3cret-pass", false)

	for _, email := range []string{"ana@example.com", "nobody@example.com"} {
		before := h.verifies.Load()

		ta.Client(t).AcceptJSON().PostJSON("/login", map[string]any{"email": email, "password": "nope"}).
			AssertJSONValidationErrors("email")

		if n := h.verifies.Load() - before; n != 1 {
			t.Errorf("login as %s ran Verify %d times, want 1", email, n)
		}
	}
}

func TestRememberMe(t *testing.T) {
	t.Parallel()

	ta := newApp(t, hash.Fake())
	ta.user(t, "ana@example.com", "s3cret-pass", false)

	c := ta.Client(t)
	c.PostForm("/login", url.Values{"email": {"ana@example.com"}, "password": {"s3cret-pass"}, "remember": {"on"}})

	token := c.CookieValue("remember_web")
	if token == "" {
		t.Fatal("no remember-me cookie")
	}

	// A new browser session with only the remember cookie is logged in,
	// and the token is rotated.
	fresh := ta.Client(t).WithCookie("remember_web", token)
	fresh.Get("/dashboard").AssertOK()

	if rotated := fresh.CookieValue("remember_web"); rotated == "" || rotated == token {
		t.Fatalf("token not rotated: %q", rotated)
	}

	// The old token is single use.
	ta.Client(t).WithCookie("remember_web", token).Get("/dashboard").AssertRedirect("/login")

	for _, bad := range []string{"garbage", "id.secret", strings.SplitN(token, ".", 2)[0] + ".wrong"} {
		ta.Client(t).WithCookie("remember_web", bad).Get("/dashboard").AssertRedirect("/login")
	}

	// Logging out deletes the token.
	fresh.PostForm("/logout", nil)
	ta.Client(t).
		WithCookie("remember_web", fresh.CookieValue("remember_web")).
		Get("/dashboard").
		AssertRedirect("/login")
}

// A stolen remember-me cookie replayed concurrently logs in at most once.
func TestSecurity_A07_RememberTokenSingleUseUnderConcurrency(t *testing.T) {
	t.Parallel()

	ta := newApp(t, hash.Fake())
	ta.user(t, "eve@example.com", "s3cret-pass", false)

	for range 5 {
		c := ta.Client(t)
		c.PostForm("/login", url.Values{"email": {"eve@example.com"}, "password": {"s3cret-pass"}, "remember": {"on"}})
		token := c.CookieValue("remember_web")

		var (
			wg sync.WaitGroup
			ok atomic.Int32
		)

		for range 8 {
			wg.Go(func() {
				if ta.Client(t).WithCookie("remember_web", token).Get("/dashboard").Status() == http.StatusOK {
					ok.Add(1)
				}
			})
		}

		wg.Wait()

		if n := ok.Load(); n != 1 {
			t.Fatalf("concurrent replays of one remember token logged in %d times, want 1", n)
		}
	}
}

func TestPasswordRehashOnLogin(t *testing.T) {
	t.Parallel()

	ta := newApp(t, hash.Fake())

	old, _ := hash.Bcrypt(4).Make("s3cret-pass")
	u := ta.user(t, "bob@example.com", "irrelevant", false)

	db, _ := ta.DB()
	if _, err := orm.QueryOf[User](db).Where("id", u.ID).Update(t.Context(), orm.Set{"password": old}); err != nil {
		t.Fatal(err)
	}

	ta.Client(t).PostForm("/login", url.Values{"email": {"bob@example.com"}, "password": {"s3cret-pass"}}).
		AssertRedirect("/dashboard")

	got, _ := orm.QueryOf[User](db).Find(t.Context(), u.ID)
	if !strings.HasPrefix(got.Password, "$argon2id$") {
		t.Fatalf("password not rehashed: %q", got.Password)
	}
}

func TestTokensAndAbilities(t *testing.T) {
	t.Parallel()

	ta := newApp(t, hash.Fake())
	u := ta.user(t, "ana@example.com", "pw", true)
	ctx := t.Context()

	plain, tok, err := ta.tokens.Create(ctx, u.AuthID(), "cli", []string{"posts:read"}, time.Hour)
	if err != nil || tok.ID == "" || !strings.HasPrefix(plain, tok.ID+".") {
		t.Fatalf("Create = %q %+v %v", plain, tok, err)
	}

	api := ta.Client(t).AcceptJSON()
	api.WithToken(plain).GetJSON("/api/me").AssertOK().AssertJSONPath("email", "ana@example.com")
	api.WithToken(plain).Delete("/api/posts").AssertForbidden()

	admin, _, _ := ta.tokens.Create(ctx, u.AuthID(), "admin", []string{"*"}, 0)
	ta.Client(t).AcceptJSON().WithToken(admin).Delete("/api/posts").AssertNoContent()

	for _, bad := range []string{"", "nodot", tok.ID + ".wrong", "x." + strings.SplitN(plain, ".", 2)[1]} {
		ta.Client(t).AcceptJSON().WithToken(bad).GetJSON("/api/me").AssertUnauthorized()
	}

	// Expired and revoked tokens fail.
	expired, _, _ := ta.tokens.Create(ctx, u.AuthID(), "old", nil, time.Nanosecond)
	time.Sleep(time.Millisecond) //nolint:forbidigo // wall-clock expiry of a real token row

	ta.Client(t).AcceptJSON().WithToken(expired).GetJSON("/api/me").AssertUnauthorized()

	if err := ta.tokens.Revoke(ctx, tok.ID); err != nil {
		t.Fatal(err)
	}

	ta.Client(t).AcceptJSON().WithToken(plain).GetJSON("/api/me").AssertUnauthorized()

	if err := ta.tokens.RevokeAll(ctx, u.AuthID()); err != nil {
		t.Fatal(err)
	}

	ta.Client(t).AcceptJSON().WithToken(admin).Delete("/api/posts").AssertUnauthorized()

	// The session guard also satisfies the API group.
	ta.Client(t).AcceptJSON().ActingAs("web", u).GetJSON("/api/me").AssertOK()

	// last_used_at is recorded.
	another, _, _ := ta.tokens.Create(ctx, u.AuthID(), "used", nil, 0)
	if _, err := ta.tokens.Find(ctx, another); err != nil {
		t.Fatal(err)
	}

	if found, err := ta.tokens.Find(ctx, another); err != nil || found.LastUsedAt == nil || found.Can("x") {
		t.Fatalf("Find = %+v %v", found, err)
	}
}

// TestTokensListReportsTheOwner locks the owner of every listed token: the
// response of an app that lists a caller's keys renders it, so an empty
// Token.UserID would reach clients.
func TestTokensListReportsTheOwner(t *testing.T) {
	t.Parallel()

	ta := newApp(t, hash.Fake())
	ana := ta.user(t, "ana@example.com", "pw", true)
	bob := ta.user(t, "bob@example.com", "pw", true)
	ctx := t.Context()

	for _, name := range []string{"cli", "ci"} {
		if _, _, err := ta.tokens.Create(ctx, ana.AuthID(), name, nil, time.Hour); err != nil {
			t.Fatal(err)
		}
	}

	if _, _, err := ta.tokens.Create(ctx, bob.AuthID(), "bob-cli", nil, 0); err != nil {
		t.Fatal(err)
	}

	list, err := ta.tokens.List(ctx, ana.AuthID())
	if err != nil || len(list) != 2 {
		t.Fatalf("List(ana) = %+v, %v, want 2 tokens", list, err)
	}

	for _, tok := range list {
		if tok.UserID != ana.AuthID() {
			t.Errorf("List(ana) token %s UserID = %q, want %q", tok.ID, tok.UserID, ana.AuthID())
		}
	}
}

func TestTokenOnlyRoutesAnswerProblemJSON(t *testing.T) {
	t.Parallel()

	ta := newApp(t, hash.Fake())
	u := ta.user(t, "ana@example.com", "pw", true)

	plain, _, err := ta.tokens.Create(t.Context(), u.AuthID(), "cli", []string{"posts:read"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	tests := map[string]*vukemtest.Client{
		"browser with a session":   ta.Client(t).ActingAs("web", u).WithHeader("Accept", "text/html"),
		"browser without a cookie": ta.Client(t).WithHeader("Accept", "text/html,application/xhtml+xml"),
		"no Accept header":         ta.Client(t),
	}

	// The clients report through t, so the cases run without subtests.
	for name, c := range tests {
		res := c.Get("/v1/me")
		if res.Status() != http.StatusUnauthorized || res.Header("Location") != "" ||
			!strings.Contains(res.Header("WWW-Authenticate"), "Bearer") ||
			!strings.HasPrefix(res.Header("Content-Type"), "application/problem+json") {
			t.Errorf("%s: GET /v1/me = %d %v, want 401 problem+json with WWW-Authenticate", name, res.Status(),
				res.Recorder().Header())
		}
	}

	// A token without the ability gets a 403 problem, also from a browser.
	res := ta.Client(t).WithHeader("Accept", "text/html").WithToken(plain).Delete("/v1/posts").AssertForbidden()
	if ct := res.Header("Content-Type"); !strings.HasPrefix(ct, "application/problem+json") {
		t.Errorf("DELETE /v1/posts Content-Type = %q, want application/problem+json", ct)
	}

	// Only errors are forced to JSON: an authenticated handler still sees
	// the client's Accept header and can negotiate.
	ta.Client(t).WithHeader("Accept", "text/html").WithToken(plain).Get("/v1/accept").AssertOK().AssertSee("text/html")

	// A session guard in the list keeps the browser redirect.
	ta.Client(t).WithHeader("Accept", "text/html").Get("/dashboard").AssertRedirect("/login")
}

// Routes under auth.ProblemJSON answer every 401 and 403 as problem+json,
// even with a session guard in the list (no redirect to the login page) and
// from custom middleware rejecting through web.Fail. Other errors and the
// handlers keep negotiating with the client's Accept header.
func TestProblemJSONGroupForcesAuthFailures(t *testing.T) {
	t.Parallel()

	ta := newApp(t, hash.Fake())
	unverified := ta.user(t, "uma@example.com", "pw", false)
	verified := ta.user(t, "ana@example.com", "pw", true)

	html := "text/html,application/xhtml+xml"
	tests := []struct {
		name   string
		client *vukemtest.Client
		path   string
		status int
	}{
		{"guest browser", ta.Client(t).WithHeader("Accept", html), "/papi/me", http.StatusUnauthorized},
		{"guest without Accept", ta.Client(t), "/papi/me", http.StatusUnauthorized},
		{
			"unverified browser", ta.Client(t).ActingAs("web", unverified).WithHeader("Accept", html),
			"/papi/verified", http.StatusForbidden,
		},
		{
			"session without token ability", ta.Client(t).ActingAs("web", verified).WithHeader("Accept", html),
			"/papi/posts", http.StatusForbidden,
		},
		{
			"custom middleware", ta.Client(t).ActingAs("web", verified).WithHeader("Accept", html),
			"/papi/scope", http.StatusForbidden,
		},
	}

	// The clients report through t, so the cases run without subtests.
	for _, tt := range tests {
		method := http.MethodGet
		if tt.path == "/papi/posts" {
			method = http.MethodDelete
		}

		res := tt.client.Do(method, tt.path, nil, "")
		if res.Status() != tt.status || res.Header("Location") != "" ||
			!strings.HasPrefix(res.Header("Content-Type"), "application/problem+json") {
			t.Errorf("%s: %s %s = %d %v, want %d problem+json", tt.name, method, tt.path, res.Status(),
				res.Recorder().Header(), tt.status)
		}
	}

	// Only auth failures are forced: other errors still negotiate.
	res := ta.Client(t).ActingAs("web", verified).WithHeader("Accept", html).Get("/papi/conflict")
	if res.Status() != http.StatusConflict ||
		strings.HasPrefix(res.Header("Content-Type"), "application/problem+json") {
		t.Errorf("GET /papi/conflict = %d %q, want 409 negotiated as HTML", res.Status(), res.Header("Content-Type"))
	}

	// Authenticated handlers still see the client's Accept header.
	ta.Client(t).ActingAs("web", verified).WithHeader("Accept", html).Get("/papi/me").AssertOK().AssertSee("text/html")

	// Order matters: Required mounted before ProblemJSON keeps the redirect.
	ta.Client(t).WithHeader("Accept", html).Get("/papi-late").AssertRedirect("/login")
}

func TestTokensUseTheirClock(t *testing.T) {
	t.Parallel()

	ta := newApp(t, hash.Fake())
	u := ta.user(t, "ana@example.com", "pw", true)
	ctx := t.Context()
	now := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	ta.tokens.Now = func() time.Time { return now }

	plain, tok, err := ta.tokens.Create(ctx, u.AuthID(), "cli", nil, time.Hour)
	if err != nil || tok.ExpiresAt == nil || !tok.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("Create = %+v %v, want expiry %v", tok, err, now.Add(time.Hour))
	}

	if found, err := ta.tokens.Find(ctx, plain); err != nil || found.LastUsedAt == nil || !found.LastUsedAt.Equal(now) {
		t.Fatalf("Find = %+v %v, want last use %v", found, err, now)
	}

	now = now.Add(time.Hour)
	if _, err := ta.tokens.Find(ctx, plain); err == nil {
		t.Fatal("Find after the clock passed expires_at = nil error, want rejected")
	}
}

func TestActingAsAndVerified(t *testing.T) {
	t.Parallel()

	ta := newApp(t, hash.Fake())
	verified := ta.user(t, "v@example.com", "pw", true)
	unverified := ta.user(t, "u@example.com", "pw", false)

	ta.Client(t).ActingAs("web", verified).Get("/verified").AssertOK()
	ta.Client(t).ActingAs("web", verified).Get("/whoami").AssertOK().AssertSee("v@example.com")
	ta.Client(t).Get("/whoami").AssertOK().AssertSee("guest")
	ta.Client(t).ActingAs("web", unverified).Get("/verified").AssertRedirect("/email/verify")
	ta.Client(t).ActingAs("web", unverified).GetJSON("/verified").AssertForbidden()
	ta.Client(t).ActingAs("web", verified).Get("/dashboard").AssertSee("hello v@example.com")

	// A session for a deleted user is not authenticated.
	db, _ := ta.DB()
	if err := orm.Delete(t.Context(), db, &unverified); err != nil {
		t.Fatal(err)
	}

	ta.Client(t).ActingAs("web", unverified).Get("/dashboard").AssertRedirect("/login")
}

func TestProviderEdgeCases(t *testing.T) {
	t.Parallel()

	db, err := database.Open(t.Context(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := schema.NewMigrator(db, migrations); err != nil {
		t.Fatal(err)
	}

	p := auth.NewORMProvider[User](db)
	if _, err := p.ByCredentials(context.Background(), map[string]string{"password": "x"}); err == nil {
		t.Fatal("password-only credentials accepted")
	}

	if _, err := p.ByCredentials(t.Context(), map[string]string{"nope": "x"}); err == nil {
		t.Fatal("unknown column accepted")
	}
}

func TestSecurity_A01_TokenRevokeScopedToOwner(t *testing.T) {
	t.Parallel()

	ta := newApp(t, hash.Fake())
	ana := ta.user(t, "ana@example.com", "pw", true)
	bob := ta.user(t, "bob@example.com", "pw", true)
	ctx := t.Context()

	anaPlain, anaTok, _ := ta.tokens.Create(ctx, ana.AuthID(), "ana-cli", []string{"posts:read"}, 0)
	_, bobTok, _ := ta.tokens.Create(ctx, bob.AuthID(), "bob-cli", nil, 0)

	// Bob passing Ana's token id must not revoke it.
	if ok, err := ta.tokens.RevokeFor(ctx, bob.AuthID(), anaTok.ID); err != nil || ok {
		t.Fatalf("RevokeFor(bob, ana's token) = %v, %v, want false, nil", ok, err)
	}

	if _, err := ta.tokens.Find(ctx, anaPlain); err != nil {
		t.Fatalf("ana's token gone after bob's revoke: %v", err)
	}

	list, err := ta.tokens.List(ctx, ana.AuthID())
	if err != nil || len(list) != 1 || list[0].ID != anaTok.ID || list[0].Name != "ana-cli" ||
		!list[0].Can("posts:read") || list[0].RevokedAt != nil {
		t.Fatalf("List(ana) = %+v, %v", list, err)
	}

	if ok, err := ta.tokens.RevokeFor(ctx, ana.AuthID(), anaTok.ID); err != nil || !ok {
		t.Fatalf("RevokeFor(ana, own token) = %v, %v, want true, nil", ok, err)
	}

	if _, err := ta.tokens.Find(ctx, anaPlain); err == nil {
		t.Error("Find(ana's token) after her revoke = nil error, want rejected")
	}

	if list, _ := ta.tokens.List(ctx, ana.AuthID()); len(list) != 1 || list[0].RevokedAt == nil {
		t.Errorf("List(ana) after revoke = %+v, want her token marked revoked", list)
	}

	// RevokeAll is scoped to its user too.
	if err := ta.tokens.RevokeAll(ctx, ana.AuthID()); err != nil {
		t.Fatal(err)
	}

	if list, _ := ta.tokens.List(ctx, bob.AuthID()); len(list) != 1 || list[0].ID != bobTok.ID ||
		list[0].RevokedAt != nil {
		t.Errorf("List(bob) = %+v, want only bob's live token", list)
	}
}

func TestSecurity_A04_TokensStoredHashed(t *testing.T) {
	t.Parallel()

	ta := newApp(t, hash.Fake())
	u := ta.user(t, "ana@example.com", "s3cret-pass", false)

	plain, tok, err := ta.tokens.Create(t.Context(), u.AuthID(), "cli", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	c := ta.Client(t)
	c.PostForm("/login", url.Values{"email": {"ana@example.com"}, "password": {"s3cret-pass"}, "remember": {"on"}})

	db, _ := ta.DB()

	var tokenHash, rememberHash string
	if err := db.QueryRowContext(t.Context(), "SELECT hash FROM access_tokens WHERE id = ?", tok.ID).
		Scan(&tokenHash); err != nil {
		t.Fatal(err)
	}

	if err := db.QueryRowContext(t.Context(), "SELECT hash FROM remember_tokens").Scan(&rememberHash); err != nil {
		t.Fatal(err)
	}

	secret := strings.SplitN(plain, ".", 2)[1]
	remember := strings.SplitN(c.CookieValue("remember_web"), ".", 2)[1]

	if strings.Contains(tokenHash, secret) || strings.Contains(rememberHash, remember) || len(tokenHash) != 64 {
		t.Fatalf("secrets stored in clear: %q %q", tokenHash, rememberHash)
	}
}

func TestUserFromContext(t *testing.T) {
	t.Parallel()

	if _, err := auth.UserFrom[User](t.Context(), "web"); err == nil {
		t.Fatal("user found in an empty context")
	}
}

func TestSecurity_A04_TokenPrefixForSecretScanning(t *testing.T) {
	t.Parallel()

	ta := newApp(t, hash.Fake())
	u := ta.user(t, "ana@example.com", "pw", true)
	ctx := t.Context()

	legacy, _, err := ta.tokens.Create(ctx, u.AuthID(), "legacy", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	ta.tokens.Prefix = "old_"

	rotated, _, err := ta.tokens.Create(ctx, u.AuthID(), "rotated", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	ta.tokens.Prefix = "vk_"

	plain, tok, err := ta.tokens.Create(ctx, u.AuthID(), "cli", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(plain, "vk_") || strings.HasPrefix(tok.ID, "vk_") {
		t.Fatalf("Create() = %q (id %q), want a vk_ prefix on the plain token only", plain, tok.ID)
	}

	// Tokens issued before the prefix, or under an earlier one, keep working.
	for name, p := range map[string]string{"prefixed": plain, "legacy": legacy, "earlier prefix": rotated} {
		if _, err := ta.tokens.Find(ctx, p); err != nil {
			t.Errorf("Find(%s) = %v, want nil", name, err)
		}
	}

	// The prefix is a label, not a credential: the secret still decides.
	// The replacement differs from the last character, so the secret always changes.
	wrong := "X"
	if strings.HasSuffix(plain, wrong) {
		wrong = "Y"
	}

	if _, err := ta.tokens.Find(ctx, plain[:len(plain)-1]+wrong); err == nil {
		t.Error("Find(prefixed token with a wrong secret) = nil, want error")
	}

	for _, bad := range []string{"vk", "vk._", "vk-_", "a b_", strings.Repeat("a", 32) + "_"} {
		ta.tokens.Prefix = bad
		if _, _, err := ta.tokens.Create(ctx, u.AuthID(), "bad", nil, 0); err == nil {
			t.Errorf("Create() with Prefix %q = nil, want error", bad)
		}
	}
}

func TestSecurity_A04_SessionKeptWhenUserLookupFails(t *testing.T) {
	t.Parallel()

	ta := newApp(t, hash.Fake())
	ta.user(t, "ana@example.com", "s3cret-pass", false)

	c := ta.Client(t)
	c.PostForm("/login", url.Values{"email": {"ana@example.com"}, "password": {"s3cret-pass"}}).
		AssertRedirect("/dashboard")

	// A failing lookup is a server error, not a logout or a login redirect.
	ta.down.Store(true)
	c.Get("/dashboard").AssertStatus(http.StatusServiceUnavailable)

	ta.down.Store(false)
	c.Get("/dashboard").AssertOK().AssertSee("hello ana@example.com")

	// A custom provider's wrapped sql.ErrNoRows means the user is gone:
	// the session is forgotten and the browser goes to login.
	ta.gone.Store(true)
	c.Get("/dashboard").AssertRedirect("/login")

	ta.gone.Store(false)
	c.Get("/dashboard").AssertRedirect("/login")
}

// ptBR serves the framework's built-in Brazilian Portuguese messages.
var ptBR = vukem.WithTranslations(fstest.MapFS{"en.json": {Data: []byte(`{}`)}, "pt-BR.json": {Data: []byte(`{}`)}})

// Translated login errors stay identical for unknown users and wrong
// passwords.
func TestSecurity_A07_TranslatedLoginErrorsDoNotRevealUsers(t *testing.T) {
	t.Parallel()

	ta := newApp(t, hash.Fake(), ptBR)
	ta.user(t, "ana@example.com", "s3cret-pass", false)

	for _, email := range []string{"ana@example.com", "nobody@example.com"} {
		ta.Client(t).AcceptJSON().WithHeader("Accept-Language", "pt-BR").
			PostJSON("/login", map[string]any{"email": email, "password": "nope"}).
			AssertJSONValidationErrors("email").
			AssertJSONPath("errors.email.0", "Essas credenciais não correspondem aos nossos registros.")
	}
}

func TestAuthErrorsTranslated(t *testing.T) {
	t.Parallel()

	ta := newApp(t, hash.Fake(), ptBR)
	u := ta.user(t, "u@example.com", "pw", false)

	plain, _, err := ta.tokens.Create(t.Context(), u.AuthID(), "cli", []string{"posts:read"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	pt := func() *vukemtest.Client {
		return ta.Client(t).AcceptJSON().WithHeader("Accept-Language", "pt-BR")
	}

	pt().GetJSON("/api/me").AssertUnauthorized().AssertJSONPath("detail", "Não autenticado.")
	pt().ActingAs("web", u).GetJSON("/verified").AssertForbidden().
		AssertJSONPath("detail", "Seu endereço de e-mail não foi verificado.")
	pt().WithToken(plain).Delete("/api/posts").AssertForbidden().
		AssertJSONPath("detail", "Este token não tem permissão para posts:delete.")

	// Without a matching locale the English messages stay.
	ta.Client(t).AcceptJSON().WithToken(plain).Delete("/api/posts").AssertForbidden().
		AssertJSONPath("detail", "This token cannot posts:delete.")
}
