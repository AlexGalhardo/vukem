// Package apiv1 holds the JSON API handlers (vukem install:api).
package apiv1

import (
	"net/http"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/examples/api/app/accounts"
	"github.com/AlexGalhardo/vukem/examples/api/app/models"
	"github.com/AlexGalhardo/vukem/web"
	"github.com/AlexGalhardo/vukem/z"
)

// Tokens issues and revokes API tokens.
type Tokens struct {
	a *accounts.Accounts
	// TTL is the token lifetime.
	TTL time.Duration
}

// NewTokens returns the token handlers.
func NewTokens(a *accounts.Accounts) *Tokens { return &Tokens{a: a, TTL: 30 * 24 * time.Hour} }

type issueInput struct {
	Email      string `json:"email"`
	Password   string `json:"password"`
	DeviceName string `json:"device_name"`
	// Abilities are the abilities the caller asks the key to carry, out of
	// Abilities(). Omitting them grants none: a key never carries more
	// authority than the request asked for.
	Abilities []string `json:"abilities"`
}

var issueSchema = z.Struct[issueInput](z.Shape{
	"email":       z.String().Trim().Email(),
	"password":    z.String().Min(1),
	"device_name": z.String().Trim().Min(1).Max(100),
	"abilities": z.Slice(z.String().Trim().OneOf(Abilities()...)).
		Max(len(Abilities())).
		Unique().
		Optional(),
})

// Issue exchanges email and password for a token: POST /api/v1/tokens.
func (h *Tokens) Issue(c *web.Ctx) error {
	in, err := web.Parse(c, issueSchema)
	if err != nil {
		return err
	}

	u, err := h.a.Web.Validate(c, map[string]string{"email": strings.ToLower(in.Email), "password": in.Password})
	if err != nil {
		return err
	}

	// A token is a login: an unverified address may belong to someone else
	// (anyone can sign up first with it).
	if !u.EmailVerified() {
		return errs.NewKey(http.StatusForbidden, "auth.unverified", "Your email address is not verified.", nil)
	}

	// Only what the request asked for: no wildcard, so a route gated on a
	// new ability refuses every key issued before it.
	plain, tok, err := h.a.Tokens.Create(c.Context(), u.AuthID(), in.DeviceName, in.Abilities, h.TTL)
	if err != nil {
		return err
	}

	return c.JSON(http.StatusCreated, map[string]any{
		"token":      plain,
		"type":       "Bearer",
		"abilities":  tok.Abilities,
		"expires_at": tok.ExpiresAt,
	})
}

// Me returns the authenticated user: GET /api/v1/me.
func (*Tokens) Me(c *web.Ctx) error {
	u, err := auth.User[models.User](c, "api")
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, u)
}

// Revoke revokes the token used by this request (the row stays, marked
// revoked_at): DELETE /api/v1/tokens/current.
func (h *Tokens) Revoke(c *web.Ctx) error {
	tok, ok := auth.CurrentToken(c, "api")
	if !ok {
		return auth.ErrUnauthenticated
	}

	if err := h.a.Tokens.Revoke(c.Context(), tok.ID); err != nil {
		return err
	}

	return c.NoContent()
}
