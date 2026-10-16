package social

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/web"
)

var testKey = []byte("0123456789abcdef0123456789abcdef")

// fakeServer is an OAuth2 + OIDC provider: it records the PKCE challenge
// of each authorization, enforces it on the token endpoint and signs ID
// tokens with an RSA or EC key.
type fakeServer struct {
	*httptest.Server

	mu         sync.Mutex
	challenges map[string]string // code -> challenge
	nonces     map[string]string // code -> nonce
	rsaKey     *rsa.PrivateKey
	ecKey      *ecdsa.PrivateKey
	alg        string
	claims     map[string]any // overrides for the next ID token
	tamper     bool           // flip a signature bit
	kid        string         // override the key id
	profile    map[string]any
	emails     []map[string]any
	// checkSecret validates the client secret (default: "secret").
	checkSecret func(string) bool
}

func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()

	rk, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	ek, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	f := &fakeServer{challenges: map[string]string{}, nonces: map[string]string{}, rsaKey: rk, ecKey: ek, alg: "RS256"}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)

	return f
}

// authorize simulates the user approving: it returns a code bound to the
// challenge and nonce of the authorization URL.
func (f *fakeServer) authorize(t *testing.T, authURL string) (code, state string) {
	t.Helper()

	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}

	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || len(q.Get("code_challenge")) != 43 {
		t.Fatalf("PKCE missing or not S256: %s", authURL)
	}

	code = "code-" + q.Get("state")[:8]

	f.mu.Lock()
	f.challenges[code] = q.Get("code_challenge")
	f.nonces[code] = q.Get("nonce")
	f.mu.Unlock()

	return code, q.Get("state")
}

func (f *fakeServer) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	enc := json.NewEncoder(w)

	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		send(enc, map[string]any{
			"issuer": f.URL, "authorization_endpoint": f.URL + "/authorize", "token_endpoint": f.URL + "/token",
			"userinfo_endpoint": f.URL + "/userinfo", "jwks_uri": f.URL + "/jwks",
		})
	case "/jwks":
		send(enc, map[string]any{"keys": []any{
			map[string]any{
				"kty": "RSA",
				"kid": "rsa1",
				"n":   b64(f.rsaKey.N.Bytes()),
				"e":   b64(big.NewInt(int64(f.rsaKey.E)).Bytes()),
			},
			map[string]any{
				"kty": "EC",
				"kid": "ec1",
				"crv": "P-256",
				"x":   b64(f.ecKey.X.Bytes()),
				"y":   b64(f.ecKey.Y.Bytes()),
			},
			map[string]any{"kty": "oct", "kid": "ignored"},
		}})
	case "/token":
		_ = r.ParseForm()
		code := r.PostForm.Get("code")

		f.mu.Lock()
		want, ok := f.challenges[code]
		nonce := f.nonces[code]
		delete(f.challenges, code) // codes are single use
		f.mu.Unlock()

		secret := r.PostForm.Get("client_secret")
		if id, s, basic := r.BasicAuth(); basic && id == "client" {
			secret = s
		}

		goodSecret := secret == "secret"
		if f.checkSecret != nil {
			goodSecret = f.checkSecret(secret)
		}

		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if !ok || b64(sum[:]) != want || !goodSecret {
			w.WriteHeader(http.StatusBadRequest)
			send(enc, map[string]any{"error": "invalid_grant"})

			return
		}

		send(enc, map[string]any{
			"access_token": "access-" + code, "refresh_token": "refresh", "expires_in": 3600, "scope": "openid email",
			"id_token": f.idToken(nonce),
		})
	case "/userinfo", "/api/user", "/api/users/@me":
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer access-") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		send(enc, f.profile)
	case "/api/users/me":
		send(enc, map[string]any{"data": f.profile})
	case "/api/user/emails":
		send(enc, f.emails)
	default:
		http.NotFound(w, r)
	}
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func (f *fakeServer) idToken(nonce string) string {
	f.mu.Lock()
	defer f.mu.Unlock()

	claims := map[string]any{
		"iss": f.URL, "aud": "client", "sub": "oidc-123", "email": "ana@example.com", "email_verified": true,
		"name": "Ana", "picture": "https://example.com/a.png", "nonce": nonce,
		"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
	}
	for k, v := range f.claims {
		if v == nil {
			delete(claims, k)
		} else {
			claims[k] = v
		}
	}

	kid := map[string]string{"RS256": "rsa1", "ES256": "ec1"}[f.alg]
	if f.kid != "" {
		kid = f.kid
	}
	header := b64(mustJSON(map[string]any{"alg": f.alg, "kid": kid}))
	payload := b64(mustJSON(claims))
	digest := sha256.Sum256([]byte(header + "." + payload))

	var sig []byte

	switch f.alg {
	case "RS256":
		sig, _ = rsa.SignPKCS1v15(rand.Reader, f.rsaKey, crypto.SHA256, digest[:])
	case "ES256":
		r, s, _ := ecdsa.Sign(rand.Reader, f.ecKey, digest[:])
		sig = append(pad32(r.Bytes()), pad32(s.Bytes())...)
	case "none":
		sig = nil
	}

	if f.tamper && len(sig) > 0 {
		sig[len(sig)-1] ^= 1
	}

	return header + "." + payload + "." + b64(sig)
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v) //nolint:errchkjson // test data
	return b
}

