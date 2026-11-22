package oauth_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/auth/jwt"
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/oauth"
	"github.com/AlexGalhardo/vukem/schema"
)

const (
	issuer   = "https://auth.example.com"
	resource = "https://api.example.com/mcp"
	clientID = "app"
	redirect = "https://client.example.com/cb"
	verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
)

func challengeOf(v string) string {
	sum := sha256.Sum256([]byte(v))

	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// consent signs in a fixed user and renders the fields as a plain string.
type consent struct {
	subject  string
	rendered atomic.Pointer[oauth.ConsentRequest]
}

func (c *consent) Subject(w http.ResponseWriter, r *http.Request) (string, bool) {
	if c.subject == "" {
		http.Redirect(w, r, "/login", http.StatusFound)

		return "", false
	}

	return c.subject, true
}

func (c *consent) Render(w http.ResponseWriter, _ *http.Request, req oauth.ConsentRequest) error {
	c.rendered.Store(&req)
	_, err := io.WriteString(w, "consent")

	return err
}

type logBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.b.Write(p)
}

func (l *logBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.b.String()
}

type env struct {
	srv     *oauth.Server
	consent *consent
	logs    *logBuf
	key     *ecdsa.PrivateKey
	clock   *atomic.Int64
}

func sqlStore(t *testing.T) oauth.Store {
	t.Helper()

	db, err := database.Open(t.Context(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	m, err := schema.NewMigrator(db, []schema.Migration{oauth.Migration("1_oauth")})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := m.Up(t.Context()); err != nil {
		t.Fatal(err)
	}

	return oauth.NewSQLStore(db)
}

func newEnv(t *testing.T, store oauth.Store, mutate func(*oauth.Config)) *env {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	e := &env{consent: &consent{subject: "user-1"}, logs: &logBuf{}, key: key, clock: &atomic.Int64{}}
	e.clock.Store(time.Now().Unix())

	cfg := oauth.Config{
		Issuer: issuer, Keys: []jwt.Key{jwt.ES256("k1", key)}, Store: store,
		Clients: oauth.StaticClients{
			{ID: clientID, RedirectURIs: []string{redirect}},
			{ID: "conf", RedirectURIs: []string{redirect}, SecretHash: oauth.SecretHash("s3cret")},
		},
		Consent: e.consent, Resources: []string{resource}, ScopesSupported: []string{"read", "write"},
		CSRFKey: bytes.Repeat([]byte{7}, 32), Logger: slog.New(slog.NewTextHandler(e.logs, nil)),
		Now: func() time.Time { return time.Unix(e.clock.Load(), 0) },
	}
	if mutate != nil {
		mutate(&cfg)
	}

	if e.srv, err = oauth.New(cfg); err != nil {
		t.Fatal(err)
	}

	return e
}

func authQuery(mutate func(url.Values)) url.Values {
	v := url.Values{
		"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {redirect}, "state": {"st"},
		"code_challenge": {challengeOf(verifier)}, "code_challenge_method": {"S256"},
		"scope": {"read"}, "resource": {resource},
	}
	if mutate != nil {
		mutate(v)
	}

	return v
}

func (e *env) get(q url.Values) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	e.srv.AuthorizeHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/authorize?"+q.Encode(), http.NoBody))

	return rec
}

func (e *env) post(h http.Handler, path string, form url.Values, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	for k, v := range hdr {
		r.Header.Set(k, v)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	return rec
}

// approve runs GET then POST of the consent form and returns the code.
func (e *env) approve(t *testing.T, q url.Values) string {
	t.Helper()

	if rec := e.get(q); rec.Code != 200 {
		t.Fatalf("GET /authorize = %d %s", rec.Code, rec.Body.String())
	}

	form := e.consent.rendered.Load().Fields
	form.Set("decision", "approve")

	rec := e.post(e.srv.AuthorizeHandler(), "/authorize", form, nil)
	if rec.Code != http.StatusFound {
		t.Fatalf("POST /authorize = %d %s", rec.Code, rec.Body.String())
	}

	loc, _ := url.Parse(rec.Header().Get("Location"))
	if loc.Query().Get("iss") != issuer || loc.Query().Get("state") != "st" || loc.Query().Get("code") == "" {
		t.Fatalf("redirect = %s, want code, state and iss", loc)
	}

	return loc.Query().Get("code")
}

type tokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Error        string `json:"error"`
}

func (e *env) token(form url.Values, hdr map[string]string) (tokens, int) {
	rec := e.post(e.srv.TokenHandler(), "/token", form, hdr)

	var tk tokens
	_ = json.Unmarshal(rec.Body.Bytes(), &tk)

	return tk, rec.Code
}

func (e *env) exchange(code, v string) (tokens, int) {
	return e.token(url.Values{
		"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {code}, "redirect_uri": {redirect},
		"code_verifier": {v},
	}, nil)
}

func (e *env) refresh(rt string) (tokens, int) {
	return e.token(url.Values{"grant_type": {"refresh_token"}, "client_id": {clientID}, "refresh_token": {rt}}, nil)
}

func stores(t *testing.T) map[string]func() oauth.Store {
	t.Helper()

	return map[string]func() oauth.Store{
		"memory": func() oauth.Store { return oauth.NewMemoryStore() },
		"sql":    func() oauth.Store { return sqlStore(t) },
	}
}

func TestAuthorizationCodeFlow(t *testing.T) {
	t.Parallel()

	for name, mk := range stores(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			e := newEnv(t, mk(), nil)
			code := e.approve(t, authQuery(nil))

			tk, status := e.exchange(code, verifier)
			if status != 200 || tk.TokenType != "Bearer" || tk.ExpiresIn != 600 || tk.RefreshToken == "" {
				t.Fatalf("token = %+v (%d)", tk, status)
			}

			at, err := e.srv.VerifyAccessToken(t.Context(), tk.AccessToken, resource)
			if err != nil || at.Subject != "user-1" || at.ClientID != clientID ||
				strings.Join(at.Scopes, " ") != "read" {
				t.Fatalf("VerifyAccessToken = %+v, %v", at, err)
			}

			// The audience is the resource: another resource refuses the token.
			if _, err := e.srv.VerifyAccessToken(
				t.Context(),
				tk.AccessToken,
				"https://other.example.com",
			); !errors.Is(
				err,
				oauth.ErrInvalidToken,
			) {
				t.Errorf("token accepted for another resource: %v", err)
			}

			next, status := e.refresh(tk.RefreshToken)
			if status != 200 || next.RefreshToken == "" || next.RefreshToken == tk.RefreshToken {
				t.Fatalf("refresh = %+v (%d), want a rotated token", next, status)
			}
		})
	}
}

