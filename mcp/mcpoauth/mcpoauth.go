// Package mcpoauth lets an mcp HTTP handler verify the access tokens of an
// oauth.Server. Neither package imports the other; this adapter joins them.
//
//	h, err := srv.HTTPHandler(mcp.HTTPConfig{Verifier: mcpoauth.Verifier(as), ...})
package mcpoauth

import (
	"context"
	"errors"

	"github.com/AlexGalhardo/vukem/mcp"
	"github.com/AlexGalhardo/vukem/oauth"
)

type verifier struct{ as *oauth.Server }

// Verifier returns an mcp.TokenVerifier backed by as.
func Verifier(as *oauth.Server) mcp.TokenVerifier { return verifier{as: as} }

// VerifyAccessToken implements mcp.TokenVerifier.
func (v verifier) VerifyAccessToken(ctx context.Context, token, resource string) (*mcp.TokenInfo, error) {
	at, err := v.as.VerifyAccessToken(ctx, token, resource)
	if errors.Is(err, oauth.ErrInvalidToken) {
		return nil, mcp.ErrInvalidToken
	}

	if err != nil {
		return nil, err
	}

	return &mcp.TokenInfo{
		Subject:   at.Subject,
		Tenant:    at.Tenant,
		GrantID:   at.GrantID,
		ClientID:  at.ClientID,
		Scopes:    at.Scopes,
		Resource:  at.Resource,
		ExpiresAt: at.ExpiresAt,
	}, nil
}
