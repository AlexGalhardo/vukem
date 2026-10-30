package cli

import (
	"strings"
	"testing"
)

// The passkey scaffold pulls each challenge out of the session before
// verifying (single use), asks for the current password before adding or
// removing a passkey (the password stays the recovery path), throttles
// every endpoint, scopes deletion to the signed-in user and derives the
// relying party from APP_URL with a boot-time Validate. The generated
// TestSecurity_A07_Passkey* tests check the behaviour end to end.
func TestSecurity_A07_PasskeyScaffoldDefaults(t *testing.T) {
	t.Parallel()

	files := map[string][]string{
		"scaffolds/auth-passkeys/app/accounts/passkeys.go.tmpl": {
			`c.Session().Pull(passkeyRegisterKey).(string)`,
			`c.Session().Pull(passkeyLoginKey).(string)`,
			`rp.Validate()`,
			`Where("id", id).Where("user_id", userID).Delete(ctx)`,
			`Where("sign_count", pk.SignCount)`,
		},
		"scaffolds/auth-passkeys/app/controllers/account/passkeys.go.tmpl": {
			`u, err := h.confirmPassword(c, "register")`,
			`u, err := h.confirmPassword(c, "delete")`,
		},
		"scaffolds/auth-passkeys/routes/passkeys.go.tmpl": {
			`h.RegistrationOptions).Name("passkeys.options").Use(required, verified, confirm)`,
			`h.Register).Name("passkeys.store").Use(required, verified)`,
			`h.Delete).Name("passkeys.destroy").Use(required, verified, confirm)`,
			`h.LoginOptions).Name("passkeys.login.options").Use(guest, login)`,
			`h.Login).Name("passkeys.login").Use(guest, login)`,
		},
		"scaffolds/auth-passkeys/tests/passkeys_test.go.tmpl": {
			"func TestSecurity_A07_PasskeyChallengeIsSingleUse(",
			"func TestSecurity_A07_PasskeyIsScopedToItsUser(",
			"func TestSecurity_A07_PasskeyChangesNeedThePassword(",
		},
	}

	squeeze := func(s string) string { return strings.Join(strings.Fields(s), "") }

	for src, wants := range files {
		b, err := scaffoldFS.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}

		got := squeeze(string(b))

		for _, want := range wants {
			if !strings.Contains(got, squeeze(want)) {
				t.Errorf("%s lacks %q", src, want)
			}
		}
	}
}
