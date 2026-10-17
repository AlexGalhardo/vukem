// Package social signs users in with OAuth2 / OpenID Connect providers
// (GitHub, Google, GitLab, Discord, Microsoft and any compliant provider),
// in the spirit of AdonisJS Ally, using only the standard library.
//
// Every flow is Authorization Code + PKCE (S256). The state, PKCE verifier
// and OIDC nonce travel in a short-lived, HMAC-signed, HttpOnly cookie that
// is checked in constant time and used once.
//
//	s := social.New(app.Key.Derive("social.state", 32),
//		social.GitHub(social.Config{ClientID: id, ClientSecret: secret, RedirectURL: url}),
//		social.Google(googleCfg),
//	)
//	r.Get("/auth/{provider}/redirect", func(c *web.Ctx) error { return s.Redirect(c, c.Param("provider")) })
//	r.Get("/auth/{provider}/callback", func(c *web.Ctx) error {
//		u, err := s.User(c, c.Param("provider"))
//		...
//	})
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/authentication.md.
package social

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/web"
)

// Config holds a provider's client credentials. RedirectURL is fixed by
// configuration and never taken from the request.
type Config struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	// Scopes replace the provider defaults when set.
	Scopes []string
}

// Token is the provider's token response.
type Token struct {
	Access  string    `json:"access_token"`
	Refresh string    `json:"refresh_token,omitempty"`
	Expiry  time.Time `json:"expiry,omitzero"`
	Scopes  []string  `json:"scopes,omitempty"`
	IDToken string    `json:"-"`
}

// User is the signed-in identity. EmailVerified is true only when the
// provider vouches for the address; link accounts by (Provider, ID), and
// by Email only when EmailVerified.
type User struct {
	Provider      string         `json:"provider"`
	ID            string         `json:"id"`
	Email         string         `json:"email"`
	EmailVerified bool           `json:"email_verified"`
	Name          string         `json:"name"`
	Nickname      string         `json:"nickname"`
	AvatarURL     string         `json:"avatar_url"`
	Token         Token          `json:"-"`
	Raw           map[string]any `json:"-"`
}

// Provider is one OAuth2/OIDC provider.
type Provider interface {
	// Name is the URL segment ("github").
	Name() string
	// AuthURL is the authorization endpoint URL for this attempt.
	AuthURL(ctx context.Context, h *http.Client, state, challenge, nonce string) (string, error)
	// Exchange trades the code for tokens.
	Exchange(ctx context.Context, h *http.Client, code, verifier string) (Token, error)
	// User loads the identity; nonce is the expected OIDC nonce ("" in
	// stateless mode).
	User(ctx context.Context, h *http.Client, tok Token, nonce string) (User, error)
}

// Errors returned by the callback, mapped to HTTP statuses.
var (
	ErrUnknownProvider = &errs.HTTPError{Status: http.StatusNotFound, Message: "Unknown sign-in provider."}
	ErrState           = &errs.HTTPError{
		Status:  http.StatusBadRequest,
		Message: "The sign-in attempt expired or is invalid. Please try again.",
	}
	ErrAccessDenied = &errs.HTTPError{Status: http.StatusForbidden, Message: "Sign-in was cancelled."}
)

// Social is the set of configured providers.
type Social struct {
	key       []byte
	providers map[string]Provider
	// HTTP is the client for provider calls (timeouts by default).
	HTTP *http.Client
	// StateTTL bounds the time between redirect and callback.
	StateTTL time.Duration
	// InsecureCookies drops the Secure flag (plain-HTTP development only).
	InsecureCookies bool
	now             func() time.Time

	mu   sync.Mutex
	used map[string]int64 // consumed states until they expire
}

// New returns Social with providers; key signs the state cookie (derive it
// from APP_KEY: app.Key.Derive("social.state", 32)).
func New(key []byte, providers ...Provider) *Social {
	s := &Social{
		key:       key,
		providers: map[string]Provider{},
		HTTP:      &http.Client{Timeout: 10 * time.Second},
		StateTTL:  10 * time.Minute,
		now:       time.Now,
		used:      map[string]int64{},
	}

	for _, p := range providers {
		s.providers[p.Name()] = p
	}

	return s
}

// Provider returns a configured provider.
func (s *Social) Provider(name string) (Provider, error) {
	p, ok := s.providers[name]
	if !ok {
		return nil, ErrUnknownProvider
	}

	return p, nil
}

type statePayload struct {
	Provider string `json:"p"`
	State    string `json:"s"`
	Verifier string `json:"v"`
	Nonce    string `json:"n,omitempty"`
	Expires  int64  `json:"e"`
}

func cookieName(provider string) string { return "social_" + provider }