// flow runs redirect + callback through web.Ctx and returns the user.
func flow(
	t *testing.T,
	s *Social,
	f *fakeServer,
	provider string,
	tweak func(q url.Values, cookie *http.Cookie),
) (User, error) {
	t.Helper()

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/"+provider+"/redirect", http.NoBody)

	if err := s.Redirect(web.NewCtx(rec, req, nil), provider); err != nil {
		t.Fatal(err)
	}

	if rec.Code != http.StatusFound {
		t.Fatalf("redirect status = %d", rec.Code)
	}

	cookie := rec.Result().Cookies()[0]
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("state cookie flags = %+v", cookie)
	}

	code, state := f.authorize(t, rec.Header().Get("Location"))

	q := url.Values{"code": {code}, "state": {state}}
	if tweak != nil {
		tweak(q, cookie)
	}

	cb := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/auth/"+provider+"/callback?"+q.Encode(),
		http.NoBody,
	)
	cb.AddCookie(cookie)

	return s.User(web.NewCtx(httptest.NewRecorder(), cb.WithContext(web.WithServices(cb.Context(),
		&web.Services{Log: slog.New(slog.DiscardHandler)})), nil), provider)
}

func cfg() Config {
	return Config{ClientID: "client", ClientSecret: "secret", RedirectURL: "https://app.test/cb"}
}

func oauthEndpoints(f *fakeServer) Endpoints {
	return Endpoints{Auth: f.URL + "/authorize", Token: f.URL + "/token", API: f.URL + "/api"}
}

func TestGitHubFlow(t *testing.T) {
	t.Parallel()

	f := newFakeServer(t)
	f.profile = map[string]any{
		"id":         1234567890.0,
		"login":      "ana",
		"name":       "Ana",
		"avatar_url": "https://a/x.png",
		"email":      "public@x.io",
	}
	f.emails = []map[string]any{
		{"email": "old@x.io", "primary": false, "verified": true},
		{"email": "ana@x.io", "primary": true, "verified": true},
	}

	s := New(testKey, GitHub(cfg(), WithEndpoints(oauthEndpoints(f))))

	u, err := flow(t, s, f, "github", nil)
	if err != nil {
		t.Fatal(err)
	}

	want := User{
		Provider: "github", ID: "1234567890", Email: "ana@x.io", EmailVerified: true, Name: "Ana",
		Nickname: "ana", AvatarURL: "https://a/x.png",
	}
	if diff := cmp.Diff(want, u, cmpUser); diff != "" {
		t.Fatalf("user (-want +got):\n%s", diff)
	}

	if u.Token.Access == "" || u.Token.Refresh != "refresh" || u.Token.Expiry.IsZero() || len(u.Token.Scopes) != 2 {
		t.Fatalf("token = %+v", u.Token)
	}

	// Without a verified primary email the public one is not trusted.
	f.emails = nil
	if u, _ := flow(t, s, f, "github", nil); u.Email != "public@x.io" || u.EmailVerified {
		t.Fatalf("unverified email = %+v", u)
	}
}