func TestSecurity_A07_CodeReplayRevokesTheGrant(t *testing.T) {
	t.Parallel()

	for name, mk := range stores(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			e := newEnv(t, mk(), nil)
			code := e.approve(t, authQuery(nil))

			first, _ := e.exchange(code, verifier)
			if first.AccessToken == "" {
				t.Fatal("first redemption failed")
			}

			if tk, status := e.exchange(code, verifier); status != 400 || tk.Error != "invalid_grant" {
				t.Errorf("replay = %+v (%d), want invalid_grant", tk, status)
			}

			if _, status := e.refresh(first.RefreshToken); status != 400 {
				t.Errorf("refresh token of a replayed code = %d, want 400: the grant must be revoked", status)
			}
		})
	}
}

func TestSecurity_A07_PKCE(t *testing.T) {
	t.Parallel()

	e := newEnv(t, oauth.NewMemoryStore(), nil)

	// A wrong verifier fails, burns the code and ends the grant.
	code := e.approve(t, authQuery(nil))
	if tk, status := e.exchange(code, strings.Repeat("a", 43)); status != 400 || tk.Error != "invalid_grant" {
		t.Errorf("wrong verifier = %+v (%d)", tk, status)
	}

	if _, status := e.exchange(code, verifier); status != 400 {
		t.Errorf("the right verifier after a wrong one = %d, want 400", status)
	}

	for name, mut := range map[string]func(url.Values){
		"no challenge":    func(v url.Values) { v.Del("code_challenge") },
		"plain method":    func(v url.Values) { v.Set("code_challenge_method", "plain") },
		"no method":       func(v url.Values) { v.Del("code_challenge_method") },
		"short challenge": func(v url.Values) { v.Set("code_challenge", "abc") },
	} {
		rec := e.get(authQuery(mut))
		if loc := rec.Header().
			Get("Location"); rec.Code != http.StatusFound ||
			!strings.Contains(loc, "error=invalid_request") {
			t.Errorf("%s = %d %q, want a redirect with invalid_request", name, rec.Code, loc)
		}
	}

	// A malformed verifier never reaches the comparison.
	code = e.approve(t, authQuery(nil))
	if _, status := e.exchange(code, "short"); status != 400 {
		t.Errorf("short verifier = %d, want 400", status)
	}
}

