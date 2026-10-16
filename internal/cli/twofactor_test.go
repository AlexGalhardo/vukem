package cli

import (
	"bytes"
	"os"
	"path/filepath"
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
