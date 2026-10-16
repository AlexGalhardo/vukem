package social

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Endpoints are an OAuth2 provider's URLs.
type Endpoints struct {
	Auth  string
	Token string
	// API is the base of profile/userinfo calls.
	API string
}

// OAuth2Provider is a plain OAuth2 provider; the built-ins (GitHub, GitLab,
// Discord) and OAuth2 for anything else are values of it.
type OAuth2Provider struct {
	name    string
	cfg     Config
	ep      Endpoints
	scopes  []string
	extra   url.Values
	mapUser func(ctx context.Context, h *http.Client, ep Endpoints, tok Token) (User, error)
	now     func() time.Time
}

// Name implements Provider.
func (p *OAuth2Provider) Name() string { return p.name }

// AuthURL implements Provider.
func (p *OAuth2Provider) AuthURL(_ context.Context, _ *http.Client, state, codeChallenge, _ string) (string, error) {
	return authURL(p.ep.Auth, p.cfg, p.scopes, state, codeChallenge, "", p.extra), nil
}

// Exchange implements Provider.
func (p *OAuth2Provider) Exchange(ctx context.Context, h *http.Client, code, verifier string) (Token, error) {
	return exchange(ctx, h, p.ep.Token, p.cfg, code, verifier, p.now())
}

// User implements Provider.
func (p *OAuth2Provider) User(ctx context.Context, h *http.Client, tok Token, _ string) (User, error) {
	u, err := p.mapUser(ctx, h, p.ep, tok)
	if err == nil && u.ID == "" {
		err = errNoID
	}

	return u, err
}

func authURL(
	base string,
	cfg Config,
	defScopes []string,
	state, codeChallenge, nonce string,
	extra url.Values,
) string {
	scopes := cfg.Scopes
	if len(scopes) == 0 {
		scopes = defScopes
	}

	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {cfg.ClientID},
		"redirect_uri":          {cfg.RedirectURL},
		"state":                 {state},
		"code_challenge":        {codeChallenge},
		"code_challenge_method": {"S256"},
	}

	if len(scopes) > 0 {
		q.Set("scope", strings.Join(scopes, " "))
	}

	if nonce != "" {
		q.Set("nonce", nonce)
	}

	maps.Copy(q, extra)

	if strings.Contains(base, "?") {
		return base + "&" + q.Encode()
	}

	return base + "?" + q.Encode()
}

// Option customizes a built-in provider.
type Option func(*OAuth2Provider)

// WithEndpoints overrides URLs (GitHub Enterprise, self-hosted GitLab, tests).
func WithEndpoints(ep Endpoints) Option { return func(p *OAuth2Provider) { p.ep = ep } }

// WithAuthParams adds authorization URL parameters (e.g. prompt=consent).
func WithAuthParams(v url.Values) Option { return func(p *OAuth2Provider) { p.extra = v } }

func newOAuth2(name string, cfg Config, ep Endpoints, scopes []string,
	mapUser func(context.Context, *http.Client, Endpoints, Token) (User, error), opts []Option,
) *OAuth2Provider {
	p := &OAuth2Provider{name: name, cfg: cfg, ep: ep, scopes: scopes, mapUser: mapUser, now: time.Now}
	for _, o := range opts {
		o(p)
	}

	return p
}

// OAuth2 builds a provider for any OAuth2 server; mapUser loads the
// profile with the access token.
func OAuth2(name string, cfg Config, ep Endpoints, scopes []string,
	mapUser func(ctx context.Context, h *http.Client, ep Endpoints, tok Token) (User, error),
) *OAuth2Provider {
	return newOAuth2(name, cfg, ep, scopes, mapUser, nil)
}

// GitHub signs in with GitHub (scopes read:user, user:email). The email is
// the primary verified address from /user/emails.
func GitHub(cfg Config, opts ...Option) *OAuth2Provider {
	//nolint:gosec // G101: public endpoint URLs, not credentials
	ep := Endpoints{
		Auth:  "https://github.com/login/oauth/authorize",
		Token: "https://github.com/login/oauth/access_token",
		API:   "https://api.github.com",
	}

	return newOAuth2("github", cfg, ep, []string{"read:user", "user:email"}, githubUser, opts)
}