func TestSecurity_A01_RedirectURIAndOpenRedirect(t *testing.T) {
	t.Parallel()

	e := newEnv(t, oauth.NewMemoryStore(), nil)

	for _, ru := range []string{
		"https://evil.example.com/cb", redirect + "/", redirect + "?x=1", "https://client.example.com/CB", "",
		"https://client.example.com/cb#frag", "javascript:alert(1)",
	} {
		rec := e.get(authQuery(func(v url.Values) { v.Set("redirect_uri", ru) }))
		if rec.Code != 400 || rec.Header().Get("Location") != "" {
			t.Errorf(
				"redirect_uri %q = %d Location=%q, want a 400 that does not redirect",
				ru,
				rec.Code,
				rec.Header().Get("Location"),
			)
		}
	}

	if rec := e.get(
		authQuery(func(v url.Values) { v.Set("client_id", "ghost") }),
	); rec.Code != 400 ||
		rec.Header().Get("Location") != "" {
		t.Errorf("unknown client = %d, want a 400 without redirect", rec.Code)
	}

	// At the token endpoint the redirect URI must be the one of the request.
	code := e.approve(t, authQuery(nil))

	tk, status := e.token(url.Values{
		"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {code},
		"redirect_uri": {"https://client.example.com/other"}, "code_verifier": {verifier},
	}, nil)
	if status != 400 || tk.Error != "invalid_grant" {
		t.Errorf("redirect mismatch at /token = %+v (%d)", tk, status)
	}
}

func TestSecurity_A01_ResourceIsRequiredAndBound(t *testing.T) {
	t.Parallel()

	e := newEnv(t, oauth.NewMemoryStore(), nil)

	for name, mut := range map[string]func(url.Values){
		"missing":  func(v url.Values) { v.Del("resource") },
		"unlisted": func(v url.Values) { v.Set("resource", "https://evil.example.com/mcp") },
	} {
		rec := e.get(authQuery(mut))
		if loc := rec.Header().Get("Location"); !strings.Contains(loc, "error=invalid_target") {
			t.Errorf("%s resource = %d %q, want invalid_target", name, rec.Code, loc)
		}
	}

	code := e.approve(t, authQuery(nil))

	tk, status := e.token(url.Values{
		"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {code}, "redirect_uri": {redirect},
		"code_verifier": {verifier}, "resource": {"https://other.example.com"},
	}, nil)
	if status != 400 || tk.Error != "invalid_target" {
		t.Errorf("another resource at /token = %+v (%d), want invalid_target", tk, status)
	}

	if rec := e.get(
		authQuery(func(v url.Values) { v.Set("scope", "admin") }),
	); !strings.Contains(
		rec.Header().Get("Location"),
		"invalid_scope",
	) {
		t.Errorf("unsupported scope = %d", rec.Code)
	}
}

