package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// writeFiles creates files (slash paths) under dir.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()

	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// The patches must keep matching the scaffolds they rewrite.
func TestTwoFactorPatchesMatchScaffolds(t *testing.T) {
	t.Parallel()

	data := map[string]string{"Module": "example.com/app", "Providers": `"github"`, "FirstProvider": "github"}

	tests := []struct {
		src string
		p   patch
	}{
		{"scaffolds/auth-web/app/controllers/account/account.go.tmpl", loginTwoFactorPatch},
		{"scaffolds/auth-inertia/app/controllers/account/account.go.tmpl", loginTwoFactorPatch},
		{"scaffolds/auth-social/routes/social.go.tmpl", socialTwoFactorPatch},
		{"scaffolds/api/app/controllers/apiv1/tokens.go.tmpl", apiTwoFactorPatch},
		{"scaffolds/auth-web/resources/views/auth/dashboard.html", dashboardTwoFactorPatches[0]},
		{"scaffolds/inertia-react/resources/js/pages/Dashboard.tsx", dashboardTwoFactorPatches[1]},
		{"scaffolds/auth-inertia-react/resources/js/pages/Dashboard.tsx", dashboardTwoFactorPatches[1]},
		{"scaffolds/auth-inertia-vue/resources/js/pages/Dashboard.vue", dashboardTwoFactorPatches[2]},
		{"scaffolds/auth-inertia-svelte/resources/js/pages/Dashboard.svelte", dashboardTwoFactorPatches[3]},
	}

	for _, tt := range tests {
		raw, err := scaffoldFS.ReadFile(tt.src)
		if os.IsNotExist(err) {
			continue // not every frontend ships every page
		}

		if err != nil {
			t.Fatal(err)
		}

		if rel, ok := strings.CutSuffix(tt.src, ".tmpl"); ok {
			if raw, err = renderScaffold(rel, raw, data); err != nil {
				t.Fatal(err)
			}
		}

		if !bytes.Contains(raw, []byte(tt.p.old)) {
			t.Errorf("%s no longer contains the text patched for %s:\n%s", tt.src, tt.p.file, tt.p.old)
		}
	}
}

func TestApplyPatchesKeepsCRLF(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	crlf := strings.ReplaceAll("\tcreds := x\n"+loginTwoFactorPatch.old+"\n\treturn nil\n", "\n", "\r\n")
	writeFiles(t, dir, map[string]string{loginTwoFactorPatch.file: crlf})

	env := &Env{Dir: dir, Stdout: &bytes.Buffer{}}
	for range 2 { // the second run finds the marker and changes nothing
		if err := applyPatches(env, loginTwoFactorPatch); err != nil {
			t.Fatal(err)
		}
	}

	got := read(t, dir, loginTwoFactorPatch.file)
	if !strings.Contains(got, "h.startTwoFactor(") || strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\n") {
		t.Errorf("patched file = %q", got)
	}

	if strings.Count(got, "h.startTwoFactor(") != 1 {
		t.Errorf("patched twice: %q", got)
	}
}

// install:auth --2fa refuses a login handler it cannot change: 2FA that
// can be turned on but is never asked for would be worse than none.
func TestSecurity_A07_TwoFactorFailsClosedOnEditedLogin(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"go.mod":                             "module example.com/app\n\ngo 1.27\n",
		"app/accounts/accounts.go":           "package accounts\n",
		"routes/auth.go":                     "package routes\n",
		"app/controllers/account/account.go": "package account\n\n// Login was rewritten by hand.\n",
	})

	code, out, errOut := vukem(t, dir, "install:auth", "--2fa")
	if code == 0 || !strings.Contains(errOut, "changed by hand") {
		t.Fatalf("install:auth --2fa = %d\n%s%s", code, out, errOut)
	}

	for _, rel := range []string{"routes/two_factor.go", "app/models/two_factor.go", "app/accounts/two_factor.go"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err == nil {
			t.Errorf("%s was written although the login could not be updated", rel)
		}
	}
}

