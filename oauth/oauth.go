// Package oauth is an OAuth 2.1 authorization server for apps that serve
// protected APIs, such as an MCP server (package mcp). It implements the
// authorization code flow with PKCE (S256 only), resource indicators
// (RFC 8707), authorization server metadata (RFC 8414), issuer identification
// (RFC 9207), token revocation (RFC 7009) and Client ID Metadata Documents.
//
//	srv, err := oauth.New(oauth.Config{
//		Issuer:    "https://auth.example.com",
//		Keys:      []jwt.Key{jwt.ES256("2026-11", signingKey)},
//		Store:     oauth.NewSQLStore(db),
//		Clients:   oauth.Resolvers{oauth.StaticClients{...}, &oauth.MetadataDocuments{}},
//		Consent:   myConsent{},
//		Resources: []string{"https://api.example.com/mcp"},
//		CSRFKey:   key,
//	})
//	r.Handle("GET /authorize", srv.AuthorizeHandler())
//	r.Handle("POST /authorize", srv.AuthorizeHandler())
//	r.Handle("POST /token", srv.TokenHandler())
//	r.Handle("POST /revoke", srv.RevokeHandler())
//	r.Handle("GET /.well-known/oauth-authorization-server", srv.MetadataHandler())
//	r.Handle("GET /.well-known/jwks.json", srv.JWKSHandler())
//
// Access tokens are short-lived JWTs signed with an asymmetric key, with the
// resource as audience. Refresh tokens are opaque, stored hashed, rotated on
// every use; presenting a used one revokes the whole grant. Authorization
// codes are single use and bound to the client, the redirect URI, the PKCE
// challenge and the resource.
//
// There is no Dynamic Client Registration: clients are pre-registered or
// identified by a metadata document.
package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/auth/jwt"
)

// Config configures a Server. Issuer, Keys, Store, Clients, Consent,
// Resources and CSRFKey are required; New returns an error when one is
// missing or weak.
type Config struct {
	// Issuer is the HTTPS URL of this server, without a trailing slash; it
	// is the "iss" of tokens and of authorization responses.
	Issuer string
	// Keys sign access tokens: the first is the active key, the others stay
	// in the JWKS so tokens signed before a rotation still verify. Only
	// asymmetric algorithms are accepted.
	Keys []jwt.Key
	// Store persists grants, codes and refresh tokens.
	Store Store
	// Clients resolves client ids.
	Clients ClientResolver
	// Consent signs the user in and shows the consent screen.
	Consent Consent
	// Resources are the resource identifiers (audiences) tokens may be
	// issued for; a request for another one is refused.
	Resources []string
	// ScopesSupported are the scopes a client may ask for.
	ScopesSupported []string
	// CSRFKey (at least 32 bytes) signs the consent form tokens.
	CSRFKey []byte
	// AccessTokenTTL (default 10 minutes), RefreshTokenTTL (default 30 days),
	// CodeTTL (default 60 seconds) and ConsentTTL, how long a consent form
	// stays valid (default 10 minutes).
	AccessTokenTTL, RefreshTokenTTL, CodeTTL, ConsentTTL time.Duration
	// Allow is a rate-limit hook, called with the endpoint name
	// ("authorize", "token", "revoke") before any work; false answers 429.
	Allow func(r *http.Request, endpoint string) bool
	// Logger receives internal errors (default: discarded). It never gets a
	// token, code, secret or verifier.
	Logger *slog.Logger
	// Now is the clock (default time.Now).
	Now func() time.Time
}

// Server is an OAuth 2.1 authorization server.
type Server struct {
	cfg      Config
	verifier *jwt.Verifier
}

// New validates cfg and returns a Server.
func New(cfg Config) (*Server, error) {
	u, err := url.Parse(cfg.Issuer)

	switch {
	case err != nil || u.Scheme != "https" || u.Host == "" || u.Fragment != "" || u.RawQuery != "" || strings.HasSuffix(cfg.Issuer, "/"):
		return nil, fmt.Errorf(
			"oauth: Issuer %q must be an https URL without a trailing slash, query or fragment",
			cfg.Issuer,
		)
	case len(cfg.Keys) == 0:
		return nil, errors.New("oauth: Keys is required")
	case cfg.Store == nil || cfg.Clients == nil || cfg.Consent == nil:
		return nil, errors.New("oauth: Store, Clients and Consent are required")
	case len(cfg.Resources) == 0:
		return nil, errors.New("oauth: Resources is required")
	case len(cfg.CSRFKey) < 32:
		return nil, errors.New("oauth: CSRFKey must be at least 32 bytes")
	}

	for _, k := range cfg.Keys {
		if k.Alg == jwt.AlgHS256 || k.Alg == "" || k.Private == nil && k.Public == nil {
			return nil, fmt.Errorf("oauth: key %q: only asymmetric keys (RS256, ES256, EdDSA) are accepted", k.ID)
		}
	}

	if cfg.Keys[0].Private == nil {
		return nil, errors.New("oauth: the first key must be a signing key")
	}

	for _, r := range cfg.Resources {
		if ru, err := url.Parse(r); err != nil || ru.Scheme == "" || ru.Host == "" || ru.Fragment != "" {
			return nil, fmt.Errorf("oauth: resource %q must be an absolute URI without a fragment", r)
		}
	}

	setDefault := func(d *time.Duration, v time.Duration) {
		if *d <= 0 {
			*d = v
		}
	}

	setDefault(&cfg.AccessTokenTTL, 10*time.Minute)
	setDefault(&cfg.RefreshTokenTTL, 30*24*time.Hour)
	setDefault(&cfg.CodeTTL, time.Minute)
	setDefault(&cfg.ConsentTTL, 10*time.Minute)

	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}

	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	s := &Server{cfg: cfg}
	s.verifier = jwt.NewVerifier(jwt.StaticKeys(cfg.Keys...), jwt.VerifyOptions{
		Algorithms: []string{jwt.AlgRS256, jwt.AlgES256, jwt.AlgEdDSA},
		Issuer:     cfg.Issuer,
		Types:      []string{"at+jwt"}, // RFC 9068: an ID token or any other JWT is not an access token
		Now:        cfg.Now,
	})

	return s, nil
}

