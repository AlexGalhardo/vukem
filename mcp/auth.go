package mcp

import (
	"context"
	"errors"
	"maps"
	"time"
)

// TokenVerifier validates an OAuth access token presented to the MCP
// endpoint. It is the whole contract between this package and an
// authorization server, so any OAuth implementation fits behind an adapter.
//
// resource is the canonical URI of this MCP server (HTTPConfig.Resource);
// the verifier must refuse tokens issued for another audience (RFC 8707).
// It returns ErrInvalidToken, or an error wrapping it, for a token that is
// unknown, expired or not for this resource; any other error means the check
// could not run and the request is refused too.
type TokenVerifier interface {
	VerifyAccessToken(ctx context.Context, token string, resource string) (*TokenInfo, error)
}

// TokenInfo describes a verified access token.
type TokenInfo struct {
	Subject   string
	Tenant    string // from the grant, when the authorization server sets one
	GrantID   string
	ClientID  string
	Scopes    []string
	Resource  string
	ExpiresAt time.Time
	Extra     map[string]string
}

// ErrInvalidToken marks a token the verifier rejects.
var ErrInvalidToken = errors.New("mcp: invalid access token")

type tokenKey struct{}

// WithTokenInfo returns a context carrying info. The HTTP handler does it for
// every authorized request; tests and stdio wrappers may do it too.
func WithTokenInfo(ctx context.Context, info *TokenInfo) context.Context {
	return context.WithValue(ctx, tokenKey{}, info)
}

// TokenInfoFrom returns the verified token of the request, when the handler
// has a Verifier. The caller owns the copy.
func TokenInfoFrom(ctx context.Context) (*TokenInfo, bool) {
	info, ok := ctx.Value(tokenKey{}).(*TokenInfo)
	if !ok || info == nil {
		return nil, false
	}

	cp := *info
	cp.Scopes = append([]string(nil), info.Scopes...)

	if info.Extra != nil {
		cp.Extra = make(map[string]string, len(info.Extra))
		maps.Copy(cp.Extra, info.Extra)
	}

	return &cp, true
}