// The secret and the recovery code hashes are orm.Encrypted columns, and
// codes are checked against the last used step under a row lock.
func TestSecurity_A07_TwoFactorScaffoldDefaults(t *testing.T) {
	t.Parallel()

	files := map[string][]string{
		"scaffolds/auth-2fa/app/models/two_factor.go.tmpl": {
			"TwoFactorSecret        orm.Encrypted[string]",
			"TwoFactorRecoveryCodes orm.Encrypted[[]string]",
		},
		"scaffolds/auth-2fa/app/accounts/two_factor.go.tmpl": {
			"tf.TwoFactorLastStep, totp.Options{}",
			"lockTwoFactor(ctx, tx, userID, a.now())",
			"totp.UseRecoveryCode(",
		},
		"scaffolds/auth-2fa/routes/two_factor.go.tmpl": {
			"h.Challenge).Use(auth.Guest(acc.Web), challenge)",
			"h.Confirm).Name(\"two-factor.confirm\").Use(required, verified, confirm)",
		},
	}

	for src, wants := range files {
		b, err := scaffoldFS.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}

		for _, want := range wants {
			if !bytes.Contains(b, []byte(want)) {
				t.Errorf("%s lacks %q", src, want)
			}
		}
	}
}

// New projects ship no secret and no credential: .env (with the generated
// APP_KEY) is git-ignored and .env.example leaves every key empty.
func TestSecurity_A02_StarterShipsNoSecrets(t *testing.T) {
	t.Parallel()

	ignore, err := starterFS.ReadFile("starter/gitignore")
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Contains(strings.Split(strings.ReplaceAll(string(ignore), "\r\n", "\n"), "\n"), ".env") {
		t.Errorf("starter .gitignore does not ignore .env:\n%s", ignore)
	}

	example, err := starterFS.ReadFile("starter/.env.example.tmpl")
	if err != nil {
		t.Fatal(err)
	}

	for line := range strings.SplitSeq(string(example), "\n") {
		k, v, _ := strings.Cut(strings.TrimSpace(line), "=")
		if strings.Contains(k, "KEY") || strings.Contains(k, "SECRET") || strings.Contains(k, "PASSWORD") {
			if v != "" {
				t.Errorf(".env.example sets %s=%q, want it empty", k, v)
			}
		}
	}
}

// The authentication scaffold keeps its secure defaults; the generated
// tests (TestRegisterAndVerifyEmail, TestLoginLogoutAndThrottle,
// TestPasswordReset) check the behaviour in TestNewProjectEndToEnd.
func TestSecurity_A07_AuthScaffoldDefaults(t *testing.T) {
	t.Parallel()

	files := map[string][]string{
		"scaffolds/auth-core/app/requests/auth.go.tmpl": {
			`"password":              z.String().Min(12).Max(256),`,
		},
		"scaffolds/auth-core/app/accounts/passwords.go.tmpl": {
			"subtle.ConstantTimeCompare([]byte(stored), []byte(digest(token)))",
			"a.expired(created, a.ResetTTL)",
			"DELETE FROM remember_tokens WHERE user_id",
			"tokens := auth.NewTokens(tx)",
			"tokens.RevokeAll(ctx, u.AuthID())",
		},
		"scaffolds/auth-web/app/controllers/account/account.go.tmpl": {
			"strings.ToLower(strings.TrimSpace(r.PostFormValue(\"email\")))",
		},
		"scaffolds/auth-inertia/app/controllers/account/account.go.tmpl": {
			"strings.ToLower(strings.TrimSpace(email))",
		},
		"scaffolds/auth-web/routes/auth.go.tmpl": {
			`r.Post("/login", h.Login).Use(guest, throttle, perAccount)`,
			`h.SendResetLink).Name("password.email").Use(guest, mails)`,
		},
		"scaffolds/auth-inertia/routes/auth.go.tmpl": {
			`r.Post("/login", h.Login).Use(guest, throttle, perAccount)`,
		},
	}

	for src, wants := range files {
		b, err := scaffoldFS.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}

		for _, want := range wants {
			if !bytes.Contains(b, []byte(want)) {
				t.Errorf("%s lacks %q", src, want)
			}
		}
	}
}