func randomToken() string { return rand.Text() + rand.Text() }

func hashOf(plain string) string {
	sum := sha256.Sum256([]byte(plain))

	return hex.EncodeToString(sum[:])
}

func (s *Server) allowed(w http.ResponseWriter, r *http.Request, endpoint string) bool {
	if s.cfg.Allow == nil || s.cfg.Allow(r, endpoint) {
		return true
	}

	w.Header().Set("Retry-After", "30")
	http.Error(w, "too many requests", http.StatusTooManyRequests)

	return false
}

func (s *Server) allowedResource(res string) bool { return slices.Contains(s.cfg.Resources, res) }

// scopesWithin reports whether every requested scope is supported.
func (s *Server) scopesWithin(scopes []string) bool {
	for _, sc := range scopes {
		if !slices.Contains(s.cfg.ScopesSupported, sc) {
			return false
		}
	}

	return true
}

// AccessToken is a verified access token.
type AccessToken struct {
	Subject string
	// Tenant is the tenant of the grant (Decision.Tenant), empty when the
	// consent did not set one.
	Tenant string
	// GrantID identifies the grant the token belongs to.
	GrantID   string
	ClientID  string
	Scopes    []string
	Resource  string
	ExpiresAt time.Time
}

// ErrInvalidToken is returned by VerifyAccessToken for a token that is
// malformed, badly signed, expired, issued by someone else or for another
// resource.
var ErrInvalidToken = errors.New("oauth: invalid access token")

// VerifyAccessToken checks a token issued by this server for resource: the
// signature (with an algorithm from the allow list, never "none" and never a
// symmetric one), issuer, expiry, that the audience is exactly resource, and
// that its grant still exists and is not revoked, so a revocation takes effect
// on the next call instead of when the token expires. A store failure is
// returned as is and never accepts the token.
func (s *Server) VerifyAccessToken(ctx context.Context, token, resource string) (*AccessToken, error) {
	claims, err := s.verifier.Verify(ctx, token)
	if err != nil || !slices.Contains(claims.Audience, resource) || len(claims.Audience) != 1 {
		return nil, ErrInvalidToken
	}

	scope, _ := claims.Extra["scope"].(string)
	clientID, _ := claims.Extra["client_id"].(string)
	grantID, _ := claims.Extra["gid"].(string)

	if grantID == "" {
		return nil, ErrInvalidToken
	}

	grant, err := s.cfg.Store.Grant(ctx, grantID)
	if errors.Is(err, ErrNotFound) {
		return nil, ErrInvalidToken
	}

	if err != nil {
		return nil, fmt.Errorf("oauth: load grant of access token: %w", err)
	}

	if grant.Revoked || grant.Subject != claims.Subject || grant.ClientID != clientID {
		return nil, ErrInvalidToken
	}

	scopes := strings.Fields(scope)
	if slices.ContainsFunc(scopes, func(sc string) bool { return !slices.Contains(grant.Scopes, sc) }) {
		return nil, ErrInvalidToken
	}

	return &AccessToken{
		Subject: claims.Subject, Tenant: grant.Tenant, GrantID: grant.ID, ClientID: clientID, Scopes: scopes,
		Resource: resource, ExpiresAt: claims.ExpiresAt,
	}, nil
}

func (s *Server) issueAccessToken(
	g Grant,
	clientID string,
	scopes []string,
) (token string, ttl time.Duration, err error) {
	now := s.cfg.Now()
	claims := jwt.Claims{
		Issuer: s.cfg.Issuer, Subject: g.Subject, Audience: []string{g.Resource},
		IssuedAt: now, NotBefore: now, ExpiresAt: now.Add(s.cfg.AccessTokenTTL), ID: rand.Text(),
		Extra: map[string]any{"scope": strings.Join(scopes, " "), "client_id": clientID, "gid": g.ID},
	}

	token, err = jwt.SignTyped(s.cfg.Keys[0], "at+jwt", claims)

	return token, s.cfg.AccessTokenTTL, err
}