func TestSecurity_A07_ConcurrentRefreshReuse(t *testing.T) {
	t.Parallel()

	for name, mk := range stores(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			e := newEnv(t, mk(), nil)
			tk, _ := e.exchange(e.approve(t, authQuery(nil)), verifier)

			const n = 16

			var (
				wg   sync.WaitGroup
				ok   atomic.Int32
				mu   sync.Mutex
				next []string
			)

			for range n {
				wg.Go(func() {
					got, status := e.refresh(tk.RefreshToken)
					if status == 200 {
						ok.Add(1)
						mu.Lock()
						next = append(next, got.RefreshToken)
						mu.Unlock()
					}
				})
			}

			wg.Wait()

			if ok.Load() != 1 {
				t.Fatalf("%d concurrent uses of one refresh token succeeded, want exactly 1", ok.Load())
			}

			// Reuse detection revoked the grant: even the winner's token is dead.
			if len(next) == 1 {
				if _, status := e.refresh(next[0]); status != 400 {
					t.Errorf("the rotated token after reuse = %d, want 400 (grant revoked)", status)
				}
			}
		})
	}
}

func TestSecurity_A07_RefreshReuseAndRevocation(t *testing.T) {
	t.Parallel()

	e := newEnv(t, oauth.NewMemoryStore(), nil)
	tk, _ := e.exchange(e.approve(t, authQuery(nil)), verifier)
	next, _ := e.refresh(tk.RefreshToken)

	if _, status := e.refresh(tk.RefreshToken); status != 400 {
		t.Errorf("reusing a rotated refresh token = %d, want 400", status)
	}

	if _, status := e.refresh(next.RefreshToken); status != 400 {
		t.Errorf("the family after a reuse = %d, want it revoked", status)
	}

	// RFC 7009: revoke with client authentication; an unknown token is a 200.
	tk, _ = e.exchange(e.approve(t, authQuery(nil)), verifier)

	if rec := e.post(
		e.srv.RevokeHandler(),
		"/revoke",
		url.Values{"client_id": {clientID}, "token": {"unknown"}},
		nil,
	); rec.Code != 200 {
		t.Errorf("revoking an unknown token = %d, want 200", rec.Code)
	}

	if rec := e.post(
		e.srv.RevokeHandler(),
		"/revoke",
		url.Values{"client_id": {clientID}, "token": {tk.RefreshToken}},
		nil,
	); rec.Code != 200 {
		t.Fatalf("revoke = %d", rec.Code)
	}

	if _, status := e.refresh(tk.RefreshToken); status != 400 {
		t.Errorf("refresh after revoke = %d, want 400", status)
	}

	// Another client cannot revoke it.
	tk, _ = e.exchange(e.approve(t, authQuery(nil)), verifier)
	e.post(
		e.srv.RevokeHandler(),
		"/revoke",
		url.Values{"client_id": {"conf"}, "client_secret": {"s3cret"}, "token": {tk.RefreshToken}},
		nil,
	)

	if _, status := e.refresh(tk.RefreshToken); status != 200 {
		t.Errorf("a token revoked by another client = %d, want it untouched", status)
	}
}