// Changing credentials ends the user's other sessions: a password reset
// ends all of them and enabling 2FA all but the current one, on every
// session driver through the user's session version (generated
// TestPasswordResetEndsOtherSessions, run by TestNewProjectEndToEnd). The
// login still regenerates the session id.
func TestSecurity_A07_AuthScaffoldRevokesOtherSessions(t *testing.T) {
	t.Parallel()

	revoke2FA := "h.a.RevokeOtherSessions(c.Context(), u.ID, c.Session().ID())"
	files := map[string][]string{
		"scaffolds/auth-core/app/accounts/accounts.go.tmpl": {
			"if err := a.bumpSessionVersion(ctx, a.DB, userID); err != nil {",
			`"DELETE FROM remember_tokens WHERE user_id = "`,
			"UPDATE users SET session_version = session_version + 1 WHERE id = ",
			"m.RevokeOthers(ctx, a.Web.SessionUserID(strconv.FormatInt(userID, 10)), keep)",
			"sessions:  app.Sessions,",
		},
		"scaffolds/auth-core/app/accounts/passwords.go.tmpl": {
			"if err := a.bumpSessionVersion(ctx, tx, u.ID); err != nil {",
			`return u, a.revokeServerSessions(ctx, u.ID, "")`,
		},
		"scaffolds/auth-core/app/models/user.go.tmpl": {
			"func (u User) AuthSessionVersion() int64 { return u.SessionVersion }",
		},
		"scaffolds/auth-core/database/migrations/TS1_create_users_table.go.tmpl": {
			`t.BigInteger("session_version").Default(0)`,
		},
		"scaffolds/auth-web/tests/sessions_test.go.tmpl": {
			`[]string{"database", "cookie"}`,
			`c.Get("/login").AssertOK() // signed out`,
		},
	}

	for src, wants := range files {
		b, err := scaffoldFS.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range wants {
			if !bytes.Contains(b, []byte(want)) {
				t.Errorf("%s lacks %q", src, want)
			}
		}
	}

	// Enabling 2FA flashes the one-time recovery codes before revoking, and
	// a revocation failure is logged instead of failing the request.
	for _, src := range []string{
		"scaffolds/auth-2fa-web/app/controllers/account/two_factor.go.tmpl",
		"scaffolds/auth-2fa-inertia/app/controllers/account/two_factor.go.tmpl",
	} {
		b, err := scaffoldFS.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}

		flash := bytes.Index(b, []byte("c.Session().Flash(recoveryCodesFlash, codes)"))
		revoke := bytes.Index(b, []byte("if err := "+revoke2FA+"; err != nil {\n\t\tweb.SecurityEvent("))
		refresh := bytes.Index(b, []byte("if err := h.a.Web.RefreshSession(c); err != nil {\n\t\tweb.SecurityEvent("))

		if flash < 0 || revoke < 0 || flash > revoke {
			t.Errorf("%s: recovery codes flashed at %d, revocation with logged failure at %d", src, flash, revoke)
		}

		// The session version bump would end the current session too.
		if refresh < revoke {
			t.Errorf("%s: current session refreshed at %d, before the revocation at %d", src, refresh, revoke)
		}
	}
}

// The password reset form answers alike for known and unknown emails and
// queues the mail, so the response time does not reveal accounts either;
// the generated TestPasswordResetAnswersAlike checks the behaviour in
// TestNewProjectEndToEnd / TestNewInertiaProjectEndToEnd.
func TestSecurity_A07_PasswordResetAnswersAlike(t *testing.T) {
	t.Parallel()

	src, err := scaffoldFS.ReadFile("scaffolds/auth-core/app/accounts/passwords.go.tmpl")
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Contains(src, []byte("a.Mailer.SendLater(ctx, mail.ResetPassword{")) ||
		bytes.Contains(src, []byte("a.Mailer.Send(ctx, mail.ResetPassword{")) {
		t.Error("SendResetLink does not queue the reset mail")
	}

	for _, kit := range []string{"auth-web", "auth-inertia"} {
		test, err := scaffoldFS.ReadFile("scaffolds/" + kit + "/tests/password_reset_test.go.tmpl")
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"func TestPasswordResetAnswersAlike(", "nobody@example.com", "vukem.MailJobKind"} {
			if !bytes.Contains(test, []byte(want)) {
				t.Errorf("%s password reset test lacks %q", kit, want)
			}
		}
	}
}