var cmpUser = cmp.FilterPath(func(p cmp.Path) bool {
	last := p.Last().String()
	return last == ".Token" || last == ".Raw"
}, cmp.Ignore())

func TestGitLabAndDiscordMapping(t *testing.T) {
	t.Parallel()

	f := newFakeServer(t)

	f.profile = map[string]any{
		"id": 42.0, "username": "ana", "name": "Ana", "email": "ana@gl.io",
		"confirmed_at": "2026-01-01T00:00:00Z", "avatar_url": "https://gl/a.png",
	}
	s := New(testKey, GitLab(cfg(), WithEndpoints(oauthEndpoints(f))))

	u, err := flow(t, s, f, "gitlab", nil)
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(User{
		Provider: "gitlab", ID: "42", Email: "ana@gl.io", EmailVerified: true, Name: "Ana",
		Nickname: "ana", AvatarURL: "https://gl/a.png",
	}, u, cmpUser); diff != "" {
		t.Fatalf("gitlab (-want +got):\n%s", diff)
	}

	f.profile = map[string]any{
		"id": "80351110224678912", "username": "nelly", "global_name": nil,
		"email": "nelly@d.io", "verified": false, "avatar": "8342729096ea3675442027381ff50dfe",
	}
	s = New(testKey, Discord(cfg(), WithEndpoints(oauthEndpoints(f))))

	u, err = flow(t, s, f, "discord", nil)
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(User{
		Provider:  "discord",
		ID:        "80351110224678912",
		Email:     "nelly@d.io",
		Name:      "nelly",
		Nickname:  "nelly",
		AvatarURL: "https://cdn.discordapp.com/avatars/80351110224678912/8342729096ea3675442027381ff50dfe.png",
	},
		u, cmpUser); diff != "" {
		t.Fatalf("discord (-want +got):\n%s", diff)
	}

	// Generic OAuth2 with a custom mapper; an empty id is an error.
	generic := OAuth2("acme", cfg(), oauthEndpoints(f), []string{"profile"},
		func(_ context.Context, _ *http.Client, _ Endpoints, _ Token) (User, error) { return User{}, nil })
	if _, err := flow(t, New(testKey, generic), f, "acme", nil); err == nil {
		t.Fatal("user without id accepted")
	}
}

func TestOIDCFlows(t *testing.T) {
	t.Parallel()

	for _, alg := range []string{"RS256", "ES256"} {
		t.Run(alg, func(t *testing.T) {
			t.Parallel()

			f := newFakeServer(t)
			f.alg = alg

			u, err := flow(t, New(testKey, OIDC("acme", f.URL, cfg())), f, "acme", nil)
			if err != nil {
				t.Fatal(err)
			}

			if diff := cmp.Diff(User{
				Provider: "acme", ID: "oidc-123", Email: "ana@example.com", EmailVerified: true,
				Name: "Ana", AvatarURL: "https://example.com/a.png",
			}, u, cmpUser); diff != "" {
				t.Fatalf("user (-want +got):\n%s", diff)
			}
		})
	}
}

