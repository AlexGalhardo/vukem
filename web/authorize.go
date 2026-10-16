package web

import (
	"context"
	"errors"
	"net/http"

	"github.com/AlexGalhardo/vukem/errs"
)

// Authorizer decides abilities for the user in ctx: nil, or an error with
// status 401, 403 or 404.
type Authorizer interface {
	Authorize(ctx context.Context, ability string, subject any) error
}

// Authorize checks ability on subject (nil for gate abilities) with
// Services.Authorizer and returns the error to return from the handler.
// Denials are logged as "authz.denied" security events. Without an
// Authorizer it denies (fail closed).
func (c *Ctx) Authorize(ability string, subject any) error {
	var err error

	if c.s.Authorizer == nil {
		err = errs.Wrap(http.StatusForbidden, errors.New("web: no Authorizer configured"))
	} else {
		err = c.s.Authorizer.Authorize(c.Context(), ability, subject)
	}

	if err != nil {
		SecurityEvent(c.r, "authz.denied", "ability", ability, "status", errs.Status(err))
	}

	return err
}