func githubUser(ctx context.Context, h *http.Client, ep Endpoints, tok Token) (User, error) {
	var raw map[string]any
	if err := getJSON(ctx, h, ep.API+"/user", tok.Access, &raw); err != nil {
		return User{}, err
	}

	u := User{
		ID:        idString(raw["id"]),
		Nickname:  str(raw["login"]),
		Name:      str(raw["name"]),
		AvatarURL: str(raw["avatar_url"]),
		Raw:       raw,
	}

	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}

	if err := getJSON(ctx, h, ep.API+"/user/emails", tok.Access, &emails); err == nil {
		for _, e := range emails {
			if e.Primary && e.Verified {
				u.Email, u.EmailVerified = e.Email, true
			}
		}
	}

	if u.Email == "" {
		u.Email = str(raw["email"]) // public email, verification unknown
	}

	return u, nil
}

// GitLab signs in with GitLab.com (or a self-hosted instance via
// WithEndpoints). The primary email is confirmed when confirmed_at is set.
func GitLab(cfg Config, opts ...Option) *OAuth2Provider {
	//nolint:gosec // G101: public endpoint URLs, not credentials
	ep := Endpoints{
		Auth:  "https://gitlab.com/oauth/authorize",
		Token: "https://gitlab.com/oauth/token",
		API:   "https://gitlab.com/api/v4",
	}

	return newOAuth2("gitlab", cfg, ep, []string{"read_user"}, gitlabUser, opts)
}

func gitlabUser(ctx context.Context, h *http.Client, ep Endpoints, tok Token) (User, error) {
	var raw map[string]any
	if err := getJSON(ctx, h, ep.API+"/user", tok.Access, &raw); err != nil {
		return User{}, err
	}

	return User{
		ID:            idString(raw["id"]),
		Email:         str(raw["email"]),
		EmailVerified: raw["confirmed_at"] != nil && str(raw["email"]) != "",
		Name:          str(raw["name"]),
		Nickname:      str(raw["username"]),
		AvatarURL:     str(raw["avatar_url"]),
		Raw:           raw,
	}, nil
}

// Discord signs in with Discord (scopes identify, email).
func Discord(cfg Config, opts ...Option) *OAuth2Provider {
	//nolint:gosec // G101: public endpoint URLs, not credentials
	ep := Endpoints{
		Auth:  "https://discord.com/oauth2/authorize",
		Token: "https://discord.com/api/oauth2/token",
		API:   "https://discord.com/api",
	}

	return newOAuth2("discord", cfg, ep, []string{"identify", "email"}, discordUser, opts)
}

func discordUser(ctx context.Context, h *http.Client, ep Endpoints, tok Token) (User, error) {
	var raw map[string]any
	if err := getJSON(ctx, h, ep.API+"/users/@me", tok.Access, &raw); err != nil {
		return User{}, err
	}

	u := User{
		ID:            str(raw["id"]),
		Email:         str(raw["email"]),
		EmailVerified: raw["verified"] == true,
		Name:          str(raw["global_name"]),
		Nickname:      str(raw["username"]),
		Raw:           raw,
	}

	if u.Name == "" {
		u.Name = u.Nickname
	}

	if avatar := str(raw["avatar"]); avatar != "" && u.ID != "" {
		u.AvatarURL = fmt.Sprintf(
			"https://cdn.discordapp.com/avatars/%s/%s.png",
			url.PathEscape(u.ID),
			url.PathEscape(avatar),
		)
	}

	return u, nil
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

// idString formats numeric JSON ids without exponent notation.
func idString(v any) string {
	switch n := v.(type) {
	case float64:
		return fmt.Sprintf("%.0f", n)
	case string:
		return n
	default:
		return ""
	}
}