func sign(t *testing.T, header, payload map[string]any, sig []byte) string {
	t.Helper()

	enc := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}

		return base64.RawURLEncoding.EncodeToString(b)
	}

	return enc(header) + "." + enc(payload) + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func TestSecurity_A02_AccessTokenAlgorithms(t *testing.T) {
	t.Parallel()

	e := newEnv(t, oauth.NewMemoryStore(), nil)
	now := e.clock.Load()
	claims := map[string]any{"iss": issuer, "sub": "u", "aud": resource, "exp": now + 600, "iat": now}

	other, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	otherTok, _ := atSign(
		jwt.ES256("k1", other),
		jwt.Claims{Issuer: issuer, Subject: "u", Audience: []string{resource}, ExpiresAt: time.Unix(now+600, 0)},
	)
	expired, _ := atSign(
		jwt.ES256("k1", e.key),
		jwt.Claims{Issuer: issuer, Subject: "u", Audience: []string{resource}, ExpiresAt: time.Unix(now-3600, 0)},
	)
	wrongIss, _ := atSign(
		jwt.ES256("k1", e.key),
		jwt.Claims{
			Issuer:    "https://evil.example.com",
			Subject:   "u",
			Audience:  []string{resource},
			ExpiresAt: time.Unix(now+600, 0),
		},
	)
	wrongAud, _ := atSign(
		jwt.ES256("k1", e.key),
		jwt.Claims{
			Issuer:    issuer,
			Subject:   "u",
			Audience:  []string{"https://other.example.com"},
			ExpiresAt: time.Unix(now+600, 0),
		},
	)
	multiAud, _ := atSign(
		jwt.ES256("k1", e.key),
		jwt.Claims{
			Issuer:    issuer,
			Subject:   "u",
			Audience:  []string{resource, "https://other.example.com"},
			ExpiresAt: time.Unix(now+600, 0),
		},
	)

	pub, _ := e.key.PublicKey.Bytes()
	mac := hmac.New(sha256.New, pub)
	hs := func() string {
		head := map[string]any{"alg": "HS256", "typ": "JWT", "kid": "k1"}
		hb, err := json.Marshal(head)
		if err != nil {
			t.Fatal(err)
		}
		pb, err := json.Marshal(claims)
		if err != nil {
			t.Fatal(err)
		}
		signing := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(pb)
		mac.Write([]byte(signing))

		return signing + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	}()

	idToken, err := jwt.Sign(
		jwt.ES256("k1", e.key),
		jwt.Claims{Issuer: issuer, Subject: "u", Audience: []string{resource}, ExpiresAt: time.Unix(now+600, 0)},
	)
	if err != nil {
		t.Fatal(err)
	}

	tests := map[string]string{
		"typ JWT, not at+jwt":      idToken,
		"alg none":                 sign(t, map[string]any{"alg": "none", "typ": "JWT"}, claims, nil),
		"alg None":                 sign(t, map[string]any{"alg": "None", "typ": "JWT"}, claims, nil),
		"HS256 keyed with pub key": hs,
		"unknown kid":              sign(t, map[string]any{"alg": "ES256", "kid": "zzz"}, claims, make([]byte, 64)),
		"signed by another key":    otherTok,
		"expired":                  expired,
		"wrong issuer":             wrongIss,
		"wrong audience":           wrongAud,
		"several audiences":        multiAud,
		"empty":                    "",
		"garbage":                  "a.b.c",
	}

	for name, tok := range tests {
		if _, err := e.srv.VerifyAccessToken(t.Context(), tok, resource); !errors.Is(err, oauth.ErrInvalidToken) {
			t.Errorf("%s: VerifyAccessToken = %v, want ErrInvalidToken", name, err)
		}
	}
}

