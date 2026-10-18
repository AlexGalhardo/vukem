package jwt

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/web"
)

// Guard authenticates "Authorization: Bearer <jwt>" requests: the token
// must verify and its "sub" must name a user of the provider. It
// implements auth.Checker, so auth.Required(guard) protects routes.
type Guard[U auth.Authenticatable] struct {
	name     string
	verifier *Verifier
	provider auth.UserProvider[U]
}

// NewGuard returns a JWT guard named name. It panics when v.Err() reports
// a misconfiguration (a RemoteJWKS without Audience or Issuer), so the app
// fails at boot rather than refusing every token.
func NewGuard[U auth.Authenticatable](name string, v *Verifier, p auth.UserProvider[U]) *Guard[U] {
	if err := v.Err(); err != nil {
		panic(fmt.Sprintf("jwt: guard %q: %v", name, err))
	}

	return &Guard[U]{name: name, verifier: v, provider: p}
}

// Name implements auth.Checker.
func (g *Guard[U]) Name() string { return g.name }

// APIOnly implements auth.APIGuard: auth.Required answers 401
// problem+json and never redirects to a login page.
func (*Guard[U]) APIOnly() {}

// Check implements auth.Checker.
func (g *Guard[U]) Check(c *web.Ctx) bool {
	_, err := g.User(c)
	return err == nil
}

// Authenticate implements auth.FailClosedChecker, so auth.Required answers
// a key set or user store outage with 503 instead of 401.
func (g *Guard[U]) Authenticate(c *web.Ctx) error {
	_, err := g.User(c)
	return err
}

type claimsKey struct{ guard string }

// User returns the token's user (cached for the request),
// auth.ErrUnauthenticated for a missing, invalid or expired token or an
// unknown user, or a 503 error when the key set (jwt.ErrKeysUnavailable)
// or the user lookup fails, so an outage is not reported as bad
// credentials.
func (g *Guard[U]) User(c *web.Ctx) (U, error) {
	if u, err := auth.User[U](c, g.name); err == nil {
		return u, nil
	}

	var zero U

	raw, ok := auth.BearerToken(c.Request())
	if !ok {
		return zero, auth.ErrUnauthenticated
	}

	claims, err := g.verifier.Verify(c.Context(), raw)

	switch {
	case err == nil:
	case errors.Is(err, ErrKeysUnavailable):
		return zero, errs.Wrap(http.StatusServiceUnavailable, err)
	default:
		web.SecurityEvent(c.Request(), "jwt.rejected", "guard", g.name)
		return zero, auth.ErrUnauthenticated
	}

	u, err := g.provider.ByID(c.Context(), claims.Subject)
	if err != nil {
		if userGone(err) {
			return zero, auth.ErrUnauthenticated
		}

		return zero, errs.Wrap(http.StatusServiceUnavailable, fmt.Errorf("jwt: load user: %w", err))
	}

	auth.SetUser(c, g.name, u)
	c.SetRequest(c.Request().WithContext(context.WithValue(c.Context(), claimsKey{g.name}, claims)))

	return u, nil
}

// ClaimsFrom returns the claims of the token guard authenticated c with.
func ClaimsFrom(c *web.Ctx, guard string) (Claims, bool) {
	cl, ok := c.Context().Value(claimsKey{guard}).(Claims)
	return cl, ok
}

// userGone reports a lookup error meaning the token's subject has no user;
// anything else is an outage.
func userGone(err error) bool {
	return errors.Is(err, auth.ErrUnauthenticated) || errors.Is(err, auth.ErrUserNotFound) ||
		errors.Is(err, orm.ErrNotFound) || errors.Is(err, sql.ErrNoRows)
}
