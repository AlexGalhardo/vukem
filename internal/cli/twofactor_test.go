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
			"h.Confirm).Name(\"two-factor.confirm\").Use(required, confirm)",
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
			"DELETE FROM access_tokens WHERE user_id",
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
		`c.Increment(ctx, "register:exists:"+hex.EncodeToString(sum[:]), 1, time.Hour)`,
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
			`r.Get("/email/verify/{id}", h.Verify).Name("verification.verify").Use(acc.Signer.Middleware())`,
		} {
			if !bytes.Contains(routes, []byte(want)) {
				t.Errorf("%s routes lack %q", kit, want)
			}
		}

		verify, err := scaffoldFS.ReadFile("scaffolds/" + kit + "/app/controllers/account/verification.go.tmpl")
		if err != nil {
			t.Fatal(err)
		}

		if !bytes.Contains(verify, []byte("signedIn && current.ID != u.ID")) {
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
