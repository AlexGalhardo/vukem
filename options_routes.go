package vukem

import (
	"errors"

	"github.com/AlexGalhardo/vukem/router"
)

// WithRoutes registers routes (and router middleware) after providers
// registered, so handlers can use the services they added. An error stops
// New.
func WithRoutes(fn func(r *router.Router, app *App) error) Option {
	return func(o *options) error { o.routes = append(o.routes, fn); return nil }
}

// WithRoutesAfterBoot registers routes at the end of Boot, after every
// provider's Boot, so handlers can use clients a provider opens in Boot (a
// database or object-store client). The routes exist only once the app
// booted: Run, commands and vukemtest boot first; Handler fails with
// [ErrNotBooted] before that, and Boot fails with [ErrRoutesCompiled] when
// the router was compiled earlier. Global router middleware applies to
// them; groups made in WithRoutes do not. In [KindWarmup], which never
// boots, they run at the end of New so the route table is complete; booted
// services are then still unset (check app.Kind) and a panic is returned
// as an error.
func WithRoutesAfterBoot(fn func(r *router.Router, app *App) error) Option {
	return func(o *options) error { o.bootRoutes = append(o.bootRoutes, fn); return nil }
}

// ErrRoutesCompiled is returned by Boot when routes from
// WithRoutesAfterBoot are pending but the router was already compiled
// (Handler, Router.URL or a request before Boot), so they would never be
// served.
var ErrRoutesCompiled = errors.New("vukem: router compiled before Boot, so routes after boot would be lost; " +
	"call Boot before Handler/Router.URL; avoid URL() inside WithRoutes")

// ErrNotBooted is returned by Handler when routes from WithRoutesAfterBoot
// are pending because Boot has not run: serving then would answer 404 for
// them.
var ErrNotBooted = errors.New("vukem: routes after boot are pending; call Boot before Handler")
