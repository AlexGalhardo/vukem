package cloudflare_test

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/auth/jwt"
	"github.com/AlexGalhardo/vukem/contrib/cloudflare"
)

const testAUD = "4714c1358e65fe4b408ad6d432a5f878f08194bdb4752441fd56faefa9b2b6f2"

// accessFixture serves a team's certs over TLS and returns its domain,
// a signing key, and an Access validator.
func accessFixture(t *testing.T) (string, jwt.Key, *cloudflare.Access) {
	t.Helper()

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	key := jwt.RS256("k1", priv)

	set, err := jwt.PublicJWKS(key)
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cdn-cgi/access/certs" {
			http.NotFound(w, r)
			return
		}

		_ = json.NewEncoder(w).Encode(set)
	}))
	t.Cleanup(srv.Close)

	a, err := cloudflare.NewAccess(
		cloudflare.AccessOptions{TeamDomain: srv.URL, Audience: testAUD, Client: srv.Client()},
	)
	if err != nil {
		t.Fatal(err)
	}

	return srv.URL, key, a
}

func accessReq(t *testing.T, a *cloudflare.Access, token string) (int, string) {
	t.Helper()

	var email string

	h := a.Middleware()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		c, _ := cloudflare.AccessClaimsFrom(r.Context())
		email, _ = c.Extra["email"].(string)
	}))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin", http.NoBody)
	if token != "" {
		req.Header.Set(cloudflare.AccessHeader, token)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec.Code, email
}

func TestSecurity_A07_AccessJWTRejected(t *testing.T) {
	t.Parallel()

	team, key, a := accessFixture(t)

	otherPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	valid := jwt.Claims{
		Issuer: team, Audience: []string{testAUD}, Subject: "u1",
		ExpiresAt: time.Now().Add(time.Hour), IssuedAt: time.Now(), Extra: map[string]any{"email": "ana@example.com"},
	}

	sign := func(k jwt.Key, mutate func(*jwt.Claims)) string {
		c := valid
		if mutate != nil {
			mutate(&c)
		}

		tok, err := jwt.Sign(k, c)
		if err != nil {
			t.Fatal(err)
		}

		return tok
	}

	if code, email := accessReq(t, a, sign(key, nil)); code != http.StatusOK || email != "ana@example.com" {
		t.Fatalf("valid token = %d, %q; want 200, ana@example.com", code, email)
	}

	tests := map[string]string{
		"missing":        "",
		"garbage":        "not.a.jwt",
		"wrong audience": sign(key, func(c *jwt.Claims) { c.Audience = []string{"other-app"} }),
		"wrong issuer":   sign(key, func(c *jwt.Claims) { c.Issuer = "https://evil.cloudflareaccess.com" }),
		"expired":        sign(key, func(c *jwt.Claims) { c.ExpiresAt = time.Now().Add(-time.Hour) }),
		"foreign key":    sign(jwt.RS256("k1", otherPriv), nil),
	}

	for name, tok := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if code, _ := accessReq(t, a, tok); code != http.StatusForbidden {
				t.Errorf("Middleware() = %d, want 403", code)
			}
		})
	}
}

// Keys that cannot be fetched deny every token.
func TestSecurity_A07_AccessFailsClosedWithoutKeys(t *testing.T) {
	t.Parallel()

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)

	a, err := cloudflare.NewAccess(
		cloudflare.AccessOptions{TeamDomain: srv.URL, Audience: testAUD, Client: srv.Client()},
	)
	if err != nil {
		t.Fatal(err)
	}

	priv, _ := rsa.GenerateKey(rand.Reader, 2048)

	tok, err := jwt.Sign(jwt.RS256("k1", priv), jwt.Claims{
		Issuer: srv.URL, Audience: []string{testAUD}, ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}

	if code, _ := accessReq(t, a, tok); code != http.StatusForbidden {
		t.Errorf("Middleware(keys down) = %d, want 403", code)
	}
}

func TestNewAccessRefusesUnsafeConfig(t *testing.T) {
	t.Parallel()

	for _, o := range []cloudflare.AccessOptions{
		{TeamDomain: "https://team.cloudflareaccess.com"},
		{TeamDomain: "http://team.cloudflareaccess.com", Audience: testAUD},
		{TeamDomain: "team.cloudflareaccess.com", Audience: testAUD},
		{TeamDomain: "https://team.cloudflareaccess.com/x", Audience: testAUD},
	} {
		if _, err := cloudflare.NewAccess(o); err == nil {
			t.Errorf("NewAccess(%+v) = nil error", o)
		}
	}
}
