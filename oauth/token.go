package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/auth/jwt"
)

const maxFormBytes = 16 << 10

// oauthError is an error response of the token or revocation endpoint. It
// carries a code and never a reason: why a code or verifier failed is what an
// attacker wants to know.
type oauthError struct {
	status int
	code   string
}

// Error implements error.
func (e oauthError) Error() string { return e.code }

var (
	errInvalidRequest = oauthError{http.StatusBadRequest, "invalid_request"}
	errInvalidGrant   = oauthError{http.StatusBadRequest, "invalid_grant"}
	errInvalidClient  = oauthError{http.StatusUnauthorized, "invalid_client"}
	errInvalidScope   = oauthError{http.StatusBadRequest, "invalid_scope"}
	errInvalidTarget  = oauthError{http.StatusBadRequest, "invalid_target"}
	errUnsupported    = oauthError{http.StatusBadRequest, "unsupported_grant_type"}
	errServer         = oauthError{http.StatusInternalServerError, "server_error"}
)

func writeOAuth(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.WriteHeader(status)
	if b, err := json.Marshal(v); err == nil {
		_, _ = w.Write(b)
	}
}

func writeOAuthError(w http.ResponseWriter, e oauthError) {
	if e.status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", `Basic realm="oauth"`)
	}

	writeOAuth(w, e.status, map[string]string{"error": e.code})
}

// form reads the urlencoded body, bounded.
func form(w http.ResponseWriter, r *http.Request) (url.Values, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)

	if mt, _, _ := strings.Cut(
		r.Header.Get("Content-Type"),
		";",
	); strings.TrimSpace(
		mt,
	) != "application/x-www-form-urlencoded" {
		writeOAuthError(w, errInvalidRequest)

		return nil, false
	}

	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, errInvalidRequest)

		return nil, false
	}

	return r.PostForm, true
}

// authenticate resolves and authenticates the client of a token or
// revocation request: HTTP Basic, or client_id (and client_secret) in the body.
func (s *Server) authenticate(ctx context.Context, r *http.Request, v url.Values) (*Client, error) {
	id, secret, basic := r.BasicAuth()
	if basic {
		id, _ = url.QueryUnescape(id)
		secret, _ = url.QueryUnescape(secret)
	} else {
		id, secret = v.Get("client_id"), v.Get("client_secret")
	}

	if id == "" {
		return nil, errInvalidClient
	}

	c, err := s.cfg.Clients.Client(ctx, id)
	if err != nil || c == nil {
		return nil, errInvalidClient
	}

	if len(c.SecretHash) > 0 && !c.checkSecret(secret) {
		return nil, errInvalidClient
	}

	if len(c.SecretHash) == 0 && secret != "" {
		return nil, errInvalidClient // a public client does not authenticate
	}

	return c, nil
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope,omitempty"`
}

// TokenHandler serves the token endpoint: authorization_code and
// refresh_token grants.
func (s *Server) TokenHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)

			return
		}

		if !s.allowed(w, r, "token") {
			return
		}

		v, ok := form(w, r)
		if !ok {
			return
		}

		client, err := s.authenticate(r.Context(), r, v)
		if err != nil {
			writeOAuthError(w, errInvalidClient)

			return
		}

		var (
			resp tokenResponse
			oerr *oauthError
		)

		switch v.Get("grant_type") {
		case "authorization_code":
			resp, oerr = s.codeGrant(r.Context(), client, v)
		case "refresh_token":
			resp, oerr = s.refreshGrant(r.Context(), client, v)
		default:
			oerr = &errUnsupported
		}

		if oerr != nil {
			writeOAuthError(w, *oerr)

			return
		}

		writeOAuth(w, http.StatusOK, resp)
	})
}

func (s *Server) codeGrant(ctx context.Context, client *Client, v url.Values) (tokenResponse, *oauthError) {
	code, verifier, redirectURI := v.Get("code"), v.Get("code_verifier"), v.Get("redirect_uri")
	if code == "" || verifier == "" || redirectURI == "" {
		return tokenResponse{}, &errInvalidRequest
	}

	// The code is burned by the first attempt, right or wrong, so a guess
	// costs the attacker the code.
	c, err := s.cfg.Store.RedeemCode(ctx, hashOf(code), s.cfg.Now())
	if err != nil {
		if !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrExpired) && !errors.Is(err, ErrReused) {
			s.cfg.Logger.ErrorContext(ctx, "oauth: redeem code", "err", err)

			return tokenResponse{}, &errServer
		}

		return tokenResponse{}, &errInvalidGrant
	}

	grant, err := s.cfg.Store.Grant(ctx, c.GrantID)
	if err != nil || grant.Revoked {
		return tokenResponse{}, &errInvalidGrant
	}

	// Anything that does not match was not issued to this client: treat it
	// as theft and end the grant.
	if c.ClientID != client.ID || c.RedirectURI != redirectURI || !verifyPKCE(verifier, c.Challenge) {
		_ = s.cfg.Store.RevokeGrant(ctx, grant.ID)

		return tokenResponse{}, &errInvalidGrant
	}

	if res := v.Get("resource"); res != "" && res != grant.Resource {
		_ = s.cfg.Store.RevokeGrant(ctx, grant.ID)

		return tokenResponse{}, &errInvalidTarget
	}

	return s.issue(ctx, grant, client.ID, grant.Scopes, "")
}

