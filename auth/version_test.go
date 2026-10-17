package auth_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/auth/hash"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/vukemtest"
	"github.com/AlexGalhardo/vukem/web"
)

// Bumping the user's session version ends every session logged in before,
// whatever the session driver (here the default encrypted cookie), and an
// unreadable recorded version counts as stale.
func TestSecurity_A07_SessionVersionEndsSessions(t *testing.T) {
	t.Parallel()

	ta := newApp(t, hash.Fake())
	u := ta.user(t, "vic@example.com", "s3cret-pass", false)

	login := func() *vukemtest.Client {
		c := ta.Client(t)
		c.PostForm("/login", url.Values{"email": {"vic@example.com"}, "password": {"s3cret-pass"}}).
			AssertRedirect("/dashboard")

		return c
	}

	laptop, phone := login(), login()
	laptop.Get("/dashboard").AssertOK()

	db, err := ta.DB()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := orm.QueryOf[User](db).Where("id", u.ID).
		Update(t.Context(), orm.Set{"session_version": 1}); err != nil {
		t.Fatal(err)
	}

	laptop.Get("/dashboard").AssertRedirect("/login")
	phone.Get("/dashboard").AssertRedirect("/login")
	phone.Get("/dashboard").AssertRedirect("/login") // the stale login is forgotten

	again := login()
	again.Get("/dashboard").AssertOK()

	// ActingAs records the current version.
	ta.Client(t).ActingAs("web", User{Model: u.Model, SessionVersion: 1}).Get("/dashboard").AssertOK()

	// Absent after a bump, malformed, stale, not an integer.
	for _, v := range []any{nil, "1", 0, 1.5} {
		m, err := ta.Sessions()
		if err != nil {
			t.Fatal(err)
		}

		data := map[string]any{"auth_web": u.AuthID(), web.SessionUserKey: "web:" + u.AuthID(), "cart": "3 items"}
		if v != nil {
			data["_auth_version_web"] = v
		}

		withSession(ta.Client(t), m.Cookies(t.Context(), data)).Get("/dashboard").
			AssertRedirect("/login").AssertSessionMissing("auth_web").AssertSessionMissing(web.SessionUserKey).
			AssertSessionMissing("cart") // the whole session ends
	}
}

// RefreshSession keeps the current session signed in across a version bump
// that ends the others (2FA turned on).
func TestRefreshSessionKeepsCurrent(t *testing.T) {
	t.Parallel()

	ta := newApp(t, hash.Fake())
	ta.user(t, "wes@example.com", "s3cret-pass", false)

	login := func() *vukemtest.Client {
		c := ta.Client(t)
		c.PostForm("/login", url.Values{"email": {"wes@example.com"}, "password": {"s3cret-pass"}}).
			AssertRedirect("/dashboard")

		return c
	}

	current, other := login(), login()

	current.PostForm("/sessions/end-others", nil).AssertRedirect("/dashboard")
	current.Get("/dashboard").AssertOK()
	other.Get("/dashboard").AssertRedirect("/login")
}

// A remember-me token is bound to the session version it was issued
// under: once the version changes, a stolen remember cookie no longer logs
// in, and RefreshSession issues the current device a new one.
func TestSecurity_A07_RememberTokenBoundToSessionVersion(t *testing.T) {
	t.Parallel()

	ta := newApp(t, hash.Fake())
	u := ta.user(t, "rae@example.com", "s3cret-pass", false)

	login := func() *vukemtest.Client {
		c := ta.Client(t)
		c.PostForm("/login", url.Values{"email": {"rae@example.com"}, "password": {"s3cret-pass"}, "remember": {"on"}}).
			AssertRedirect("/dashboard")

		return c
	}

	// Ended by a bump made elsewhere (password reset).
	stolen := login().CookieValue("remember_web")

	db, err := ta.DB()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := orm.QueryOf[User](db).Where("id", u.ID).
		Update(t.Context(), orm.Set{"session_version": 1}); err != nil {
		t.Fatal(err)
	}

	ta.Client(t).WithCookie("remember_web", stolen).Get("/dashboard").AssertRedirect("/login")

	// The current device keeps signing in after its own bump (2FA on).
	current := login()
	other := login().CookieValue("remember_web")
	before := current.CookieValue("remember_web")

	current.PostForm("/sessions/end-others", nil).AssertRedirect("/dashboard")

	after := current.CookieValue("remember_web")
	if after == "" || after == before {
		t.Fatalf("remember cookie after RefreshSession = %q, want a new token", after)
	}

	ta.Client(t).WithCookie("remember_web", other).Get("/dashboard").AssertRedirect("/login")
	ta.Client(t).WithCookie("remember_web", before).Get("/dashboard").AssertRedirect("/login")
	ta.Client(t).WithCookie("remember_web", after).Get("/dashboard").AssertOK()
}

// RefreshSession and Logout delete the remember-me token in the request
// only when it belongs to the signed-in user: a cookie planted from
// another account cannot be used to destroy that account's token.
func TestRememberTokenDeletedOnlyForItsUser(t *testing.T) {
	t.Parallel()

	ta := newApp(t, hash.Fake())
	ta.user(t, "ann@example.com", "s3cret-pass", false)
	ta.user(t, "bob@example.com", "s3cret-pass", false)

	login := func(email string) *vukemtest.Client {
		c := ta.Client(t)
		c.PostForm("/login", url.Values{"email": {email}, "password": {"s3cret-pass"}, "remember": {"on"}}).
			AssertRedirect("/dashboard")

		return c
	}

	anns := login("ann@example.com").CookieValue("remember_web")

	bob := login("bob@example.com")
	bob.WithCookie("remember_web", anns).PostForm("/sessions/end-others", nil).AssertRedirect("/dashboard")

	bob = login("bob@example.com")
	bob.WithCookie("remember_web", anns).PostForm("/logout", nil).AssertRedirect("/")

	ta.Client(t).WithCookie("remember_web", anns).Get("/dashboard").AssertOK().AssertSee("ann@example.com")
}

type ptrUser struct{ User }

func (u *ptrUser) AuthSessionVersion() int64 { return u.SessionVersion }

// A session version method on the pointer while the guard holds values
// would never be called: construction refuses it instead of failing open.
func TestSessionGuardRejectsPointerSessionVersioner(t *testing.T) {
	t.Parallel()

	defer func() {
		r := recover()
		if msg, _ := r.(string); !strings.Contains(msg, "pointer receiver") {
			t.Fatalf(
				"NewSessionGuard(pointer-receiver SessionVersioner) panic = %v, want a pointer receiver message",
				r,
			)
		}
	}()

	auth.NewSessionGuard[ptrUser]("web", nil, hash.Fake(), nil)
}
