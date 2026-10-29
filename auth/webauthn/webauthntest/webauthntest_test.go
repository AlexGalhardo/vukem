package webauthntest_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/AlexGalhardo/vukem/auth/webauthn"
	"github.com/AlexGalhardo/vukem/auth/webauthn/webauthntest"
)

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()

	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}

	return b
}

func TestAuthenticatorPassesTheVerifier(t *testing.T) {
	t.Parallel()

	rp := webauthn.RelyingParty{ID: "localhost", Name: "Test", Origins: []string{"http://localhost:3000"}}
	user := webauthn.User{Handle: webauthn.NewUserHandle(), Name: "ada@example.com", DisplayName: "Ada"}
	a := webauthntest.New("http://localhost:3000")

	ch := webauthn.NewChallenge()
	opts := mustJSON(t, rp.RegistrationOptions(user, ch, nil))

	body, err := a.Create(opts)
	if err != nil {
		t.Fatal(err)
	}

	cred, err := rp.VerifyRegistration(ch, user, body)
	if err != nil {
		t.Fatalf("VerifyRegistration() = %v", err)
	}

	for want := uint32(1); want <= 2; want++ {
		ch = webauthn.NewChallenge()
		opts = mustJSON(t, rp.AssertionOptions(ch, nil))

		body, err = a.Get(opts)
		if err != nil {
			t.Fatal(err)
		}

		as, err := webauthn.ParseAssertion(body)
		if err != nil {
			t.Fatal(err)
		}

		cred, err = rp.VerifyAssertion(ch, cred, as)
		if err != nil || cred.SignCount != want {
			t.Fatalf("VerifyAssertion() = %d, %v; want sign count %d", cred.SignCount, err, want)
		}
	}

	other := mustJSON(t, webauthn.RelyingParty{ID: "example.com"}.AssertionOptions(ch, nil))
	if _, err := a.Get(other); !errors.Is(err, webauthntest.ErrNoCredential) {
		t.Fatalf("Get(other RP) = %v, want ErrNoCredential", err)
	}
}