func TestSecurity_A07_IDTokenChecks(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		alg    string
		claims map[string]any
	}{
		"expired":          {"RS256", map[string]any{"exp": time.Now().Add(-time.Hour).Unix()}},
		"no exp":           {"RS256", map[string]any{"exp": nil}},
		"future iat":       {"RS256", map[string]any{"iat": time.Now().Add(time.Hour).Unix()}},
		"wrong audience":   {"RS256", map[string]any{"aud": "someone-else"}},
		"multi aud no azp": {"RS256", map[string]any{"aud": []any{"client", "other"}}},
		"wrong issuer":     {"RS256", map[string]any{"iss": "https://evil.example"}},
		"wrong nonce":      {"RS256", map[string]any{"nonce": "replayed"}},
		"alg none":         {"none", nil},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFakeServer(t)
			f.alg, f.claims = tc.alg, tc.claims

			_, err := flow(t, New(testKey, OIDC("acme", f.URL, cfg())), f, "acme", nil)
			if !errors.Is(err, ErrIDToken) || errs.Status(err) != http.StatusBadGateway {
				t.Fatalf("err = %v", err)
			}
		})
	}

	// A token whose signature was altered fails, for both algorithms.
	for _, alg := range []string{"RS256", "ES256"} {
		f := newFakeServer(t)
		f.alg, f.tamper = alg, true

		if _, err := flow(t, New(testKey, OIDC("acme", f.URL, cfg())), f, "acme", nil); !errors.Is(err, ErrIDToken) {
			t.Fatalf("%s tampered signature = %v", alg, err)
		}
	}

	// An unknown key id is rejected after one JWKS refresh.
	f := newFakeServer(t)
	f.kid = "rotated-away"

	if _, err := flow(t, New(testKey, OIDC("acme", f.URL, cfg())), f, "acme", nil); !errors.Is(err, ErrIDToken) {
		t.Fatalf("unknown kid = %v", err)
	}

	// Multiple audiences with the right azp are fine.
	f2 := newFakeServer(t)
	f2.claims = map[string]any{"aud": []any{"client", "other"}, "azp": "client"}
	if _, err := flow(t, New(testKey, OIDC("acme", f2.URL, cfg())), f2, "acme", nil); err != nil {
		t.Fatalf("azp = %v", err)
	}
}

func TestSecurity_A07_SocialStateMismatch(t *testing.T) {
	t.Parallel()

	f := newFakeServer(t)
	f.profile = map[string]any{"id": 1.0, "login": "ana"}
	s := New(testKey, GitHub(cfg(), WithEndpoints(oauthEndpoints(f))))

	tweaks := map[string]func(q url.Values, ck *http.Cookie){
		"state param":      func(q url.Values, _ *http.Cookie) { q.Set("state", "attacker") },
		"missing state":    func(q url.Values, _ *http.Cookie) { q.Del("state") },
		"tampered cookie":  func(_ url.Values, ck *http.Cookie) { ck.Value = "x" + ck.Value },
		"forged signature": func(_ url.Values, ck *http.Cookie) { ck.Value = strings.SplitN(ck.Value, ".", 2)[0] + ".AAAA" },
		"no cookie":        func(_ url.Values, ck *http.Cookie) { ck.Name = "other" },
		"missing code":     func(q url.Values, _ *http.Cookie) { q.Del("code") },
	}

	for name, tweak := range tweaks {
		if _, err := flow(t, s, f, "github", tweak); !errors.Is(err, ErrState) {
			t.Errorf("%s: err = %v", name, err)
		}
	}

	// Provider errors.
	if _, err := flow(
		t,
		s,
		f,
		"github",
		func(q url.Values, _ *http.Cookie) { q.Set("error", "access_denied") },
	); !errors.Is(
		err,
		ErrAccessDenied,
	) {
		t.Errorf("access_denied = %v", err)
	}

	if _, err := flow(
		t,
		s,
		f,
		"github",
		func(q url.Values, _ *http.Cookie) { q.Set("error", "server_error") },
	); errs.Status(
		err,
	) != 400 {
		t.Errorf("server_error = %v", err)
	}

	// Expired state.
	expired := New(testKey, GitHub(cfg(), WithEndpoints(oauthEndpoints(f))))
	expired.StateTTL = -time.Minute

	if _, err := flow(t, expired, f, "github", nil); !errors.Is(err, ErrState) {
		t.Errorf("expired state = %v", err)
	}
}

