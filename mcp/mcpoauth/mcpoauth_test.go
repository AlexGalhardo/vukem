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

	store := oauth.NewMemoryStore()
	if err := store.CreateGrant(t.Context(), oauth.Grant{
		ID: "g1", ClientID: "app", Subject: "u1", Scopes: []string{"read", "write"}, Resource: resource,
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	as, err := oauth.New(oauth.Config{
		Issuer: issuer, Keys: []jwt.Key{jwt.ES256("k1", key)}, Store: store,
		Clients: oauth.StaticClients{}, Consent: noConsent{}, Resources: []string{resource},
		CSRFKey: bytes.Repeat([]byte{1}, 32),
	})
	if err != nil {
		t.Fatal(err)
	}

	v := mcpoauth.Verifier(as)

	sign := func(aud string) string {
		tok, err := atSign(jwt.ES256("k1", key), jwt.Claims{
			Issuer: issuer, Subject: "u1", Audience: []string{aud}, ExpiresAt: time.Now().Add(time.Minute),
			Extra: map[string]any{"scope": "read write", "client_id": "app", "gid": "g1"},
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

	live := sign(resource)
	if err := store.RevokeGrant(t.Context(), "g1"); err != nil {
		t.Fatal(err)
	}

	if _, err := v.VerifyAccessToken(t.Context(), live, resource); !errors.Is(err, mcp.ErrInvalidToken) {
		t.Errorf("revoked grant: err = %v, want mcp.ErrInvalidToken", err)
	}
}

// atSign signs like the server does: an RFC 9068 access token.
func atSign(k jwt.Key, c jwt.Claims) (string, error) { return jwt.SignTyped(k, "at+jwt", c) }