// Sign-up answers alike for new and existing emails: no validation error,
// no login before verification, the same redirect, and on both paths a
// password hash plus one queued mail (verification, or a notice to the
// owner limited to one per hour per address); the verification link works
// signed out and refuses another signed-in user. The generated
// TestRegisterDoesNotRevealAccounts checks the behaviour in
// TestNewProjectEndToEnd / TestNewInertiaProjectEndToEnd.
func TestSecurity_A07_SignupIsEnumerationSafe(t *testing.T) {
	t.Parallel()

	users, err := scaffoldFS.ReadFile("scaffolds/auth-core/app/accounts/users.go.tmpl")
	if err != nil {
		t.Fatal(err)
	}

	hash := bytes.Index(users, []byte("h, err := a.Hasher.Make(in.Password)"))
	lookup := bytes.Index(users, []byte(`Where("email", email).First(ctx)`))

	if hash < 0 || lookup < 0 || hash > lookup {
		t.Errorf("Register hashes at %d and looks the email up at %d: both paths must hash first", hash, lookup)
	}

	for _, want := range []string{
		"return false, a.notifyExisting(ctx, existing)",
		`c.Increment(ctx, "register:exists:"+EmailHash(u.Email), 1, time.Hour)`,
		"a.Mailer.SendLater(ctx, mail.AccountExists{",
		"a.Mailer.SendLater(ctx, mail.VerifyEmail{",
	} {
		if !bytes.Contains(users, []byte(want)) {
			t.Errorf("users.go lacks %q", want)
		}
	}

	if bytes.Contains(users, []byte("orm.Unique[models.User]")) {
		t.Error("Register still rejects existing emails with a validation error")
	}

	for _, kit := range []string{"auth-web", "auth-inertia"} {
		ctrl, err := scaffoldFS.ReadFile("scaffolds/" + kit + "/app/controllers/account/account.go.tmpl")
		if err != nil {
			t.Fatal(err)
		}

		start := bytes.Index(ctrl, []byte("func (h *Handlers) Register("))
		end := bytes.Index(ctrl[start:], []byte("\n}\n"))

		if start < 0 || end < 0 {
			t.Fatalf("%s: no Register handler", kit)
		}

		register := ctrl[start : start+end]
		if bytes.Contains(register, []byte("Web.Login(")) ||
			!bytes.Contains(register, []byte(`c.Redirect("/register/check-email")`)) {
			t.Errorf("%s Register logs in or does not redirect to /register/check-email:\n%s", kit, register)
		}

		routes, err := scaffoldFS.ReadFile("scaffolds/" + kit + "/routes/auth.go.tmpl")
		if err != nil {
			t.Fatal(err)
		}

		for _, want := range []string{
			`r.Get("/register/check-email", h.CheckEmail).Name("register.check").Use(guest)`,
			`r.Get("/email/verify/{id}", h.Verify).Name("verification.verify").Use(required)`,
		} {
			if !bytes.Contains(routes, []byte(want)) {
				t.Errorf("%s routes lack %q", kit, want)
			}
		}

		verify, err := scaffoldFS.ReadFile("scaffolds/" + kit + "/app/controllers/account/verification.go.tmpl")
		if err != nil {
			t.Fatal(err)
		}

		if !bytes.Contains(verify, []byte("if u.ID != id {")) {
			t.Errorf("%s Verify does not refuse another signed-in user", kit)
		}

		test, err := scaffoldFS.ReadFile("scaffolds/" + kit + "/tests/auth_test.go.tmpl")
		if err != nil {
			t.Fatal(err)
		}

		if !bytes.Contains(test, []byte("func TestRegisterDoesNotRevealAccounts(")) {
			t.Errorf("%s lacks the generated TestRegisterDoesNotRevealAccounts", kit)
		}
	}
}

