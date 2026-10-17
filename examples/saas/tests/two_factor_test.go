package tests

import (
	"strings"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/auth/totp"
	"github.com/AlexGalhardo/vukem/examples/saas/bootstrap"
	"github.com/AlexGalhardo/vukem/vukemtest"
)

// totpCode returns the app code of secret steps periods from now.
func totpCode(t *testing.T, secret string, steps int) string {
	t.Helper()

	code, err := totp.Code(secret, time.Now().Add(time.Duration(steps)*30*time.Second), totp.Options{})
	if err != nil {
		t.Fatal(err)
	}

	return code
}

// wrongCode returns a code that is valid at no step near now.
func wrongCode(t *testing.T, secret string) string {
	t.Helper()

	near := map[string]bool{}
	for step := -1; step <= 1; step++ {
		near[totpCode(t, secret, step)] = true
	}

	for _, code := range []string{"000000", "111111", "222222", "333333"} {
		if !near[code] {
			return code
		}
	}

	t.Fatal("no wrong code")

	return ""
}

// rejected posts body to path from page and checks the validation error
// on field shown by component.
func rejected(t *testing.T, c *vukemtest.Client, page, path, component, field string, body map[string]any) {
	t.Helper()

	c.WithHeader("Referer", page).PostJSON(path, body).AssertRedirect(page)
	c.Get(page).AssertInertia(component).HasProp("errors." + field)
}

// recoveryCodes returns the recovery codes shown on the settings page.
func recoveryCodes(t *testing.T, c *vukemtest.Client) []string {
	t.Helper()

	v, _ := c.Get("/user/two-factor").AssertInertia("Auth/TwoFactor").Prop("recoveryCodes")
	list, _ := v.([]any)

	codes := make([]string, 0, len(list))
	for _, code := range list {
		s, _ := code.(string)
		codes = append(codes, s)
	}

	return codes
}

// enableTwoFactor registers email on c, turns two-factor authentication
// on and returns the secret and the recovery codes. c stays logged in.
func enableTwoFactor(t *testing.T, app *vukemtest.App, c *vukemtest.Client, email string) (string, []string) {
	t.Helper()

	register(t, c, email)

	c.Get("/user/two-factor").AssertInertia("Auth/TwoFactor").PropEquals("enabled", false).PropEquals("pending", false)
	c.PostJSON("/user/two-factor", nil).AssertRedirect("/user/two-factor")

	page := c.Get("/user/two-factor").AssertInertia("Auth/TwoFactor").PropEquals("pending", true)

	secret, _ := page.Prop("secret")
	qr, _ := page.Prop("qr")

	s, _ := secret.(string)
	if svg, _ := qr.(string); s == "" || !strings.HasPrefix(svg, "<svg") {
		t.Fatalf("secret = %v, qr = %.40v", secret, qr)
	}

	// The secret is encrypted at rest.
	vukemtest.AssertDatabaseMissing(t, app, "users", map[string]any{"two_factor_secret": s})

	rejected(t, c, "/user/two-factor", "/user/two-factor/confirm", "Auth/TwoFactor", "code",
		map[string]any{"code": wrongCode(t, s)})
	c.PostJSON("/user/two-factor/confirm", map[string]any{"code": totpCode(t, s, 0)}).AssertRedirect("/user/two-factor")

	codes := recoveryCodes(t, c)
	if len(codes) != 8 {
		t.Fatalf("recovery codes = %v", codes)
	}

	// Shown once.
	c.Get("/user/two-factor").AssertInertia("Auth/TwoFactor").
		PropEquals("enabled", true).
		PropEquals("recoveryCodes", nil).
		PropEquals("recoveryCodesLeft", 8).
		PropEquals("secret", nil)

	return s, codes
}

func passwordStep(c *vukemtest.Client, email string, remember bool) *vukemtest.Response {
	return c.PostJSON("/login", map[string]any{
		"email": email, "password": "correct horse battery", "remember": remember,
	})
}

func TestTwoFactorLogin(t *testing.T) {
	t.Parallel()

	app := vukemtest.New(t, bootstrap.New)
	owner := app.Client(t).Inertia("")
	secret, _ := enableTwoFactor(t, app, owner, "ana2fa@example.com")

	owner.PostJSON("/user/two-factor", nil).AssertStatus(409) // a confirmed setup is not replaced
	owner.PostJSON("/logout", nil).AssertRedirect("/")

	// The password alone does not log in.
	c := app.Client(t).Inertia("")
	passwordStep(c, "ana2fa@example.com", true).AssertRedirect("/two-factor-challenge")
	c.Get("/dashboard").AssertRedirect("/login")
	c.Get("/two-factor-challenge").AssertInertia("Auth/TwoFactorChallenge").PropEquals("auth.user", nil)

	rejected(t, c, "/two-factor-challenge", "/two-factor-challenge", "Auth/TwoFactorChallenge", "code",
		map[string]any{"code": wrongCode(t, secret), "recovery_code": ""})

	// The confirmation used the current step: the next one logs in.
	code := totpCode(t, secret, 1)
	c.PostJSON("/two-factor-challenge", map[string]any{"code": code, "recovery_code": ""}).AssertRedirect("/dashboard")
	c.Get("/dashboard").AssertInertia("Dashboard").PropEquals("auth.user.email", "ana2fa@example.com")

	if c.CookieValue("remember_web") == "" {
		t.Error("remember me was asked at the password step but no cookie was set")
	}

	// The same code cannot be used twice.
	replay := app.Client(t).Inertia("")
	passwordStep(replay, "ana2fa@example.com", false).AssertRedirect("/two-factor-challenge")
	rejected(t, replay, "/two-factor-challenge", "/two-factor-challenge", "Auth/TwoFactorChallenge", "code",
		map[string]any{"code": code, "recovery_code": ""})
	replay.Get("/dashboard").AssertRedirect("/login")
}

