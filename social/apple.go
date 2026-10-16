package social

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/AlexGalhardo/vukem/auth/jwt"
)

// LinkedIn signs in with LinkedIn ("Sign In with LinkedIn using OpenID
// Connect"; scopes openid, profile, email).
func LinkedIn(cfg Config) *OIDCProvider {
	return OIDC("linkedin", "https://www.linkedin.com/oauth", cfg)
}

// X signs in with X (formerly Twitter) using OAuth 2.0 with PKCE. X does
// not share email addresses, so link X accounts by provider id.
func X(cfg Config, opts ...Option) *OAuth2Provider {
	p := newOAuth2("x", cfg, Endpoints{ //nolint:gosec // G101: public endpoint URLs, not credentials
		Auth:  "https://x.com/i/oauth2/authorize",
		Token: "https://api.x.com/2/oauth2/token",
		API:   "https://api.x.com/2",
	}, []string{"tweet.read", "users.read"}, xUser, opts)
	p.basicAuth = true

	return p
}

func xUser(ctx context.Context, h *http.Client, ep Endpoints, tok Token) (User, error) {
	var res struct {
		Data map[string]any `json:"data"`
	}

	if err := getJSON(ctx, h, ep.API+"/users/me?user.fields=profile_image_url", tok.Access, &res); err != nil {
		return User{}, fmt.Errorf("social: x profile: %w", err)
	}

	d := res.Data

	return User{
		ID: idString(d["id"]), Name: str(d["name"]), Nickname: str(d["username"]),
		AvatarURL: str(d["profile_image_url"]), Raw: d,
	}, nil
}

// AppleConfig configures Sign in with Apple. ClientID is the Services ID;
// the client secret is a short-lived ES256 JWT signed with the .p8 key
// from the Apple developer account, so Config.ClientSecret is unused.
type AppleConfig struct {
	Config

	// TeamID is the Apple developer team id.
	TeamID string
	// KeyID is the id of the Sign in with Apple key.
	KeyID string
	// PrivateKey is the PEM (.p8, PKCS#8) of that key.
	PrivateKey []byte
	// Issuer overrides https://appleid.apple.com (tests).
	Issuer string
}

// Apple signs in with Apple (OIDC with response_mode=form_post: the
// callback is a POST from appleid.apple.com, so register POST on the
// callback route, serve the app over HTTPS and add https://appleid.apple.com
// to CSRF_TRUSTED_ORIGINS). Apple sends the user's name only on the first
// sign-in.
func Apple(cfg AppleConfig) (*OIDCProvider, error) {
	key, err := parseP8(cfg.PrivateKey)
	if err != nil {
		return nil, err
	}

	if cfg.TeamID == "" || cfg.KeyID == "" || cfg.ClientID == "" {
		return nil, errors.New("social: apple needs TeamID, KeyID and ClientID")
	}

	issuer := cfg.Issuer
	if issuer == "" {
		issuer = "https://appleid.apple.com"
	}

	p := OIDC("apple", issuer, cfg.Config)
	p.scopes = []string{"name", "email"}
	p.extra = url.Values{"response_mode": {"form_post"}}
	p.formPost = true
	// Apple sends email_verified as the string "true".
	p.emailVerified = func(c map[string]any) bool { return c["email_verified"] == true || c["email_verified"] == "true" }
	p.secret = appleSecret(cfg, issuer, key)

	return p, nil
}

func parseP8(pemBytes []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("social: apple private key is not PEM")
	}

	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("social: apple private key: %w", err)
	}

	ec, ok := k.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("social: apple private key is not an EC key")
	}

	return ec, nil
}

// appleSecret signs the client secret JWT, reused for an hour.
func appleSecret(cfg AppleConfig, issuer string, key *ecdsa.PrivateKey) func(time.Time) (string, error) {
	var (
		mu      sync.Mutex
		secret  string
		expires time.Time
	)

	return func(now time.Time) (string, error) {
		mu.Lock()
		defer mu.Unlock()

		if secret != "" && now.Before(expires.Add(-5*time.Minute)) {
			return secret, nil
		}

		exp := now.Add(time.Hour)

		s, err := jwt.Sign(jwt.ES256(cfg.KeyID, key), jwt.Claims{
			Issuer: cfg.TeamID, Subject: cfg.ClientID, Audience: []string{issuer},
			IssuedAt: now, ExpiresAt: exp,
		})
		if err != nil {
			return "", fmt.Errorf("social: apple client secret: %w", err)
		}

		secret, expires = s, exp

		return secret, nil
	}
}
