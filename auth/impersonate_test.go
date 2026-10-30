package auth_test

import (
	"net/url"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/auth/hash"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/web"
)

// Impersonation swaps the signed-in user for a bounded time, remembers who
// started it, refuses nesting and self-impersonation, and gives the
// original user back on stop.
func TestSecurity_A01_ImpersonationIsBoundedAndReversible(t *testing.T) {
	t.Parallel()

	ta := newApp(t, hash.Fake())
	admin := ta.user(t, "ada@example.com", "s3cret-pass", true)
	bob := ta.user(t, "bob@example.com", "s3cret-pass", true)
	carol := ta.user(t, "carol@example.com", "s3cret-pass", true)

	c := ta.Client(t)
	c.PostForm("/login", url.Values{"email": {"ada@example.com"}, "password": {"s3cret-pass"}}).
		AssertRedirect("/dashboard")
	before := sessionCookie(c)

	c.PostForm("/impersonate/"+admin.AuthID(), nil).AssertStatus(409) // self
	c.Get("/whoami").AssertSee("ada@example.com")

	c.PostForm("/impersonate/"+bob.AuthID(), nil).AssertRedirect("/whoami")

	if sessionCookie(c) == before {
		t.Error("Impersonate kept the session id, want it regenerated")
	}

	c.Get("/whoami").AssertSee("bob@example.com")
	c.Get("/impersonator").AssertSee(admin.AuthID() + " true")

	c.PostForm("/impersonate/"+carol.AuthID(), nil).AssertStatus(409) // nested
	c.Get("/whoami").AssertSee("bob@example.com")

	c.PostForm("/impersonate-stop", nil).AssertRedirect("/whoami")
	c.Get("/whoami").AssertSee("ada@example.com")
	c.Get("/impersonator").AssertSee(" false")

	c.PostForm("/impersonate-stop", nil).AssertStatus(409) // not impersonating
	c.Get("/whoami").AssertSee("ada@example.com")
}

// An expired or unreadable deadline ends the whole session: it never falls
// back to the impersonated user, nor silently to the admin.
func TestSecurity_A01_ImpersonationExpiresClosed(t *testing.T) {
	t.Parallel()

	ta := newApp(t, hash.Fake())
	admin := ta.user(t, "ada@example.com", "s3cret-pass", true)
	bob := ta.user(t, "bob@example.com", "s3cret-pass", true)

	m, err := ta.Sessions()
	if err != nil {
		t.Fatal(err)
	}

	for _, until := range []any{
		time.Now().Add(-time.Second).UTC().Format(time.RFC3339), "not a time", 12345, nil,
	} {
		data := map[string]any{
			"auth_web":               bob.AuthID(),
			web.SessionUserKey:       "web:" + bob.AuthID(),
			"_auth_impersonator_web": admin.AuthID(),
			"cart":                   "3 items",
		}
		if until != nil {
			data["_auth_impersonate_until_web"] = until
		}

		withSession(ta.Client(t), m.Cookies(t.Context(), data)).Get("/whoami").
			AssertSee("guest").AssertSessionMissing("auth_web").AssertSessionMissing("cart")
	}

	// A live deadline keeps the impersonated user.
	data := map[string]any{
		"auth_web":                    bob.AuthID(),
		web.SessionUserKey:            "web:" + bob.AuthID(),
		"_auth_impersonator_web":      admin.AuthID(),
		"_auth_impersonate_until_web": time.Now().Add(time.Minute).UTC().Format(time.RFC3339),
	}
	withSession(ta.Client(t), m.Cookies(t.Context(), data)).Get("/whoami").AssertSee("bob@example.com")
}

// Ending the actor's sessions (password reset) during an impersonation
// signs the whole session out on stop instead of restoring the actor.
func TestSecurity_A07_StopImpersonatingChecksActorSessionVersion(t *testing.T) {
	t.Parallel()

	ta := newApp(t, hash.Fake())
	admin := ta.user(t, "ada@example.com", "s3cret-pass", true)
	bob := ta.user(t, "bob@example.com", "s3cret-pass", true)

	c := ta.Client(t)
	c.PostForm("/login", url.Values{"email": {"ada@example.com"}, "password": {"s3cret-pass"}}).
		AssertRedirect("/dashboard")
	c.PostForm("/impersonate/"+bob.AuthID(), nil).AssertRedirect("/whoami")

	db, err := ta.DB()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := orm.QueryOf[User](db).Where("id", admin.ID).
		Update(t.Context(), orm.Set{"session_version": 1}); err != nil {
		t.Fatal(err)
	}

	c.PostForm("/impersonate-stop", nil)
	c.Get("/whoami").AssertSee("guest")
}