// Redirect starts sign-in with provider: it stores state, PKCE verifier
// and nonce in a signed cookie and redirects to the provider.
func (s *Social) Redirect(c *web.Ctx, provider string) error {
	p, err := s.Provider(provider)
	if err != nil {
		return err
	}

	st := statePayload{
		Provider: provider, State: randomString(), Verifier: randomString(), Nonce: randomString(),
		Expires: s.now().Add(s.StateTTL).Unix(),
	}

	target, err := p.AuthURL(c.Context(), s.HTTP, st.State, challenge(st.Verifier), st.Nonce)
	if err != nil {
		return err
	}

	// G124: HttpOnly, Secure and SameSite are set by cookie().
	ttl := int(s.StateTTL.Seconds())
	ck := s.cookie(provider, s.seal(st), ttl) //nolint:gosec // see above

	if formPost(p) {
		// The provider POSTs the callback from its own site, which a
		// SameSite=Lax cookie would not reach; None requires Secure.
		if s.InsecureCookies {
			return errs.New(http.StatusInternalServerError, "Sign in with "+provider+" needs HTTPS.")
		}

		ck.SameSite = http.SameSiteNoneMode
	}

	c.SetCookie(ck)

	if c.IsHTMX() {
		c.SetHeader("HX-Redirect", target) // a full navigation, not an XHR
		return c.NoContent()
	}

	http.Redirect(c.Writer(), c.Request(), target, http.StatusFound)

	return nil
}

// User completes sign-in: it checks the state cookie (single use), handles
// provider errors, exchanges the code with the PKCE verifier and loads the
// user (verifying the ID token for OIDC providers).
func (s *Social) User(c *web.Ctx, provider string) (User, error) {
	p, err := s.Provider(provider)
	if err != nil {
		return User{}, err
	}

	raw := c.Cookie(cookieName(provider))
	c.SetCookie(s.cookie(provider, "", -1)) // single use, whatever happens next

	// response_mode=form_post providers (Apple) send the result in a POST body.
	param := c.Query
	if formPost(p) && c.Method() == http.MethodPost {
		param = func(k string) string { return c.Request().PostFormValue(k) }
	}

	st, ok := s.open(raw)
	if !ok || st.Provider != provider || s.now().Unix() > st.Expires ||
		subtle.ConstantTimeCompare([]byte(st.State), []byte(param("state"))) != 1 {
		web.SecurityEvent(c.Request(), "social.state_mismatch", "provider", provider)
		return User{}, ErrState
	}

	if !s.consume(st) {
		web.SecurityEvent(c.Request(), "social.state_replayed", "provider", provider)
		return User{}, ErrState
	}

	if e := param("error"); e != "" {
		if e == "access_denied" {
			return User{}, ErrAccessDenied
		}

		return User{}, errs.New(http.StatusBadRequest, "Sign-in failed: "+e+".")
	}

	code := param("code")
	if code == "" {
		return User{}, ErrState
	}

	tok, err := p.Exchange(c.Context(), s.HTTP, code, st.Verifier)
	if err != nil {
		return User{}, errs.Wrap(http.StatusBadGateway, err)
	}

	u, err := p.User(c.Context(), s.HTTP, tok, st.Nonce)
	if err != nil {
		return User{}, errs.Wrap(http.StatusBadGateway, err)
	}

	u.Provider, u.Token = provider, tok

	if u.Name == "" {
		u.Name = postedName(param("user"))
	}

	return u, nil
}

// formPost reports whether p answers with response_mode=form_post.
func formPost(p Provider) bool {
	fp, ok := p.(interface{ FormPost() bool })
	return ok && fp.FormPost()
}

// postedName reads the name Apple posts once, on the first sign-in, as a
// JSON "user" form field.
func postedName(raw string) string {
	if raw == "" {
		return ""
	}

	var v struct {
		Name struct {
			First string `json:"firstName"`
			Last  string `json:"lastName"`
		} `json:"name"`
	}

	if json.Unmarshal([]byte(raw), &v) != nil {
		return ""
	}

	return strings.TrimSpace(v.Name.First + " " + v.Name.Last)
}

// UserFromToken loads the user for an access token obtained elsewhere
// (stateless API/SPA flows). OIDC ID tokens are not available here, so
// only the userinfo/profile endpoint is used.
func (s *Social) UserFromToken(ctx context.Context, provider, accessToken string) (User, error) {
	p, err := s.Provider(provider)
	if err != nil {
		return User{}, err
	}

	u, err := p.User(ctx, s.HTTP, Token{Access: accessToken}, "")
	if err != nil {
		return User{}, err
	}

	u.Provider, u.Token = provider, Token{Access: accessToken}

	return u, nil
}

