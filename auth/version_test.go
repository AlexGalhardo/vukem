package auth_test

import (
	"net/url"
	"testing"

	"github.com/AlexGalhardo/vukem/auth/hash"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/vukemtest"
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

		data := map[string]any{"auth_web": u.AuthID()}
		if v != nil {
			data["_auth_version_web"] = v
		}

		withSession(ta.Client(t), m.Cookies(t.Context(), data)).Get("/dashboard").
			AssertRedirect("/login").AssertSessionMissing("auth_web")
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
