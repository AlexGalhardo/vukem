package vukem

import (
	"bytes"
	"encoding/json"
	"maps"
	"slices"
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

func TestCodegenTypesCommand(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	build := func(opts ...Option) (*App, error) {
		return New(append(opts, WithLogger(discardLogger()), WithStdout(&out), WithRoutes(openAPIRoutes))...)
	}

	if err := RunMain(
		t.Context(),
		[]string{"app", "codegen:types", "-zod"},
		testSource(nil).Lookup,
		build,
	); err != nil {
		t.Fatal(err)
	}

	if s := out.String(); !strings.Contains(s, "export type PingResponse = PingOut;") ||
		!strings.Contains(s, "PingOutSchema") {
		t.Fatalf("codegen:types = %q", s)
	}
}

func TestServeOpenAPIWithPrefix(t *testing.T) {
	t.Parallel()

	routes := func(r *router.Router, app *App) error {
		ServeOpenAPIWith(app, r, "/api/v1/openapi.json", OpenAPIOptions{PathPrefix: "/api/v1", InProduction: true})
		ServeOpenAPI(app, r, "/openapi.json")
		api.Register(r, "GET /api/v1/ping", api.Op{Name: "ping"},
			func(*web.Ctx, struct{}) (pingOut, error) { return pingOut{Pong: true}, nil })
		api.Register(r, "GET /api/v10/ping", api.Op{Name: "ping10"},
			func(*web.Ctx, struct{}) (pingOut, error) { return pingOut{Pong: true}, nil })
		api.Register(r, "GET /internal/stats", api.Op{Name: "stats"},
			func(*web.Ctx, struct{}) (pingOut, error) { return pingOut{}, nil })

		return nil
	}

	paths := func(t *testing.T, app *App, target string) []string {
		t.Helper()

		rec := doReq(t, app, target)
		if rec.Code != 200 {
			t.Fatalf("GET %s = %d, want 200", target, rec.Code)
		}

		var doc struct {
			Paths map[string]any `json:"paths"`
		}

		if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
			t.Fatal(err)
		}

		return slices.Sorted(maps.Keys(doc.Paths))
	}

	dev := newTestApp(t, WithSource(testSource(nil)), WithRoutes(routes))
	if got := paths(t, dev, "/api/v1/openapi.json"); !slices.Equal(got, []string{"/api/v1/ping"}) {
		t.Errorf("prefixed document paths = %v, want [/api/v1/ping]", got)
	}

	if got := paths(t, dev, "/openapi.json"); !slices.Contains(got, "/internal/stats") {
		t.Errorf("unprefixed document paths = %v, want every route", got)
	}

	prod := newTestApp(t, WithSource(testSource(map[string]string{"APP_ENV": "production"})), WithRoutes(routes))
	if got := paths(t, prod, "/api/v1/openapi.json"); !slices.Equal(got, []string{"/api/v1/ping"}) {
		t.Errorf("opted-in production document paths = %v", got)
	}

	if rec := doReq(t, prod, "/openapi.json"); rec.Code != 404 {
		t.Errorf("production /openapi.json without opt-in = %d, want 404", rec.Code)
	}
}