func TestSecurity_A01_ConsentCSRF(t *testing.T) {
	t.Parallel()

	e := newEnv(t, oauth.NewMemoryStore(), nil)

	if rec := e.get(authQuery(nil)); rec.Code != 200 {
		t.Fatalf("GET = %d", rec.Code)
	}

	good := e.consent.rendered.Load().Fields
	good.Set("decision", "approve")

	clone := func(mut func(url.Values)) url.Values {
		v := url.Values{}
		for k, vs := range good {
			v[k] = append([]string(nil), vs...)
		}

		mut(v)

		return v
	}

	tests := map[string]struct {
		form url.Values
		hdr  map[string]string
	}{
		"no token":      {clone(func(v url.Values) { v.Del("csrf") }), nil},
		"garbage token": {clone(func(v url.Values) { v.Set("csrf", "9999999999.AAAA") }), nil},
		"token of other URI": {
			clone(func(v url.Values) { v.Set("redirect_uri", redirect); v.Set("state", "other") }),
			nil,
		},
		"token of other res": {clone(func(v url.Values) { v.Set("scope", "read write") }), nil},
		"cross-site":         {good, map[string]string{"Sec-Fetch-Site": "cross-site"}},
	}

	for name, tc := range tests {
		rec := e.post(e.srv.AuthorizeHandler(), "/authorize", tc.form, tc.hdr)
		if rec.Code != 400 || rec.Header().Get("Location") != "" {
			t.Errorf("%s = %d Location=%q, want a 400", name, rec.Code, rec.Header().Get("Location"))
		}
	}

	// Another user cannot use the token of the first.
	e.consent.subject = "user-2"

	if rec := e.post(e.srv.AuthorizeHandler(), "/authorize", good, nil); rec.Code != 400 {
		t.Errorf("token of another user = %d, want 400", rec.Code)
	}

	e.consent.subject = "user-1"

	// An expired form.
	e.clock.Add(int64(11 * time.Minute / time.Second))

	if rec := e.post(e.srv.AuthorizeHandler(), "/authorize", good, nil); rec.Code != 400 {
		t.Errorf("expired consent form = %d, want 400", rec.Code)
	}

	e.clock.Add(-int64(11 * time.Minute / time.Second))

	// A denial redirects with access_denied and the issuer.
	deny := clone(func(v url.Values) { v.Set("decision", "deny") })

	rec := e.post(e.srv.AuthorizeHandler(), "/authorize", deny, nil)
	if loc := rec.Header().
		Get("Location"); !strings.Contains(loc, "error=access_denied") ||
		!strings.Contains(loc, "iss=") {
		t.Errorf("deny = %d %q", rec.Code, loc)
	}

	if rec := e.post(e.srv.AuthorizeHandler(), "/authorize", good, nil); rec.Code != http.StatusFound {
		t.Errorf("the untouched form = %d, want it to work", rec.Code)
	}
}

func TestSecurity_A07_ClientAuthentication(t *testing.T) {
	t.Parallel()

	e := newEnv(t, oauth.NewMemoryStore(), nil)

	q := authQuery(func(v url.Values) { v.Set("client_id", "conf") })
	e.get(q)

	form := e.consent.rendered.Load().Fields
	form.Set("decision", "approve")

	loc, _ := url.Parse(e.post(e.srv.AuthorizeHandler(), "/authorize", form, nil).Header().Get("Location"))
	code := loc.Query().Get("code")

	base := func() url.Values {
		return url.Values{
			"grant_type":    {"authorization_code"},
			"code":          {code},
			"redirect_uri":  {redirect},
			"code_verifier": {verifier},
		}
	}

	for name, f := range map[string]struct {
		form url.Values
		hdr  map[string]string
	}{
		"no secret":                   {func() url.Values { v := base(); v.Set("client_id", "conf"); return v }(), nil},
		"wrong secret":                {func() url.Values { v := base(); v.Set("client_id", "conf"); v.Set("client_secret", "nope"); return v }(), nil},
		"public client with a secret": {func() url.Values { v := base(); v.Set("client_id", clientID); v.Set("client_secret", "x"); return v }(), nil},
		"unknown client":              {func() url.Values { v := base(); v.Set("client_id", "ghost"); return v }(), nil},
	} {
		if _, status := e.token(f.form, f.hdr); status != 401 {
			t.Errorf("%s = %d, want 401 invalid_client", name, status)
		}
	}

	basic := "Basic " + base64.StdEncoding.EncodeToString([]byte("conf:s3cret"))
	if tk, status := e.token(base(), map[string]string{"Authorization": basic}); status != 200 || tk.AccessToken == "" {
		t.Errorf("HTTP Basic = %+v (%d), want tokens", tk, status)
	}

	// Content type and method are checked.
	r := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(`{"grant_type":"x"}`))
	r.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	e.srv.TokenHandler().ServeHTTP(rec, r)

	if rec.Code != 400 {
		t.Errorf("JSON body at /token = %d, want 400", rec.Code)
	}

	if tk, status := e.token(
		url.Values{"grant_type": {"password"}, "client_id": {clientID}},
		nil,
	); status != 400 ||
		tk.Error != "unsupported_grant_type" {
		t.Errorf("password grant = %+v (%d)", tk, status)
	}
}

type failingStore struct{ oauth.Store }

