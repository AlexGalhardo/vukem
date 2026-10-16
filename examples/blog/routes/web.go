// Package routes declares the HTTP routes.
package routes

import (
	"context"

	"github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/authz"
	"github.com/AlexGalhardo/vukem/examples/blog/app/accounts"
	"github.com/AlexGalhardo/vukem/examples/blog/app/controllers"
	"github.com/AlexGalhardo/vukem/examples/blog/app/models"
	"github.com/AlexGalhardo/vukem/examples/blog/app/policies"
	"github.com/AlexGalhardo/vukem/examples/blog/resources"
	"github.com/AlexGalhardo/vukem/middleware"
	"github.com/AlexGalhardo/vukem/router"
)

// Web registers the browser routes. vukem route:list shows them.
func Web(r *router.Router, app *vukem.App) error {
	if err := vukem.DefaultMiddleware(app, r); err != nil {
		return err
	}

	r.UseServer(middleware.StaticFS(resources.Public(), middleware.StaticConfig{Prefix: "/"}))

	acc, err := accounts.New(app)
	if err != nil {
		return err
	}

	// Policies decide with the user loaded by the "web" guard.
	gate := authz.New(func(ctx context.Context) (*models.User, error) {
		u, err := auth.UserFrom[models.User](ctx, "web")
		return &u, err
	})
	if err := gate.Register(policies.All()...); err != nil {
		return err
	}

	r.Services().Authorizer = gate

	posts := controllers.NewPostController(acc.DB)
	optional, required := auth.Optional(acc.Web), auth.Required(acc.Web)

	r.Get("/", controllers.Home).Name("home")
	r.Resource("posts", posts).Only("index", "show").Use(optional)
	r.Resource("posts", posts).Except("index", "show").Use(required)
	r.Post("/posts/{post}/comments", posts.Comment).Name("posts.comments.store").Use(required)

	return nil
}