func TestSecurity_A07_SocialReplayAndPKCE(t *testing.T) {
	t.Parallel()

	f := newFakeServer(t)
	f.profile = map[string]any{"id": 1.0, "login": "ana"}
	s := New(testKey, GitHub(cfg(), WithEndpoints(oauthEndpoints(f))))

	// Capture a successful callback and replay it.
	var (
		replayQ  url.Values
		replayCk http.Cookie
	)

	if _, err := flow(
		t,
		s,
		f,
		"github",
		func(q url.Values, ck *http.Cookie) { replayQ, replayCk = q, *ck },
	); err != nil {
		t.Fatal(err)
	}

	cb := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/cb?"+replayQ.Encode(), http.NoBody)
	cb.AddCookie(&replayCk)

	if _, err := s.User(web.NewCtx(httptest.NewRecorder(), cb, nil), "github"); !errors.Is(err, ErrState) {
		t.Fatalf("replayed state = %v", err)
	}

	// The provider rejects a code exchanged without the right verifier, so
	// an intercepted code is useless (PKCE).
	if _, err := s.providers["github"].Exchange(t.Context(), s.HTTP, "code-unknown", "verifier"); err == nil {
		t.Fatal("exchange without matching PKCE verifier succeeded")
	}

	// Unknown providers are 404.
	rec := httptest.NewRecorder()
	if err := s.Redirect(
		web.NewCtx(rec, httptest.NewRequestWithContext(t.Context(), "GET", "/", http.NoBody), nil),
		"myspace",
	); !errors.Is(
		err,
		ErrUnknownProvider,
	) {
		t.Fatalf("unknown provider = %v", err)
	}
}

func TestStatelessAndHTMX(t *testing.T) {
	t.Parallel()

	f := newFakeServer(t)
	f.profile = map[string]any{"sub": "u1", "email": "a@b.io", "email_verified": true}

	s := New(testKey, OIDC("acme", f.URL, cfg()), Microsoft("common", cfg()))

	u, err := s.UserFromToken(t.Context(), "acme", "access-abc")
	if err != nil || u.ID != "u1" || !u.EmailVerified || u.Token.Access != "access-abc" {
		t.Fatalf("UserFromToken = %+v %v", u, err)
	}

	if _, err := s.UserFromToken(t.Context(), "acme", "bad"); err == nil {
		t.Fatal("bad access token accepted")
	}

	if _, err := s.UserFromToken(t.Context(), "nope", "x"); !errors.Is(err, ErrUnknownProvider) {
		t.Fatal("unknown provider")
	}

	// htmx requests get HX-Redirect so the browser navigates.
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	req.Header.Set("HX-Request", "true")

	if err := s.Redirect(web.NewCtx(rec, req, nil), "acme"); err != nil {
		t.Fatal(err)
	}

	if rec.Code != http.StatusNoContent || !strings.HasPrefix(rec.Header().Get("HX-Redirect"), f.URL+"/authorize?") {
		t.Fatalf("htmx redirect = %d %v", rec.Code, rec.Header())
	}

	// Microsoft verifies emails with xms_edov and multi-tenant issuers.
	m := Microsoft("common", cfg())
	d := &discovery{Issuer: "https://login.microsoftonline.com/{tenantid}/v2.0"}

	claims := map[string]any{
		"iss": "https://login.microsoftonline.com/t1/v2.0", "tid": "t1", "aud": "client",
		"exp": float64(time.Now().Add(time.Hour).Unix()),
	}
	if err := m.checkClaims(d, claims, ""); err != nil {
		t.Fatalf("tenant issuer = %v", err)
	}

	if m.emailVerified(map[string]any{"email_verified": true}) || !m.emailVerified(map[string]any{"xms_edov": true}) {
		t.Fatal("microsoft email verification")
	}
}