func (failingStore) RedeemCode(context.Context, string, time.Time) (oauth.Code, error) {
	return oauth.Code{}, errors.New("db down: password=hunter2")
}

func TestSecurity_A09_NoSecretsInLogsOrErrors(t *testing.T) {
	t.Parallel()

	e := newEnv(t, failingStore{oauth.NewMemoryStore()}, nil)
	code := e.approve(t, authQuery(nil))

	rec := e.post(e.srv.TokenHandler(), "/token", url.Values{
		"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {code}, "redirect_uri": {redirect},
		"code_verifier": {verifier},
	}, nil)

	if rec.Code != 500 || strings.Contains(rec.Body.String(), "hunter2") ||
		strings.Contains(rec.Body.String(), "db down") {
		t.Errorf("store failure = %d %s, want a bare server_error", rec.Code, rec.Body.String())
	}

	for _, secret := range []string{code, verifier, "s3cret"} {
		if strings.Contains(e.logs.String(), secret) {
			t.Errorf("the log contains %q: %s", secret, e.logs.String())
		}
	}
}

func TestMetadataAndJWKS(t *testing.T) {
	t.Parallel()

	e := newEnv(t, oauth.NewMemoryStore(), func(c *oauth.Config) {
		c.Clients = oauth.Resolvers{c.Clients, &oauth.MetadataDocuments{}}
	})

	rec := httptest.NewRecorder()
	e.srv.MetadataHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", http.NoBody))

	var md map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &md); err != nil {
		t.Fatal(err)
	}

	for k, want := range map[string]any{
		"issuer": issuer, "token_endpoint": issuer + "/token", "authorization_response_iss_parameter_supported": true,
		"client_id_metadata_document_supported": true,
	} {
		if md[k] != want {
			t.Errorf("metadata[%s] = %v, want %v", k, md[k], want)
		}
	}

	if _, ok := md["registration_endpoint"]; ok {
		t.Error("metadata advertises dynamic client registration")
	}

	if m := md["code_challenge_methods_supported"].([]any); len(m) != 1 || m[0] != "S256" {
		t.Errorf("code_challenge_methods_supported = %v, want only S256", m)
	}

	rec = httptest.NewRecorder()
	e.srv.JWKSHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", http.NoBody))

	if body := rec.Body.String(); !strings.Contains(body, `"kid":"k1"`) || strings.Contains(body, `"d"`) {
		t.Errorf("jwks = %s, want the public key only", body)
	}
}

func TestSecurity_A05_ConfigRefusesWeakSetup(t *testing.T) {
	t.Parallel()

	hs := jwt.HS256("k", bytes.Repeat([]byte{1}, 32))

	for name, mut := range map[string]func(*oauth.Config){
		"http issuer":       func(c *oauth.Config) { c.Issuer = "http://auth.example.com" },
		"trailing slash":    func(c *oauth.Config) { c.Issuer = issuer + "/" },
		"symmetric key":     func(c *oauth.Config) { c.Keys = []jwt.Key{hs} },
		"no keys":           func(c *oauth.Config) { c.Keys = nil },
		"short csrf key":    func(c *oauth.Config) { c.CSRFKey = []byte("short") },
		"no resources":      func(c *oauth.Config) { c.Resources = nil },
		"relative resource": func(c *oauth.Config) { c.Resources = []string{"/mcp"} },
		"no consent":        func(c *oauth.Config) { c.Consent = nil },
	} {
		e := newEnv(t, oauth.NewMemoryStore(), nil)
		_ = e

		key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		cfg := oauth.Config{
			Issuer:    issuer,
			Keys:      []jwt.Key{jwt.ES256("k", key)},
			Store:     oauth.NewMemoryStore(),
			Clients:   oauth.StaticClients{},
			Consent:   e.consent,
			Resources: []string{resource},
			CSRFKey:   bytes.Repeat([]byte{1}, 32),
		}
		mut(&cfg)

		if _, err := oauth.New(cfg); err == nil {
			t.Errorf("%s: New succeeded, want an error", name)
		}
	}
}

