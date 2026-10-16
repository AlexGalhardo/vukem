package graphql

import (
	"crypto/rand"
	_ "embed"
	"html/template"
	"net/http"
	"strings"

	"github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/middleware"
	"github.com/AlexGalhardo/vukem/openapi"
	"github.com/AlexGalhardo/vukem/router"
)

// Registrar is a router.Router or router.Group.
type Registrar interface {
	Handle(pattern string, h http.Handler) *router.Route
}

// Config tunes Serve.
type Config struct {
	HandlerConfig

	// DisableExplorer turns the explorer page off in development too.
	DisableExplorer bool
	// InsecureExplorerInProduction serves the explorer page in production.
	InsecureExplorerInProduction bool
	// InsecureIntrospectionInProduction allows __schema and __type in
	// production (they are rejected there by default).
	InsecureIntrospectionInProduction bool
}

// Serve mounts the GraphQL endpoint at path (GET and POST) and, outside
// production, an explorer page at path + "/explorer". In production the
// explorer is not registered and introspection is rejected unless the
// Insecure… options opt in.
//
//	graphql.Serve(app, r, "/graphql", schema, graphql.Config{})
func Serve(app *vukem.App, r Registrar, path string, s *Schema, cfg Config) {
	path = "/" + strings.Trim(path, "/")
	prod := app.Config.IsProduction()

	if prod && !cfg.InsecureIntrospectionInProduction {
		cfg.DisableIntrospection = true
	}

	h := s.Handler(cfg.HandlerConfig)
	r.Handle("GET "+path, h).Name("graphql").Doc(openapi.Skip{})
	r.Handle("POST "+path, h).Doc(openapi.Skip{})

	if cfg.DisableExplorer || (prod && !cfg.InsecureExplorerInProduction) {
		return
	}

	r.Handle("GET "+path+"/explorer", Explorer(path)).Name("graphql.explorer").Doc(openapi.Skip{})
}

//go:embed explorer.html
var explorerHTML string

var explorerPage = template.Must(template.New("explorer").Parse(explorerHTML))

// ExplorerCSP is the explorer page's Content-Security-Policy: scripts and
// styles only from this page (nonce), requests only to this origin.
const ExplorerCSP = "default-src 'none'; script-src 'nonce-{nonce}'; style-src 'nonce-{nonce}'; " +
	"connect-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// Explorer returns a dependency-free page for writing and running
// queries against endpoint, with a schema browser fed by introspection.
// It uses the request's CSP nonce from middleware.SecureHeaders when
// present and sets its own policy. Serve mounts it outside production.
func Explorer(endpoint string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nonce := middleware.CSPNonce(r.Context())
		if nonce == "" {
			nonce = newNonce()
		}

		h := w.Header()
		h.Set("Content-Security-Policy", strings.ReplaceAll(ExplorerCSP, "{nonce}", nonce))
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("Cache-Control", "no-store")

		if err := explorerPage.Execute(w, map[string]any{
			"Endpoint": endpoint, "Nonce": nonce, "Introspection": IntrospectionQuery,
		}); err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
	})
}

// newNonce is used when SecureHeaders did not set a request nonce.
func newNonce() string { return rand.Text() }