func (s *Server) refreshGrant(ctx context.Context, client *Client, v url.Values) (tokenResponse, *oauthError) {
	old := v.Get("refresh_token")
	if old == "" {
		return tokenResponse{}, &errInvalidRequest
	}

	hash := hashOf(old)

	grantID, err := s.cfg.Store.GrantOfRefresh(ctx, hash)
	if err != nil {
		return tokenResponse{}, &errInvalidGrant
	}

	grant, err := s.cfg.Store.Grant(ctx, grantID)
	if err != nil || grant.Revoked || grant.ClientID != client.ID {
		return tokenResponse{}, &errInvalidGrant
	}

	scopes := grant.Scopes

	if raw := v.Get("scope"); raw != "" {
		scopes = strings.Fields(raw)
		if slices.ContainsFunc(scopes, func(sc string) bool { return !slices.Contains(grant.Scopes, sc) }) {
			return tokenResponse{}, &errInvalidScope
		}
	}

	if res := v.Get("resource"); res != "" && res != grant.Resource {
		return tokenResponse{}, &errInvalidTarget
	}

	return s.issue(ctx, grant, client.ID, scopes, hash)
}

// issue mints the tokens. With a non-empty oldRefresh it rotates that
// refresh token atomically first, so two concurrent uses cannot both win.
func (s *Server) issue(
	ctx context.Context,
	g Grant,
	clientID string,
	scopes []string,
	oldRefresh string,
) (tokenResponse, *oauthError) {
	refresh := randomToken()
	next := Refresh{Hash: hashOf(refresh), GrantID: g.ID, ExpiresAt: s.cfg.Now().Add(s.cfg.RefreshTokenTTL)}

	var err error

	if oldRefresh != "" {
		_, err = s.cfg.Store.RotateRefresh(ctx, oldRefresh, next, s.cfg.Now())
	} else {
		err = s.cfg.Store.SaveRefresh(ctx, next)
	}

	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrExpired), errors.Is(err, ErrRevoked), errors.Is(err, ErrReused):
		return tokenResponse{}, &errInvalidGrant
	case err != nil:
		s.cfg.Logger.ErrorContext(ctx, "oauth: store refresh token", "err", err)

		return tokenResponse{}, &errServer
	}

	access, ttl, err := s.issueAccessToken(g, clientID, scopes)
	if err != nil {
		s.cfg.Logger.ErrorContext(ctx, "oauth: sign access token", "err", err)

		return tokenResponse{}, &errServer
	}

	return tokenResponse{
		AccessToken: access, TokenType: "Bearer", ExpiresIn: int64(ttl / time.Second),
		RefreshToken: refresh, Scope: strings.Join(scopes, " "),
	}, nil
}

// RevokeHandler serves the revocation endpoint (RFC 7009). It revokes the
// whole grant behind a refresh token, which also ends its access tokens,
// because VerifyAccessToken checks the grant. Unknown tokens answer 200, as
// the RFC asks.
func (s *Server) RevokeHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)

			return
		}

		if !s.allowed(w, r, "revoke") {
			return
		}

		v, ok := form(w, r)
		if !ok {
			return
		}

		client, err := s.authenticate(r.Context(), r, v)
		if err != nil {
			writeOAuthError(w, errInvalidClient)

			return
		}

		if tok := v.Get("token"); tok != "" {
			if id, err := s.cfg.Store.GrantOfRefresh(r.Context(), hashOf(tok)); err == nil {
				if g, err := s.cfg.Store.Grant(r.Context(), id); err == nil && g.ClientID == client.ID {
					if err := s.cfg.Store.RevokeGrant(r.Context(), id); err != nil {
						s.cfg.Logger.ErrorContext(r.Context(), "oauth: revoke grant", "err", err)
						writeOAuthError(w, errServer)

						return
					}
				}
			}
		}

		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
	})
}

// MetadataHandler serves the authorization server metadata (RFC 8414).
func (s *Server) MetadataHandler() http.Handler {
	_, cimd := s.cfg.Clients.(*MetadataDocuments)
	if rs, ok := s.cfg.Clients.(Resolvers); ok {
		cimd = slices.ContainsFunc(rs, func(r ClientResolver) bool { _, ok := r.(*MetadataDocuments); return ok })
	}

	doc := map[string]any{
		"issuer":                                         s.cfg.Issuer,
		"authorization_endpoint":                         s.cfg.Issuer + "/authorize",
		"token_endpoint":                                 s.cfg.Issuer + "/token",
		"revocation_endpoint":                            s.cfg.Issuer + "/revoke",
		"jwks_uri":                                       s.cfg.Issuer + "/.well-known/jwks.json",
		"response_types_supported":                       []string{"code"},
		"grant_types_supported":                          []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":               []string{"S256"},
		"token_endpoint_auth_methods_supported":          []string{"none", "client_secret_basic", "client_secret_post"},
		"scopes_supported":                               s.cfg.ScopesSupported,
		"authorization_response_iss_parameter_supported": true,
		"client_id_metadata_document_supported":          cimd,
	}

	body, err := json.Marshal(doc)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err != nil || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
			http.Error(w, "unavailable", http.StatusMethodNotAllowed)

			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		_, _ = w.Write(body)
	})
}

// JWKSHandler serves the public keys of every configured key, so tokens
// signed before a rotation still verify at the resource server.
func (s *Server) JWKSHandler() http.Handler {
	set, err := jwt.PublicJWKS(s.cfg.Keys...)
	body, merr := json.Marshal(set)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err != nil || merr != nil || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
			http.Error(w, "unavailable", http.StatusMethodNotAllowed)

			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=300")
		_, _ = w.Write(body)
	})
}