// Pages behind a login also need a verified email, so an account someone
// pre-registered with another person's address is useless until that
// person verifies it (and a password reset then ends the squatter's
// sessions): the dashboard, 2FA settings, billing and the admin panel
// (admin by email) require auth.Verified, and API tokens are not issued to
// unverified users. The generated TestUnverifiedUsersSeeTheNotice and
// TestAPITokenNeedsVerifiedEmail check the behaviour end to end.
func TestSecurity_A07_AuthScaffoldRequiresVerifiedEmail(t *testing.T) {
	t.Parallel()

	files := map[string][]string{
		"scaffolds/auth-web/routes/auth.go.tmpl": {
			"verified := auth.Verified(acc.Web)",
			`r.Get("/dashboard", h.Dashboard).Name("dashboard").Use(required, verified)`,
		},
		"scaffolds/auth-inertia/routes/auth.go.tmpl": {
			"verified := auth.Verified(acc.Web)",
			`r.Get("/dashboard", h.Dashboard).Name("dashboard").Use(required, verified)`,
		},
		"scaffolds/auth-2fa/routes/two_factor.go.tmpl":       {"verified := auth.Verified(acc.Web)"},
		"scaffolds/billing/routes/billing.go.tmpl":           {"verified := auth.Verified(acc.Web)"},
		"scaffolds/admin/app/admin/admin.go.tmpl":            {"u.EmailVerified()"},
		"scaffolds/api/app/controllers/apiv1/tokens.go.tmpl": {"if !u.EmailVerified() {"},
		"scaffolds/auth-web/tests/auth_test.go.tmpl":         {"func TestUnverifiedUsersSeeTheNotice("},
		"scaffolds/auth-inertia/tests/auth_test.go.tmpl":     {"func TestUnverifiedUsersSeeTheNotice("},
		"scaffolds/api/tests/api_tokens_test.go.tmpl":        {"func TestAPITokenNeedsVerifiedEmail("},
	}

	for src, wants := range files {
		b, err := scaffoldFS.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}

		for _, want := range wants {
			if !bytes.Contains(b, []byte(want)) {
				t.Errorf("%s lacks %q", src, want)
			}
		}
	}

	// Every signed-in route of the 2FA and billing scaffolds needs it.
	for _, src := range []string{"scaffolds/auth-2fa/routes/two_factor.go.tmpl", "scaffolds/billing/routes/billing.go.tmpl"} {
		b, err := scaffoldFS.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}

		for line := range strings.Lines(string(b)) {
			if strings.Contains(line, "Use(required") && !strings.Contains(line, "Use(required, verified") {
				t.Errorf("%s: route without auth.Verified: %s", src, strings.TrimSpace(line))
			}
		}
	}
}

// POST /register is limited per IP and per address (a hash of it, so the
// limiter store holds no emails), through app.RateLimiters so
// RATE_LIMIT_STORE=cache shares the budget. The limit counts requests, not
// accounts, so new and existing addresses hit it alike (generated
// TestRegisterThrottle).
func TestSecurity_A07_SignupRateLimited(t *testing.T) {
	t.Parallel()

	for _, kit := range []string{"auth-web", "auth-inertia"} {
		routes, err := scaffoldFS.ReadFile("scaffolds/" + kit + "/routes/auth.go.tmpl")
		if err != nil {
			t.Fatal(err)
		}

		for _, want := range []string{
			`limiters("register", `,
			`limiters("register.email", `,
			`r.Post("/register", h.Register).Use(guest, signupIP, signupEmail)`,
		} {
			if !bytes.Contains(routes, []byte(want)) {
				t.Errorf("%s routes lack %q", kit, want)
			}
		}

		ctrl, err := scaffoldFS.ReadFile("scaffolds/" + kit + "/app/controllers/account/account.go.tmpl")
		if err != nil {
			t.Fatal(err)
		}

		if !bytes.Contains(ctrl, []byte("sha256.Sum256([]byte(loginEmail(r)))")) {
			t.Errorf("%s RegisterThrottleKey does not hash the address", kit)
		}

		test, err := scaffoldFS.ReadFile("scaffolds/" + kit + "/tests/auth_test.go.tmpl")
		if err != nil {
			t.Fatal(err)
		}

		for _, want := range []string{"func TestRegisterThrottle(", "func TestRegisterSurvivesCacheOutage("} {
			if !bytes.Contains(test, []byte(want)) {
				t.Errorf("%s lacks the generated %s", kit, want)
			}
		}
	}
}

