package auth_test

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/auth/hash"
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/schema"
	"github.com/AlexGalhardo/vukem/vukemtest"
	"github.com/AlexGalhardo/vukem/web"
)

type User struct {
	orm.Model

	Email      string
	Password   string `json:"-"`
	VerifiedAt orm.NullTime
}

func (u User) AuthID() string       { return strconv.FormatInt(u.ID, 10) }
func (u User) AuthPassword() string { return u.Password }
func (u User) EmailVerified() bool  { return u.VerifiedAt.Valid }

var migrations = []schema.Migration{{
	ID: "1_auth",
	Up: func(s *schema.Schema) error {
		if err := s.Create("users", func(t *schema.Table) {
			t.ID()
			t.String("email").Unique()
			t.String("password")
			t.Timestamp("verified_at").Nullable()
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
	tokens *auth.Tokens
	hasher hash.Hasher
}

func newApp(t *testing.T, hasher hash.Hasher) *testApp {
	t.Helper()

	ta := &testApp{hasher: hasher}

	build := func(opts ...vukem.Option) (*vukem.App, error) {
		return vukem.New(append(opts, vukem.WithMigrations(migrations),
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
				ta.web = auth.NewSessionGuard("web", users, hasher, auth.NewRememberStore(db))
				ta.web.InsecureCookies = true
				ta.api = auth.NewTokenGuard("api", users, ta.tokens)

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
		AssertRedirect("/dashboard")

	if after := sessionCookie(c); after == before {
		t.Fatal("session cookie unchanged on login")
	}

	c.Get("/dashboard").AssertOK().AssertSee("hello ana@example.com")
	c.Get("/login").AssertRedirect("/") // Guest
	c.Get("/verified").AssertForbidden()

	c.PostForm("/logout", nil).AssertRedirect("/")
	c.Get("/dashboard").AssertRedirect("/login")

	// JSON clients get 401 instead of a redirect.
	ta.Client(t).GetJSON("/dashboard").AssertUnauthorized().AssertHeader("WWW-Authenticate", `Bearer realm="api"`)
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

func TestActingAsAndVerified(t *testing.T) {
	t.Parallel()

	ta := newApp(t, hash.Fake())
	verified := ta.user(t, "v@example.com", "pw", true)
	unverified := ta.user(t, "u@example.com", "pw", false)

	ta.Client(t).ActingAs("web", verified).Get("/verified").AssertOK()
	ta.Client(t).ActingAs("web", verified).Get("/whoami").AssertOK().AssertSee("v@example.com")
	ta.Client(t).Get("/whoami").AssertOK().AssertSee("guest")
	ta.Client(t).ActingAs("web", unverified).Get("/verified").AssertForbidden()
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
