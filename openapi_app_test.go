package vukem

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/api"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

type pingOut struct {
	Pong bool `json:"pong"`
}

func openAPIRoutes(r *router.Router, app *App) error {
	ServeOpenAPI(app, r, "/openapi.json")
	api.Register(r, "GET /api/ping", api.Op{Name: "ping"},
		func(*web.Ctx, struct{}) (pingOut, error) { return pingOut{Pong: true}, nil })

	return nil
}

func TestServeOpenAPI(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	app := newTestApp(t, WithSource(testSource(map[string]string{
		"APP_NAME": "Shop", "API_VERSION": "3.0.0",
		"OPENAPI_PREFIX": "/api",
	})), WithStdout(&out), WithRoutes(openAPIRoutes))

	rec := doReq(t, app, "/openapi.json")
	if rec.Code != 200 {
		t.Fatalf("GET /openapi.json = %d", rec.Code)
	}

	var doc struct {
		Info struct {
			Title   string `json:"title"`
			Version string `json:"version"`
		} `json:"info"`
		Paths map[string]any `json:"paths"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}

	if doc.Info.Title != "Shop" || doc.Info.Version != "3.0.0" || len(doc.Paths) != 1 || doc.Paths["/api/ping"] == nil {
		t.Fatalf("doc = %+v", doc)
	}

	if err := openAPIExportCommand().Run(t.Context(), app, nil); err != nil ||
		!strings.Contains(out.String(), `"/api/ping"`) {
		t.Fatalf("openapi:export = %v %q", err, out.String())
	}
}

func TestSecurity_A02_OpenAPIOffInProduction(t *testing.T) {
	t.Parallel()

	prod := map[string]string{"APP_ENV": "production"}

	if rec := doReq(
		t,
		newTestApp(t, WithSource(testSource(prod)), WithRoutes(openAPIRoutes)),
		"/openapi.json",
	); rec.Code != 404 {
		t.Fatalf("production /openapi.json = %d, want 404", rec.Code)
	}

	prod["OPENAPI_IN_PRODUCTION"] = "true"
	if rec := doReq(
		t,
		newTestApp(t, WithSource(testSource(prod)), WithRoutes(openAPIRoutes)),
		"/openapi.json",
	); rec.Code != 200 {
		t.Fatalf("opted-in /openapi.json = %d, want 200", rec.Code)
	}
}