func TestTwoFactorRecoveryCodes(t *testing.T) {
	t.Parallel()

	app := vukemtest.New(t, bootstrap.New)
	owner := app.Client(t).Inertia("")
	_, codes := enableTwoFactor(t, app, owner, "bob2fa@example.com")

	c := app.Client(t).Inertia("")
	passwordStep(c, "bob2fa@example.com", false).AssertRedirect("/two-factor-challenge")
	c.PostJSON("/two-factor-challenge", map[string]any{"code": "", "recovery_code": codes[0]}).
		AssertRedirect("/dashboard")
	c.PostJSON("/logout", nil).AssertRedirect("/")

	// A recovery code works once.
	passwordStep(c, "bob2fa@example.com", false).AssertRedirect("/two-factor-challenge")
	rejected(t, c, "/two-factor-challenge", "/two-factor-challenge", "Auth/TwoFactorChallenge", "recovery_code",
		map[string]any{"code": "", "recovery_code": codes[0]})
	c.PostJSON("/two-factor-challenge", map[string]any{"code": "", "recovery_code": codes[1]}).
		AssertRedirect("/dashboard")

	owner.Get("/user/two-factor").AssertInertia("Auth/TwoFactor").PropEquals("recoveryCodesLeft", 6)

	// Regenerating replaces every code.
	owner.PostJSON("/user/two-factor/recovery-codes", nil).AssertRedirect("/user/two-factor")

	if fresh := recoveryCodes(t, owner); len(fresh) != 8 || fresh[0] == codes[2] {
		t.Fatalf("regenerated codes = %v", fresh)
	}

	c.PostJSON("/logout", nil).AssertRedirect("/")
	passwordStep(c, "bob2fa@example.com", false).AssertRedirect("/two-factor-challenge")
	rejected(t, c, "/two-factor-challenge", "/two-factor-challenge", "Auth/TwoFactorChallenge", "recovery_code",
		map[string]any{"code": "", "recovery_code": codes[2]})
}

func TestTwoFactorThrottle(t *testing.T) {
	t.Parallel()

	app := vukemtest.New(t, bootstrap.New)
	secret, _ := enableTwoFactor(t, app, app.Client(t).Inertia(""), "cid2fa@example.com")

	attacker := app.Client(t).Inertia("")
	passwordStep(attacker, "cid2fa@example.com", false).AssertRedirect("/two-factor-challenge")

	for range 5 {
		attacker.PostJSON("/two-factor-challenge", map[string]any{"code": wrongCode(t, secret)})
	}

	attacker.PostJSON("/two-factor-challenge", map[string]any{"code": totpCode(t, secret, 1)}).AssertStatus(429)

	// Confirming a new setup is limited too.
	c := app.Client(t).Inertia("")
	register(t, c, "dan2fa@example.com")
	c.PostJSON("/user/two-factor", nil).AssertRedirect("/user/two-factor")

	for range 5 {
		c.PostJSON("/user/two-factor/confirm", map[string]any{"code": "000000"})
	}

	c.PostJSON("/user/two-factor/confirm", map[string]any{"code": "000000"}).AssertStatus(429)
}

func TestTwoFactorDisableNeedsPassword(t *testing.T) {
	t.Parallel()

	app := vukemtest.New(t, bootstrap.New)
	c := app.Client(t).Inertia("")
	enableTwoFactor(t, app, c, "eve2fa@example.com")

	rejected(t, c, "/user/two-factor", "/user/two-factor/disable", "Auth/TwoFactor", "password",
		map[string]any{"password": "not my password"})
	c.Get("/user/two-factor").AssertInertia("Auth/TwoFactor").PropEquals("enabled", true)

	c.PostJSON("/user/two-factor/disable", map[string]any{"password": "correct horse battery"}).
		AssertRedirect("/user/two-factor")
	c.Get("/user/two-factor").AssertInertia("Auth/TwoFactor").PropEquals("enabled", false).PropEquals("pending", false)

	c.PostJSON("/logout", nil).AssertRedirect("/")
	passwordStep(c, "eve2fa@example.com", false).AssertRedirect("/dashboard")
}

func TestTwoFactorChallengeNeedsPasswordStep(t *testing.T) {
	t.Parallel()

	app := vukemtest.New(t, bootstrap.New)
	c := app.Client(t).Inertia("")

	c.Get("/two-factor-challenge").AssertRedirect("/login")
	c.PostJSON("/two-factor-challenge", map[string]any{"code": "123456"}).AssertRedirect("/login")
}
