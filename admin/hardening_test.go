package admin_test

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/admin"
	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/auth/hash"
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/vukemtest"
)

func TestSecurity_A07_AdminRequiresTwoFactor(t *testing.T) {
	t.Parallel()

	enabled := func(ctx context.Context) (bool, error) {
		u, err := auth.UserFrom[User](ctx, "web")
		if err != nil {
			return false, err
		}

		return u.Role == "admin", nil
	}

	app := newApp(t, options{harden: func(c *admin.Config) { c.TwoFactor = enabled }})
	seed(t, app)

	as(t, app, "admin").Get("/admin").AssertOK()

	// The editor holds the panel ability but has no 2FA: every page is 403.
	editor := as(t, app, "editor")
	for _, a := range append([]action{{"GET", "/admin", nil}}, actions("posts")...) {
		do(editor, a).AssertForbidden()
	}

	vukemtest.AssertDatabaseHas(t, app, "posts", map[string]any{"id": 1, "title": "Seed"})

	// A failing lookup denies (fail closed).
	broken := newApp(t, options{harden: func(c *admin.Config) {
		c.TwoFactor = func(context.Context) (bool, error) { return true, errors.New("db down") }
	}})
	as(t, broken, "admin").Get("/admin").AssertForbidden()
}

func TestSecurity_A07_AdminMaxSessionAge(t *testing.T) {
	t.Parallel()

	clock := vukemtest.NewFakeClock(time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC))
	app := newApp(t, options{clock: clock, harden: func(c *admin.Config) { c.MaxSessionAge = 30 * time.Minute }})

	c := as(t, app, "admin")
	c.Get("/admin").AssertOK()

	clock.Advance(20 * time.Minute)
	c.Get("/admin").AssertOK()

	// Past the cap the session is still valid for the rest of the app (idle
	// 15m, lifetime 24h), but the panel ends it and asks for a new login.
	clock.Advance(15 * time.Minute)
	c.Get("/whoami").AssertSee("user=admin@example.com")
	c.Get("/admin").AssertRedirect("/login")
	c.Get("/whoami").AssertSee("guest")

	// A fresh login gets a fresh session.
	as(t, app, "admin2").Get("/whoami").AssertSee("user=admin2@example.com")
}

func TestSecurity_A01_AdminAllowCIDRs(t *testing.T) {
	t.Parallel()

	// vukemtest requests come from 192.0.2.1.
	denied := newApp(t, options{harden: func(c *admin.Config) {
		c.AllowCIDRs = []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	}})
	seed(t, denied)

	admin1 := as(t, denied, "admin")
	for _, a := range append([]action{{"GET", "/admin", nil}}, actions("posts")...) {
		do(admin1, a).AssertForbidden()
	}

	// Guests are refused too, before any login redirect.
	denied.Client(t).Get("/admin").AssertForbidden()

	allowed := newApp(t, options{harden: func(c *admin.Config) {
		c.AllowCIDRs = []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("192.0.2.0/24")}
	}})
	as(t, allowed, "admin").Get("/admin").AssertOK()
}

func TestNewRejectsBadHardening(t *testing.T) {
	t.Parallel()

	db, err := database.Open(t.Context(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	guard := auth.NewSessionGuard("web", auth.NewORMProvider[User](db), hash.Fake(), nil)

	for name, cfg := range map[string]admin.Config{
		"negative age": {DB: db, Guard: guard, MaxSessionAge: -time.Minute},
		"zero prefix":  {DB: db, Guard: guard, AllowCIDRs: []netip.Prefix{{}}},
	} {
		if _, err := admin.New(cfg); err == nil {
			t.Errorf("New(%s) = nil error, want an error", name)
		}
	}
}
