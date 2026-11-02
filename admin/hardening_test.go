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
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/vukemtest"
	"github.com/AlexGalhardo/vukem/web"
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

func TestSecurity_A07_AdminIdleTimeout(t *testing.T) {
	t.Parallel()

	// A request outside the panel does not refresh the idle clock.
	clock := vukemtest.NewFakeClock(time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC))
	app := newApp(t, options{clock: clock, harden: func(c *admin.Config) { c.IdleTimeout = 30 * time.Minute }})

	c := as(t, app, "admin")
	c.Get("/admin").AssertOK()
	clock.Advance(20 * time.Minute)
	c.Get("/whoami").AssertSee("user=admin@example.com")
	clock.Advance(11 * time.Minute)
	c.Get("/whoami").AssertSee("user=admin@example.com")
	c.Get("/admin").AssertRedirect("/login")
	c.Get("/whoami").AssertSee("guest")

	// A panel request resets the clock. The app session itself outlives it.
	clock2 := vukemtest.NewFakeClock(time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC))
	app2 := newApp(t, options{clock: clock2, harden: func(c *admin.Config) { c.IdleTimeout = 30 * time.Minute }})
	c2 := as(t, app2, "admin")
	c2.Get("/admin").AssertOK()
	clock2.Advance(20 * time.Minute)
	c2.Get("/admin").AssertOK()
	clock2.Advance(20 * time.Minute)
	c2.Get("/admin").AssertOK()
	clock2.Advance(31 * time.Minute)
	c2.Get("/admin").AssertRedirect("/login")
}

func TestSecurity_A01_AdminGuardCoversCustomRoutes(t *testing.T) {
	t.Parallel()

	// vukemtest requests come from 192.0.2.1. A route beside the panel
	// ignores AllowCIDRs until it uses panel.Guard.
	hit := func(c *web.Ctx) error { return c.String(200, "moderation") }
	beside := func(guard bool) func(*router.Router, *admin.Panel) {
		return func(r *router.Router, p *admin.Panel) {
			rt := r.Get("/admin/reports/{id}/resolve", hit)
			if guard {
				rt.Use(p.Guard())
			}
		}
	}

	denied := newApp(t, options{
		harden: func(c *admin.Config) {
			c.AllowCIDRs = []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
		},
		beside: beside(false),
	})
	as(t, denied, "admin").Get("/admin/reports/1/resolve").AssertOK()
	denied.Client(t).Get("/admin").AssertForbidden()

	guarded := newApp(t, options{
		harden: func(c *admin.Config) {
			c.AllowCIDRs = []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
		},
		beside: beside(true),
	})
	as(t, guarded, "admin").Get("/admin/reports/1/resolve").AssertForbidden()
	guarded.Client(t).Get("/admin/reports/1/resolve").AssertForbidden()

	allowed := newApp(t, options{
		harden: func(c *admin.Config) {
			c.AllowCIDRs = []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}
		},
		beside: beside(true),
	})
	as(t, allowed, "admin").Get("/admin/reports/1/resolve").AssertOK().AssertSee("moderation")
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
		"negative age":  {DB: db, Guard: guard, MaxSessionAge: -time.Minute},
		"negative idle": {DB: db, Guard: guard, IdleTimeout: -time.Minute},
		"zero prefix":   {DB: db, Guard: guard, AllowCIDRs: []netip.Prefix{{}}},
		"deny 500":      {DB: db, Guard: guard, DenyStatus: 500},
		"remote 2fa":    {DB: db, Guard: guard, TwoFactorRedirect: "//evil.test/2fa"},
		"relative 2fa":  {DB: db, Guard: guard, TwoFactorRedirect: "settings/2fa"},
		"flash only":    {DB: db, Guard: guard, TwoFactorFlash: "Turn on 2FA."},
	} {
		if _, err := admin.New(cfg); err == nil {
			t.Errorf("New(%s) = nil error, want an error", name)
		}
	}
}

func TestSecurity_A01_AdminDenyStatusHidesPanel(t *testing.T) {
	t.Parallel()

	// vukemtest requests come from 192.0.2.1.
	app := newApp(t, options{harden: func(c *admin.Config) {
		c.AllowCIDRs = []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
		c.DenyStatus = 404
	}})

	as(t, app, "admin").Get("/admin").AssertNotFound()
	app.Client(t).Get("/admin").AssertNotFound()
}

