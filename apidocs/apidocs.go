// Package apidocs serves an API reference for the app's OpenAPI document
// with Scalar (https://scalar.com). The Scalar bundle is vendored and
// embedded (no CDN), telemetry and remote fonts are off, and the page is
// not served in production unless OPENAPI_IN_PRODUCTION=true.
//
//	vukem.ServeOpenAPI(app, r, "/openapi.json")
//	apidocs.Serve(app, r, "/docs", apidocs.Config{})
package apidocs

import (
	"context"
	_ "embed"
	"errors"
	"flag"
	"html/template"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/middleware"
	"github.com/AlexGalhardo/vukem/openapi"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

// ScalarVersion is the vendored @scalar/api-reference release.
const ScalarVersion = "1.72.3"

// ScalarSHA256 pins the vendored bundle (checked by the tests).
const ScalarSHA256 = "f98a66f9d9613398fd6eb58be8f25594d7e8861e71ea11bca94d67ae9fa5567f"

//go:embed scalar/standalone.js
var scalarJS []byte

// CSP is the page's Content-Security-Policy. It matches the default policy
// except style-src, which allows inline styles because Scalar injects its
// stylesheet at run time without a nonce.
const CSP = "default-src 'self'; script-src 'self' 'nonce-{nonce}'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; font-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'self'; " +
	"frame-ancestors 'none'; form-action 'self'"

// Config tunes the reference page.
type Config struct {
	// SpecURL is the OpenAPI document (default "/openapi.json").
	SpecURL string
	// Title of the page (default APP_NAME + " API").
	Title string
	// Theme is a Scalar theme: default, alternate, moon, purple, solarized,
	// bluePlanet, saturn, kepler, mars, deepSpace, laserwave, none.
	Theme string
	// Layout is "modern" (default) or "classic".
	Layout string
	// Authentication presets the client (Scalar "authentication" option),
	// e.g. {"preferredSecurityScheme": "bearer"}. Never put real secrets here:
	// the page is public wherever it is served.
	Authentication map[string]any
	// HideClientButton hides the "Test request" client.
	HideClientButton bool
}

var page = template.Must(template.New("docs").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex">
<title>{{.Title}}</title>
</head>
<body>
<div id="app"></div>
<script src="{{.Script}}" nonce="{{.Nonce}}"></script>
<script nonce="{{.Nonce}}">Scalar.createApiReference("#app", {{.Options}})</script>
</body>
</html>
`))

// Serve registers GET path (the page) and GET path/scalar.js (the bundle).
// In production it registers nothing and returns false unless
// OPENAPI_IN_PRODUCTION=true.
func Serve(app *vukem.App, r *router.Router, path string, cfg Config) bool {
	if app.Config.IsProduction() && !app.Config.OpenAPIInProduction {
		return false
	}

	if cfg.Title == "" {
		cfg.Title = app.Config.Name + " API"
	}

	Register(r, path, cfg)

	return true
}

// Register adds the routes without the production check (for apps that
// gate them another way).
func Register(r *router.Router, path string, cfg Config) {
	path = strings.TrimSuffix(path, "/")
	script := path + "/scalar.js?v=" + ScalarVersion

	if cfg.SpecURL == "" {
		cfg.SpecURL = "/openapi.json"
	}

	if cfg.Title == "" {
		cfg.Title = "API Reference"
	}

	options := map[string]any{
		"url":              cfg.SpecURL,
		"telemetry":        false,
		"withDefaultFonts": false,
		"agent":            map[string]any{"disabled": true},
		"hideClientButton": cfg.HideClientButton,
	}

	if cfg.Theme != "" {
		options["theme"] = cfg.Theme
	}

	if cfg.Layout != "" {
		options["layout"] = cfg.Layout
	}

	if cfg.Authentication != nil {
		options["authentication"] = cfg.Authentication
	}

	r.Get(path, func(c *web.Ctx) error {
		nonce := middleware.CSPNonce(c.Context())
		if nonce != "" {
			c.Writer().Header().Set("Content-Security-Policy", strings.ReplaceAll(CSP, "{nonce}", nonce))
		}

		c.Writer().Header().Set("Content-Type", "text/html; charset=utf-8")
		c.Writer().Header().Set("Cache-Control", "no-store")

		return page.Execute(c.Writer(), map[string]any{
			"Title": cfg.Title, "Script": script, "Nonce": nonce, "Options": options,
		})
	}).Name("apidocs").Doc(openapi.Skip{})

	r.Get(path+"/scalar.js", func(c *web.Ctx) error {
		h := c.Writer().Header()
		h.Set("Content-Type", "text/javascript; charset=utf-8")
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
		_, err := c.Writer().Write(scalarJS)

		return err
	}).Name("apidocs.script").Doc(openapi.Skip{})
}

// Command returns "openapi:serve": it serves the document and the
// reference on -addr (default localhost:8081) for local previews, whatever
// APP_ENV is. Add it to bootstrap/commands.go.
func Command() vukem.Command {
	var addr *string

	return vukem.CommandFunc{
		CommandName: "openapi:serve",
		Desc:        "Preview the API reference (Scalar) for the OpenAPI document",
		DefineFlags: func(fs *flag.FlagSet) { addr = fs.String("addr", "localhost:8081", "listen address") },
		Fn: func(ctx context.Context, app *vukem.App, _ []string) error {
			doc, err := app.OpenAPI()
			if err != nil {
				return err
			}

			r := router.New(nil)
			r.Get("/openapi.json", func(c *web.Ctx) error { return c.JSON(http.StatusOK, doc) })
			Register(r, "/", Config{Title: app.Config.Name + " API"})

			h, err := r.Handler()
			if err != nil {
				return err
			}

			ln, err := new(net.ListenConfig).Listen(ctx, "tcp", *addr)
			if err != nil {
				return err
			}

			srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}

			go func() {
				<-ctx.Done()
				_ = srv.Shutdown(context.WithoutCancel(ctx))
			}()

			app.Log.InfoContext(ctx, "API reference", "url", "http://"+ln.Addr().String()+"/")

			if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
				return err
			}

			return nil
		},
	}
}