func TestRateLimitHook(t *testing.T) {
	t.Parallel()

	var seen []string

	e := newEnv(t, oauth.NewMemoryStore(), func(c *oauth.Config) {
		c.Allow = func(_ *http.Request, endpoint string) bool {
			seen = append(seen, endpoint)

			return false
		}
	})

	if rec := e.get(authQuery(nil)); rec.Code != 429 {
		t.Errorf("authorize = %d, want 429", rec.Code)
	}

	if rec := e.post(e.srv.TokenHandler(), "/token", url.Values{}, nil); rec.Code != 429 {
		t.Errorf("token = %d, want 429", rec.Code)
	}

	if rec := e.post(e.srv.RevokeHandler(), "/revoke", url.Values{}, nil); rec.Code != 429 || len(seen) != 3 {
		t.Errorf("revoke = %d, hook saw %v", rec.Code, seen)
	}
}

// atSign signs like the server does: an RFC 9068 access token.
func atSign(k jwt.Key, c jwt.Claims) (string, error) { return jwt.SignTyped(k, "at+jwt", c) }

const metaID = "https://client.example.com/meta.json"

func metaEnv(t *testing.T) *env {
	t.Helper()

	return newEnv(t, oauth.NewMemoryStore(), func(c *oauth.Config) {
		c.Clients = oauth.Resolvers{c.Clients, docClient(reply(200, nil, goodDoc))}
	})
}

func TestSecurity_A01_MetadataClientPreConsentErrorsDoNotRedirect(t *testing.T) {
	t.Parallel()

	tests := map[string]func(url.Values){
		"unsupported_response_type": func(v url.Values) { v.Set("response_type", "token") },
		"invalid_request":           func(v url.Values) { v.Del("code_challenge") },
		"invalid_target":            func(v url.Values) { v.Set("resource", "https://other.example.com") },
		"invalid_scope":             func(v url.Values) { v.Set("scope", "admin") },
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			meta := func(v url.Values) { v.Set("client_id", metaID); mutate(v) }

			e := metaEnv(t)
			e.consent.subject = ""

			rec := e.get(authQuery(meta))
			if rec.Code != http.StatusBadRequest || rec.Header().Get("Location") != "" {
				t.Errorf(
					"metadata client = %d Location=%q, want a 400 without redirect",
					rec.Code,
					rec.Header().Get("Location"),
				)
			}

			// A pre-registered client keeps the standard redirect.
			rec = e.get(authQuery(mutate))
			if rec.Code != http.StatusFound || !strings.HasPrefix(rec.Header().Get("Location"), redirect+"?error=") {
				t.Errorf(
					"registered client = %d Location=%q, want the error redirect",
					rec.Code,
					rec.Header().Get("Location"),
				)
			}
		})
	}
}

func TestMetadataClientConsentFlow(t *testing.T) {
	t.Parallel()

	e := metaEnv(t)
	q := authQuery(func(v url.Values) { v.Set("client_id", metaID) })

	if rec := e.get(q); rec.Code != http.StatusOK {
		t.Fatalf("GET /authorize = %d %s", rec.Code, rec.Body.String())
	}

	form := e.consent.rendered.Load().Fields
	form.Set("decision", "approve")

	rec := e.post(e.srv.AuthorizeHandler(), "/authorize", form, nil)
	if loc, _ := url.Parse(
		rec.Header().Get("Location"),
	); rec.Code != http.StatusFound ||
		loc.Query().Get("code") == "" {
		t.Fatalf("approve = %d Location=%q, want a code", rec.Code, rec.Header().Get("Location"))
	}

	form.Set("decision", "deny")

	rec = e.post(e.srv.AuthorizeHandler(), "/authorize", form, nil)
	if loc, _ := url.Parse(rec.Header().Get("Location")); rec.Code != http.StatusFound ||
		loc.Query().Get("error") != "access_denied" {
		t.Fatalf("deny = %d Location=%q, want access_denied", rec.Code, rec.Header().Get("Location"))
	}
}
