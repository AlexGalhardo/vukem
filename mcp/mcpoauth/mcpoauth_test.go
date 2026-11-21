package mcpoauth_test

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/auth/jwt"
	"github.com/AlexGalhardo/vukem/mcp"
	"github.com/AlexGalhardo/vukem/mcp/mcpoauth"
	"github.com/AlexGalhardo/vukem/oauth"
)

type noConsent struct{}

func (noConsent) Subject(http.ResponseWriter, *http.Request) (string, bool) { return "", false }

func (noConsent) Render(http.ResponseWriter, *http.Request, oauth.ConsentRequest) error { return nil }

func TestVerifierAdaptsTheAuthorizationServer(t *testing.T) {
	t.Parallel()

	const issuer, resource = "https://auth.example.com", "https://api.example.com/mcp"

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	as, err := oauth.New(oauth.Config{
		Issuer: issuer, Keys: []jwt.Key{jwt.ES256("k1", key)}, Store: oauth.NewMemoryStore(),
		Clients: oauth.StaticClients{}, Consent: noConsent{}, Resources: []string{resource},
		CSRFKey: bytes.Repeat([]byte{1}, 32),
	})
	if err != nil {
		t.Fatal(err)
	}

	v := mcpoauth.Verifier(as)

	sign := func(aud string) string {
		tok, err := jwt.Sign(jwt.ES256("k1", key), jwt.Claims{
			Issuer: issuer, Subject: "u1", Audience: []string{aud}, ExpiresAt: time.Now().Add(time.Minute),
			Extra: map[string]any{"scope": "read write", "client_id": "app"},
		})
		if err != nil {
			t.Fatal(err)
		}

		return tok
	}

	info, err := v.VerifyAccessToken(t.Context(), sign(resource), resource)
	if err != nil || info.Subject != "u1" || info.ClientID != "app" || len(info.Scopes) != 2 ||
		info.Resource != resource {
		t.Fatalf("VerifyAccessToken = %+v, %v", info, err)
	}

	for name, tok := range map[string]string{"another audience": sign("https://other.example.com"), "garbage": "x.y.z"} {
		if _, err := v.VerifyAccessToken(t.Context(), tok, resource); !errors.Is(err, mcp.ErrInvalidToken) {
			t.Errorf("%s: err = %v, want mcp.ErrInvalidToken", name, err)
		}
	}
}
