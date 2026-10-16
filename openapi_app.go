package vukem

import (
	"github.com/AlexGalhardo/vukem/openapi"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

// OpenAPI returns the OpenAPI 3.1 document of the app's routes (title
// APP_NAME, version API_VERSION, filtered by OPENAPI_PREFIX).
func (a *App) OpenAPI() (*openapi.Document, error) {
	if _, err := a.Router.Handler(); err != nil {
		return nil, err
	}

	return openapi.Generate(a.Router.Routes(), openapi.Info{
		Title:      a.Config.Name,
		Version:    a.Config.APIVersion,
		Servers:    []string{a.Config.URL},
		PathPrefix: a.Config.OpenAPIPrefix,
	}), nil
}

// ServeOpenAPI registers GET path (e.g. "/openapi.json") serving the
// document. In production it registers nothing (and returns nil) unless
// OPENAPI_IN_PRODUCTION=true: the document maps the whole API surface.
func ServeOpenAPI(app *App, r *router.Router, path string) *router.Route {
	if app.Config.IsProduction() && !app.Config.OpenAPIInProduction {
		return nil
	}

	return r.Get(path, func(c *web.Ctx) error {
		doc, err := app.OpenAPI()
		if err != nil {
			return err
		}

		return c.JSON(200, doc)
	}).Name("openapi").Doc(openapi.Skip{})
}