// A failing cache on the existing-email path must not answer differently
// from the new-email path: the notice is skipped and the error logged,
// never returned (generated TestRegisterSurvivesCacheOutage).
func TestSecurity_A07_SignupCacheOutageIsNoOracle(t *testing.T) {
	t.Parallel()

	users, err := scaffoldFS.ReadFile("scaffolds/auth-core/app/accounts/users.go.tmpl")
	if err != nil {
		t.Fatal(err)
	}

	start := bytes.Index(users, []byte("func (a *Accounts) notifyExisting("))
	if start < 0 {
		t.Fatal("no notifyExisting")
	}

	body, _, ok := bytes.Cut(users[start:], []byte("\n}\n"))
	if !ok {
		t.Fatal("notifyExisting has no end")
	}

	if bytes.Count(body, []byte("a.log.ErrorContext(")) < 2 ||
		bytes.Contains(body, []byte("if err != nil {\n\t\treturn err")) {
		t.Errorf("notifyExisting returns cache errors instead of logging them:\n%s", body)
	}
}

// Proving the address ends what was opened before it was proven: the
// verification link (VerifyEmail) and a provider-verified social sign-in
// that links an unverified local account both delete remember-me and API
// tokens and bump the session version (ending every session on every
// driver) in one transaction, then delete server-side sessions. The social
// link also replaces the password, which may belong to whoever signed up
// first with the address. The verification link carries a hash of the
// address, so it stops working once the email changes. The generated
// TestVerificationEndsOtherSessions, TestVerificationLinkBoundToEmail and
// TestSocialLinkEndsSquatterAccess check the behaviour end to end.
func TestSecurity_A07_ProvingTheEmailEndsSquatterAccess(t *testing.T) {
	t.Parallel()

	files := map[string][]string{
		"scaffolds/auth-core/app/accounts/users.go.tmpl": {
			"func (a *Accounts) VerifyEmail(ctx context.Context, u *models.User, keep string) error {",
			"if err := a.revokeAccess(ctx, tx, u.ID); err != nil {",
			`"DELETE FROM remember_tokens WHERE user_id = "`,
			`"DELETE FROM access_tokens WHERE user_id = "`,
			"return a.bumpSessionVersion(ctx, ex, userID)",
			"return a.revokeServerSessions(ctx, u.ID, keep)",
			`"%s/email/verify/%d?email_hash=%s", a.URL, u.ID, EmailHash(u.Email)`,
		},
		"scaffolds/auth-social/app/accounts/social.go.tmpl": {
			"if !u.EmailVerified() {",
			"a.updatePassword(ctx, tx, u.ID, rand.Text()+rand.Text())",
			"a.revokeAccess(ctx, tx, u.ID)",
			`a.revokeServerSessions(ctx, u.ID, "")`,
		},
		"scaffolds/auth-social/tests/social_test.go.tmpl": {"func TestSocialLinkEndsSquatterAccess("},
	}

	for _, kit := range []string{"auth-web", "auth-inertia"} {
		files["scaffolds/"+kit+"/app/controllers/account/verification.go.tmpl"] = []string{
			`subtle.ConstantTimeCompare([]byte(c.Query("email_hash")), []byte(accounts.EmailHash(u.Email))) != 1`,
			"h.a.VerifyEmail(c.Context(), &u, c.Session().ID())",
			"h.a.Web.RefreshSession(c)",
		}
		files["scaffolds/"+kit+"/tests/auth_test.go.tmpl"] = []string{
			"func TestVerificationEndsOtherSessions(",
			"func TestVerificationLinkBoundToEmail(",
		}
	}

	for src, wants := range files {
		b, err := scaffoldFS.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}

		for _, want := range wants {
			if !bytes.Contains(b, []byte(want)) {
				t.Errorf("%s lacks %q", src, want)
			}
		}
	}

	// The link must not verify through the bare MarkVerified, which ends
	// nothing.
	for _, kit := range []string{"auth-web", "auth-inertia"} {
		b, err := scaffoldFS.ReadFile("scaffolds/" + kit + "/app/controllers/account/verification.go.tmpl")
		if err != nil {
			t.Fatal(err)
		}

		if bytes.Contains(b, []byte("h.a.MarkVerified(")) {
			t.Errorf("%s Verify calls MarkVerified instead of VerifyEmail", kit)
		}
	}
}

