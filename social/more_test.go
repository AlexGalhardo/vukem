package social

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/auth/jwt"
	"github.com/AlexGalhardo/vukem/web"
)

func TestXFlowUsesBasicAuth(t *testing.T) {
	t.Parallel()

	f := newFakeServer(t)
	f.profile = map[string]any{"id": "123", "name": "Ana", "username": "ana", "profile_image_url": "https://img/a.png"}

	u, err := flow(t, New(testKey, X(cfg(), WithEndpoints(oauthEndpoints(f)))), f, "x", nil)
	if err != nil {
		t.Fatal(err)
	}

	if u.ID != "123" || u.Nickname != "ana" || u.Email != "" || u.EmailVerified {
		t.Errorf("user = %+v", u)
	}

	if p := X(cfg()); !strings.Contains(mustAuthURL(t, p), "scope=tweet.read+users.read") {
		t.Error("X scopes missing")
	}
}

func TestLinkedInIsOIDC(t *testing.T) {
	t.Parallel()

	if p := LinkedIn(cfg()); p.Name() != "linkedin" || p.issuer != "https://www.linkedin.com/oauth" {
		t.Errorf("LinkedIn = %s %s", p.Name(), p.issuer)
	}
}

func mustAuthURL(t *testing.T, p *OAuth2Provider) string {
	t.Helper()

	u, err := p.AuthURL(t.Context(), nil, "st", "ch", "")
	if err != nil {
		t.Fatal(err)
	}

	return u
}

func appleKey(t *testing.T) (*ecdsa.PrivateKey, []byte) {
	t.Helper()

	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}

	return k, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

func TestAppleFormPostFlow(t *testing.T) {
	t.Parallel()

	f := newFakeServer(t)
	key, p8 := appleKey(t)

	f.claims = map[string]any{"email": "ana@privaterelay.appleid.com", "email_verified": "true", "name": ""}
	f.checkSecret = func(secret string) bool {
		v := jwt.NewVerifier(jwt.StaticKeys(jwt.Key{ID: "KEY1", Alg: jwt.AlgES256, Public: &key.PublicKey}),
			jwt.VerifyOptions{Issuer: "TEAM1", Audience: f.URL})

		c, err := v.Verify(t.Context(), secret)

		return err == nil && c.Subject == "client"
	}

	apple, err := Apple(AppleConfig{Config: cfg(), TeamID: "TEAM1", KeyID: "KEY1", PrivateKey: p8, Issuer: f.URL})
	if err != nil {
		t.Fatal(err)
	}

	s := New(testKey, apple)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/apple/redirect", http.NoBody)

	if err := s.Redirect(web.NewCtx(rec, req, nil), "apple"); err != nil {
		t.Fatal(err)
	}

	cookie := rec.Result().Cookies()[0]
	if cookie.SameSite != http.SameSiteNoneMode || !cookie.Secure || !cookie.HttpOnly {
		t.Fatalf("state cookie = %+v, want SameSite=None; Secure", cookie)
	}

	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "response_mode=form_post") || !strings.Contains(loc, "scope=name+email") {
		t.Errorf("authorize URL = %s", loc)
	}

	code, state := f.authorize(t, loc)

	form := url.Values{"code": {code}, "state": {state}, "user": {`{"name":{"firstName":"Ana","lastName":"Silva"}}`}}
	cb := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/auth/apple/callback",
		strings.NewReader(form.Encode()),
	)
	cb.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	cb.AddCookie(cookie)

	u, err := s.User(web.NewCtx(httptest.NewRecorder(), cb.WithContext(web.WithServices(cb.Context(),
		&web.Services{Log: slog.New(slog.DiscardHandler)})), nil), "apple")
	if err != nil {
		t.Fatal(err)
	}

	if u.Name != "Ana Silva" || !u.EmailVerified || u.Email != "ana@privaterelay.appleid.com" {
		t.Errorf("apple user = %+v", u)
	}
}

func TestAppleNeedsHTTPSAndAKey(t *testing.T) {
	t.Parallel()

	if _, err := Apple(AppleConfig{Config: cfg(), TeamID: "T", KeyID: "K", PrivateKey: []byte("nope")}); err == nil {
		t.Error("bad key accepted")
	}

	_, p8 := appleKey(t)

	if _, err := Apple(AppleConfig{Config: cfg(), KeyID: "K", PrivateKey: p8}); err == nil {
		t.Error("missing team id accepted")
	}

	apple, err := Apple(AppleConfig{Config: cfg(), TeamID: "T", KeyID: "K", PrivateKey: p8, Issuer: "https://idp.test"})
	if err != nil {
		t.Fatal(err)
	}

	s := New(testKey, apple)
	s.InsecureCookies = true

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/apple/redirect", http.NoBody)
	if err := s.Redirect(web.NewCtx(httptest.NewRecorder(), req, nil), "apple"); err == nil {
		t.Error("form_post provider allowed without Secure cookies")
	}

	// The client secret JWT is reused within its lifetime.
	now := time.Now()
	a, _ := apple.secret(now)
	b, _ := apple.secret(now.Add(10 * time.Minute))
	c, _ := apple.secret(now.Add(2 * time.Hour))

	if a != b || a == c || errors.Is(nil, jwt.ErrInvalid) {
		t.Error("client secret cache behaves unexpectedly")
	}
}
