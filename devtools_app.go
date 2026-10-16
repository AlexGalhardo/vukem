package vukem

import (
	"github.com/AlexGalhardo/vukem/devtools"
)

// enableDevtools wires the dashboard: its recorder is the outermost server
// middleware (so timings cover the whole stack), the route middleware
// captures the matched pattern, and the routes live under /_vukem. The
// database, queue and mailer pick it up when they are first built; the
// logger and event bus are wired in New.
func (a *App) enableDevtools(dt *devtools.Devtools) {
	a.Devtools = dt

	a.Router.UseServer(dt.Middleware())
	a.Router.Use(dt.RouteMiddleware())
	dt.Register(a.Router)
}