func TestDiscoveryAndJWKSErrors(t *testing.T) {
	t.Parallel()

	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/openid-configuration" {
			_, _ = w.Write([]byte(`{"issuer":"x"}`))
			return
		}

		http.NotFound(w, r)
	}))
	defer broken.Close()

	s := New(testKey, OIDC("broken", broken.URL, cfg()), OIDC("down", "http://127.0.0.1:1", cfg()))

	for _, name := range []string{"broken", "down"} {
		rec := httptest.NewRecorder()
		if err := s.Redirect(
			web.NewCtx(rec, httptest.NewRequestWithContext(t.Context(), "GET", "/", http.NoBody), nil),
			name,
		); err == nil {
			t.Errorf("%s: redirect succeeded", name)
		}
	}

	for _, k := range []jwk{
		{Kty: "RSA", N: "!", E: "AQAB"},
		{Kty: "RSA", N: b64(big.NewInt(12345).Bytes()), E: "AQAB"}, // too small
		{Kty: "EC", Crv: "P-384"},
		{Kty: "EC", Crv: "P-256", X: "!", Y: "AA"},
		{Kty: "EC", Crv: "P-256", X: b64([]byte{1}), Y: b64([]byte{2})}, // not on the curve
	} {
		if _, err := k.public(); err == nil {
			t.Errorf("jwk %+v accepted", k)
		}
	}
}

// send writes a JSON response from the fake server.
func send(enc *json.Encoder, v any) {
	if err := enc.Encode(v); err != nil {
		panic(err)
	}
}

func TestSmallHelpers(t *testing.T) {
	t.Parallel()

	if Google(cfg()).Name() != "google" || toInt("3600") != 3600 || toInt(true) != 0 || idString(true) != "" {
		t.Fatal("helpers")
	}

	p := OIDC("acme", "https://idp.example", cfg()).AuthParams(url.Values{"prompt": {"login"}})
	got := authURL("https://idp.example/auth?x=1", cfg(), p.scopes, "s", "c", "n", p.extra)

	if !strings.HasPrefix(got, "https://idp.example/auth?x=1&") || !strings.Contains(got, "prompt=login") ||
		!strings.Contains(got, "nonce=n") || !strings.Contains(got, "scope=openid+email+profile") {
		t.Fatalf("authURL = %s", got)
	}

	gh := GitHub(Config{ClientID: "c", Scopes: []string{"repo"}}, WithAuthParams(url.Values{"allow_signup": {"false"}}))
	if u, _ := gh.AuthURL(
		t.Context(),
		nil,
		"s",
		"c",
		"",
	); !strings.Contains(u, "scope=repo") ||
		!strings.Contains(u, "allow_signup=false") {
		t.Fatalf("github auth URL = %s", u)
	}
}

func TestFakeProvider(t *testing.T) {
	t.Parallel()

	s := New(testKey)
	s.Use(Fake("github", "/auth/github/callback", User{ID: "7", Email: "ana@x.io", EmailVerified: true}))

	if names := s.Names(); len(names) != 1 || names[0] != "github" {
		t.Fatalf("Names = %v", names)
	}

	rec := httptest.NewRecorder()
	if err := s.Redirect(
		web.NewCtx(rec, httptest.NewRequestWithContext(t.Context(), "GET", "/", http.NoBody), nil),
		"github",
	); err != nil {
		t.Fatal(err)
	}

	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/auth/github/callback?code=fake-code&state=") {
		t.Fatalf("Location = %s", loc)
	}

	cb := httptest.NewRequestWithContext(t.Context(), http.MethodGet, loc, http.NoBody)
	cb.AddCookie(rec.Result().Cookies()[0])

	u, err := s.User(web.NewCtx(httptest.NewRecorder(), cb, nil), "github")
	if err != nil || u.ID != "7" || u.Provider != "github" || u.Token.Access != "fake-access-token" {
		t.Fatalf("User = %+v %v", u, err)
	}

	if _, err := s.providers["github"].Exchange(t.Context(), nil, "other", ""); err == nil {
		t.Fatal("unexpected code accepted")
	}
}
