package vukem

import (
	"github.com/AlexGalhardo/vukem/openapi"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

// OpenAPI returns the OpenAPI 3.1 document of the app's routes (title
// APP_NAME, version API_VERSION, filtered by OPENAPI_PREFIX).
func (a *App) OpenAPI() (*openapi.Document, error) {
	return a.openAPI(a.Config.OpenAPIPrefix)
}

func (a *App) openAPI(prefix string) (*openapi.Document, error) {
	if _, err := a.Router.Handler(); err != nil {
		return nil, err
	}

	return openapi.Generate(a.Router.Routes(), openapi.Info{
		Title:      a.Config.Name,
		Version:    a.Config.APIVersion,
		Servers:    []string{a.Config.URL},
		PathPrefix: prefix,
	}), nil
}

// OpenAPIOptions tunes [ServeOpenAPIWith].
type OpenAPIOptions struct {
	// PathPrefix keeps only routes under it, e.g. "/api/v1" (default
	// Config.OpenAPIPrefix, OPENAPI_PREFIX).
	PathPrefix string
	// InProduction serves this document in production too. Set it for a
	// public API; OPENAPI_IN_PRODUCTION=true does the same for every
	// document and API reference page.
	InProduction bool
}

// ServeOpenAPI registers GET path (e.g. "/openapi.json") serving the
// document. In production it registers nothing (and returns nil) unless
// OPENAPI_IN_PRODUCTION=true: the document maps the whole API surface.
func ServeOpenAPI(app *App, r *router.Router, path string) *router.Route {
	route := ServeOpenAPIWith(app, r, path, OpenAPIOptions{})
	if route != nil {
		route.Name("openapi")
	}

	return route
}

// ServeOpenAPIWith is [ServeOpenAPI] with options: a document per path
// prefix (one per API version) and a per-document production opt-in. The
// route is unnamed, so several documents can coexist; name it if needed.
//
//	vukem.ServeOpenAPIWith(app, r, "/api/v1/openapi.json",
//		vukem.OpenAPIOptions{PathPrefix: "/api/v1", InProduction: true})
func ServeOpenAPIWith(app *App, r *router.Router, path string, opts OpenAPIOptions) *router.Route {
	if app.Config.IsProduction() && !app.Config.OpenAPIInProduction && !opts.InProduction {
		return nil
	}

	prefix := opts.PathPrefix
	if prefix == "" {
		prefix = app.Config.OpenAPIPrefix
	}

	return r.Get(path, func(c *web.Ctx) error {
		doc, err := app.openAPI(prefix)
		if err != nil {
			return err
		}

		return c.JSON(200, doc)
	}).Doc(openapi.Skip{})
}
