package jwt

import (
	"context"
	"strings"

	"github.com/AlexGalhardo/vukem/auth"
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

// NewGuard returns a JWT guard named name.
func NewGuard[U auth.Authenticatable](name string, v *Verifier, p auth.UserProvider[U]) *Guard[U] {
	return &Guard[U]{name: name, verifier: v, provider: p}
}

// Name implements auth.Checker.
func (g *Guard[U]) Name() string { return g.name }

// Check implements auth.Checker.
func (g *Guard[U]) Check(c *web.Ctx) bool {
	_, err := g.User(c)
	return err == nil
}

type claimsKey struct{ guard string }

// User returns the token's user (cached for the request) or
// auth.ErrUnauthenticated.
func (g *Guard[U]) User(c *web.Ctx) (U, error) {
	if u, err := auth.User[U](c, g.name); err == nil {
		return u, nil
	}

	var zero U

	raw, ok := strings.CutPrefix(c.Header("Authorization"), "Bearer ")
	if !ok || strings.TrimSpace(raw) == "" {
		return zero, auth.ErrUnauthenticated
	}

	claims, err := g.verifier.Verify(c.Context(), strings.TrimSpace(raw))
	if err != nil {
		web.SecurityEvent(c.Request(), "jwt.rejected", "guard", g.name)
		return zero, auth.ErrUnauthenticated
	}

	u, err := g.provider.ByID(c.Context(), claims.Subject)
	if err != nil {
		return zero, auth.ErrUnauthenticated
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
