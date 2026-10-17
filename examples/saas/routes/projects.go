package routes

import (
	"context"

	"github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/authz"
	"github.com/AlexGalhardo/vukem/examples/saas/app/accounts"
	"github.com/AlexGalhardo/vukem/examples/saas/app/billing"
	"github.com/AlexGalhardo/vukem/examples/saas/app/controllers"
	"github.com/AlexGalhardo/vukem/examples/saas/app/models"
	"github.com/AlexGalhardo/vukem/examples/saas/app/policies"
	"github.com/AlexGalhardo/vukem/router"
)

// Projects registers the authorization gate and the projects resource,
// open to signed-in users only.
func Projects(r *router.Router, app *vukem.App) error {
	acc, err := accounts.New(app)
	if err != nil {
		return err
	}

	b, err := billing.New(app)
	if err != nil {
		return err
	}

	// Policies decide with the user loaded by the "web" guard.
	gate := authz.New(func(ctx context.Context) (*models.User, error) {
		u, err := auth.UserFrom[models.User](ctx, "web")
		return &u, err
	})
	if err := gate.Register(policies.All(acc.DB, b)...); err != nil {
		return err
	}

	r.Services().Authorizer = gate

	r.Resource("projects", controllers.NewProjectController(acc.DB)).Use(auth.Required(acc.Web))

	return nil
}