// The verification link needs the login of the account it verifies: the
// route runs auth.Required, so a signed-out click leads to the login page
// with the link kept as the intended URL, and the handler checks the
// signature (an expired link leads to the resend page, a bad one is a 403
// and a security event) and verifies only the signed-in user (403 for
// another account). Whoever merely owns the mailbox cannot verify an account
// someone else created with the address. The generated
// TestRegisterAndVerifyEmail and TestVerificationEndsOtherSessions check
// the behaviour end to end.
func TestSecurity_A07_VerificationLinkRequiresLogin(t *testing.T) {
	t.Parallel()

	for _, kit := range []string{"auth-web", "auth-inertia"} {
		routes, err := scaffoldFS.ReadFile("scaffolds/" + kit + "/routes/auth.go.tmpl")
		if err != nil {
			t.Fatal(err)
		}

		if !bytes.Contains(
			routes,
			[]byte(`h.Verify).Name("verification.verify").Use(required)`),
		) {
			t.Errorf("%s: the verification route lacks auth.Required", kit)
		}

		verify, err := scaffoldFS.ReadFile("scaffolds/" + kit + "/app/controllers/account/verification.go.tmpl")
		if err != nil {
			t.Fatal(err)
		}

		for _, want := range []string{
			`u, err := auth.User[models.User](c, "web")`,
			"if u.ID != id {",
			"if err := h.a.Signer.Verify(c.Request()); err != nil {",
			`web.SecurityEvent(c.Request(), "url.signature_invalid")`,
		} {
			if !bytes.Contains(verify, []byte(want)) {
				t.Errorf("%s Verify lacks %q", kit, want)
			}
		}

		// Loading the link's user by id would let a signed-out click verify it.
		if bytes.Contains(verify, []byte("orm.QueryOf[models.User]")) {
			t.Errorf("%s Verify loads the link's user instead of the signed-in one", kit)
		}

		tests, err := scaffoldFS.ReadFile("scaffolds/" + kit + "/tests/auth_test.go.tmpl")
		if err != nil {
			t.Fatal(err)
		}

		if !bytes.Contains(tests, []byte("AssertRedirect(verify)")) ||
			!bytes.Contains(tests, []byte("func TestExpiredVerificationLinkOffersANewOne(")) {
			t.Errorf("%s tests do not follow the link back after the login", kit)
		}
	}
}

// The verification mail is queued like the reset mail, so registering does
// not wait for the mail server (and a slow server cannot time out sign-up).
func TestVerificationMailIsQueued(t *testing.T) {
	t.Parallel()

	src, err := scaffoldFS.ReadFile("scaffolds/auth-core/app/accounts/users.go.tmpl")
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Contains(src, []byte("a.Mailer.SendLater(ctx, mail.VerifyEmail{")) ||
		bytes.Contains(src, []byte("a.Mailer.Send(ctx, mail.VerifyEmail{")) {
		t.Error("SendVerification does not queue the verification mail")
	}
}