func TestSecurity_A07_AdminTwoFactorRedirect(t *testing.T) {
	t.Parallel()

	app := newApp(t, options{harden: func(c *admin.Config) {
		c.TwoFactor = func(context.Context) (bool, error) { return false, nil }
		c.TwoFactorRedirect = "/settings/2fa"
	}})
	as(t, app, "admin").Get("/admin").AssertRedirect("/settings/2fa")

	// A failing lookup still denies (fail closed), never redirects.
	broken := newApp(t, options{harden: func(c *admin.Config) {
		c.TwoFactor = func(context.Context) (bool, error) { return true, errors.New("db down") }
		c.TwoFactorRedirect = "/settings/2fa"
	}})
	as(t, broken, "admin").Get("/admin").AssertForbidden()
}

func TestSecurity_A07_AdminTrackIdle(t *testing.T) {
	t.Parallel()

	tracked := func(r *router.Router, p *admin.Panel) {
		r.Get("/browse", func(c *web.Ctx) error { return c.String(200, "browse") }).Use(p.TrackIdle())
	}

	// Activity on a tracked route keeps the panel session alive.
	clock := vukemtest.NewFakeClock(time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC))
	idle := func(c *admin.Config) { c.IdleTimeout = 30 * time.Minute }
	app := newApp(t, options{clock: clock, beside: tracked, harden: idle})

	c := as(t, app, "admin")
	c.Get("/admin").AssertOK()

	for range 3 {
		clock.Advance(20 * time.Minute)
		c.Get("/browse").AssertOK()
	}

	c.Get("/admin").AssertOK()

	// Idle past the timeout, a tracked route ends the session too.
	clock.Advance(31 * time.Minute)
	c.Get("/browse").AssertOK()
	c.Get("/whoami").AssertSee("guest")

	// A visitor who never used the panel gets no clock (and no cookie write).
	guest := app.Client(t)
	guest.Get("/browse").AssertOK()

	if got := guest.Get("/browse").Header("Set-Cookie"); got != "" {
		t.Errorf("guest Set-Cookie = %q, want none", got)
	}
}

func TestSecurity_A07_AdminClockCoversEveryAdminRequest(t *testing.T) {
	t.Parallel()

	clocked := func(r *router.Router, p *admin.Panel) {
		r.Get("/browse", func(c *web.Ctx) error { return c.String(200, "browse") }).Use(p.Clock())
	}

	// A moderator who never opens the panel is still capped: idle.
	clock := vukemtest.NewFakeClock(time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC))
	idle := func(c *admin.Config) { c.IdleTimeout = 30 * time.Minute }
	app := newApp(t, options{clock: clock, beside: clocked, harden: idle})

	editor := as(t, app, "editor")
	editor.Get("/browse").AssertOK()

	for range 3 {
		clock.Advance(20 * time.Minute)
		editor.Get("/browse").AssertOK()
	}

	editor.Get("/whoami").AssertSee("user=editor@example.com")
	clock.Advance(31 * time.Minute)
	editor.Get("/browse").AssertOK()
	editor.Get("/whoami").AssertSee("guest")

	// Absolute cap from the first admin request, however active the user.
	clock2 := vukemtest.NewFakeClock(time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC))
	capped := func(c *admin.Config) { c.MaxSessionAge = time.Hour; c.IdleTimeout = 30 * time.Minute }
	app2 := newApp(t, options{clock: clock2, beside: clocked, harden: capped})

	a := as(t, app2, "admin")
	for range 3 {
		a.Get("/browse").AssertOK()
		clock2.Advance(25 * time.Minute)
	}

	a.Get("/browse").AssertOK()
	a.Get("/whoami").AssertSee("guest")

	// A user without the panel ability is left alone (no session write).
	reader := as(t, app, "reader")
	reader.Get("/browse").AssertOK()
	clock.Advance(2 * time.Hour)
	reader.Get("/browse").AssertOK()
	reader.Get("/whoami").AssertSee("user=reader@example.com")
}

func TestSecurity_A07_AdminTwoFactorRedirectFlash(t *testing.T) {
	t.Parallel()

	app := newApp(t, options{harden: func(c *admin.Config) {
		c.TwoFactor = func(context.Context) (bool, error) { return false, nil }
		c.TwoFactorRedirect = "/settings/2fa"
		c.TwoFactorFlash = "Turn on two-factor authentication to use the admin panel."
	}})
	as(t, app, "admin").Get("/admin").AssertRedirect("/settings/2fa").AssertSessionHas(admin.TwoFactorFlashKey)
}
