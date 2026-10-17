package routes

import (
	"time"

	"github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/api"
	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/examples/api/app/accounts"
	"github.com/AlexGalhardo/vukem/examples/api/app/controllers/apiv1"
	"github.com/AlexGalhardo/vukem/middleware"
	"github.com/AlexGalhardo/vukem/ratelimit"
	"github.com/AlexGalhardo/vukem/router"
)

// Tasks registers the typed /api/v1/tasks resource: token required,
// 60 requests per minute per token, described in the OpenAPI document.
func Tasks(r *router.Router, app *vukem.App) error {
	acc, err := accounts.New(app)
	if err != nil {
		return err
	}

	db, err := app.DB()
	if err != nil {
		return err
	}

	tasks := apiv1.NewTasks(db)
	limit := ratelimit.Middleware(ratelimit.NewMemory(60, time.Minute), ratelimit.ByToken)
	bearer := []string{"bearer"}
	tags := []string{"tasks"}

	r.Group("/api/v1", func(g *router.Group) {
		g.Use(middleware.JSONOnly(), limit, auth.Required(acc.API))

		api.Register(g, "GET /tasks", api.Op{
			Name: "tasks.index", Summary: "List your tasks", Tags: tags, Security: bearer,
		}, tasks.Index)
		api.Register(g, "POST /tasks", api.Op{
			Name: "tasks.store", Summary: "Create a task", Tags: tags, Security: bearer,
		}, tasks.Store)
		api.Register(g, "GET /tasks/{task}", api.Op{
			Name: "tasks.show", Summary: "Show a task", Tags: tags, Security: bearer,
		}, tasks.Show)
		api.Register(g, "PUT /tasks/{task}", api.Op{
			Name: "tasks.update", Summary: "Replace a task", Tags: tags, Security: bearer,
		}, tasks.Update)
		api.Register(g, "DELETE /tasks/{task}", api.Op{
			Name: "tasks.destroy", Summary: "Delete a task", Tags: tags, Security: bearer,
		}, tasks.Destroy)
	}).Name("api.v1.")

	// Not registered in production unless OPENAPI_IN_PRODUCTION=true.
	vukem.ServeOpenAPI(app, r, "/openapi.json")

	return nil
}