// consume marks a state as used; a second callback with the same signed
// cookie (a replay) is rejected.
//
// shortcut: the used set lives in memory, so replay protection is per
// instance; move it to the cache store (A.6.1) for multi-instance apps.
func (s *Social) consume(st statePayload) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now().Unix()
	for k, exp := range s.used {
		if exp < now {
			delete(s.used, k)
		}
	}

	if _, seen := s.used[st.State]; seen {
		return false
	}

	s.used[st.State] = st.Expires

	return true
}

func (s *Social) cookie(provider, value string, maxAge int) *http.Cookie {
	// G124: Secure unless the app opts out; SameSite=Lax lets the provider's
	// top-level redirect back carry the cookie.
	return &http.Cookie{ //nolint:gosec // see above
		Name: cookieName(provider), Value: value, Path: "/", MaxAge: maxAge,
		HttpOnly: true, Secure: !s.InsecureCookies, SameSite: http.SameSiteLaxMode,
	}
}

func (s *Social) seal(st statePayload) string {
	b, _ := json.Marshal(st) //nolint:errchkjson // plain struct of strings and ints
	body := base64.RawURLEncoding.EncodeToString(b)

	return body + "." + base64.RawURLEncoding.EncodeToString(s.mac(body))
}

func (s *Social) open(raw string) (statePayload, bool) {
	var st statePayload

	body, sig, ok := strings.Cut(raw, ".")
	if !ok {
		return st, false
	}

	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(got, s.mac(body)) {
		return st, false
	}

	b, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil || json.Unmarshal(b, &st) != nil {
		return st, false
	}

	return st, true
}

func (s *Social) mac(body string) []byte {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(body))

	return m.Sum(nil)
}

// randomString returns 32 random bytes, base64url (43 characters: a valid
// PKCE verifier).
func randomString() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b) // never fails since Go 1.24

	return base64.RawURLEncoding.EncodeToString(b)
}

// challenge is the S256 PKCE challenge of verifier.
func challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// tokenResponse is the RFC 6749 token response.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    any    `json:"expires_in"`
	Scope        string `json:"scope"`
	IDToken      string `json:"id_token"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

// exchange posts the authorization code (with the PKCE verifier) to
// tokenURL. Client credentials go in the body, which every supported
// provider accepts.
func exchange(ctx context.Context, h *http.Client, tokenURL string, cfg Config, code, verifier string,
	now time.Time, basicAuth bool,
) (Token, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {cfg.RedirectURL},
		"client_id":     {cfg.ClientID},
		"code_verifier": {verifier},
	}

	if !basicAuth {
		form.Set("client_secret", cfg.ClientSecret)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	if basicAuth {
		req.SetBasicAuth(url.QueryEscape(cfg.ClientID), url.QueryEscape(cfg.ClientSecret))
	}

	var tr tokenResponse
	if err := doJSON(h, req, &tr); err != nil {
		return Token{}, fmt.Errorf("social: token exchange: %w", err)
	}

	if tr.Error != "" || tr.AccessToken == "" {
		return Token{}, fmt.Errorf("social: token exchange failed: %s %s", tr.Error, tr.ErrorDesc)
	}

	tok := Token{Access: tr.AccessToken, Refresh: tr.RefreshToken, IDToken: tr.IDToken}

	if secs := toInt(tr.ExpiresIn); secs > 0 {
		tok.Expiry = now.Add(time.Duration(secs) * time.Second)
	}

	if tr.Scope != "" {
		tok.Scopes = strings.Fields(tr.Scope)
	}

	return tok, nil
}

func toInt(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case string:
		i, _ := strconv.ParseInt(n, 10, 64)
		return i
	default:
		return 0
	}
}

// maxBody bounds provider responses.
const maxBody = 1 << 20

// doJSON sends req and decodes a 2xx JSON response into v.
func doJSON(h *http.Client, req *http.Request, v any) error {
	resp, err := h.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return err
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// Token endpoints answer 400 with an RFC 6749 error object.
		if json.Unmarshal(body, v) == nil {
			if tr, ok := v.(*tokenResponse); ok && tr.Error != "" {
				return nil
			}
		}

		return fmt.Errorf("%s %s: status %d", req.Method, req.URL.Host, resp.StatusCode)
	}

	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("%s %s: %w", req.Method, req.URL.Host, err)
	}

	return nil
}

// getJSON fetches url with a bearer token.
func getJSON(ctx context.Context, h *http.Client, rawURL, accessToken string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, http.NoBody)
	if err != nil {
		return err
	}

	req.Header.Set("Accept", "application/json")

	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}

	return doJSON(h, req, v)
}

var errNoID = errors.New("social: provider returned no user id")
