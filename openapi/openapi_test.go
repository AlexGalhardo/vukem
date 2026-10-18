package openapi_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/api"
	"github.com/AlexGalhardo/vukem/openapi"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

var update = flag.Bool("update", false, "rewrite golden files")

type author struct {
	orm.Model

	Name string `json:"name"`
}

type product struct {
	orm.Model

	Name      string            `json:"name"`
	Price     float64           `json:"price"`
	Tags      []string          `json:"tags,omitempty"`
	Meta      map[string]string `json:"meta,omitempty"`
	Published *time.Time        `json:"published_at"`
	Author    orm.BelongsTo[author]
	Related   orm.HasMany[product] `json:"related,omitzero"`
	Secret    string               `json:"-"`
	Deleted   orm.NullTime         `json:"deleted_at"`
}

type storeProduct struct {
	Shop   int64    `json:"shop"   validate:"required,positive"`
	Name   string   `json:"name"   validate:"required,min=2,max=80"          doc:"Display name"`
	Email  string   `json:"email"  validate:"email"`
	Status string   `json:"status" validate:"oneof=draft published"`
	Code   string   `json:"code"   validate:"regex=^[A-Z]{2,4}$"`
	Price  float64  `json:"price"  validate:"min=0,max=9999.99"`
	Tags   []string `json:"tags"   validate:"max=5,unique,dive,min=1,max=20"`
	Note   *string  `json:"note"`
}

type listQuery struct {
	Q       string `json:"q"        validate:"max=50"`
	PerPage int    `json:"per_page" validate:"min=1,max=100"`
}

type page struct {
	Data []product `json:"data"`
}

func routes() []router.RouteInfo {
	r := router.New(nil)

	r.Group("/api/v1", func(g *router.Group) {
		api.Register(
			g,
			"GET /products",
			api.Op{Name: "products.index", Summary: "List products", Tags: []string{"products"}},
			func(*web.Ctx, listQuery) (page, error) { return page{}, nil },
		)
		api.Register(g, "POST /shops/{shop}/products", api.Op{
			Name:     "products.store",
			Summary:  "Create a product",
			Tags:     []string{"products"},
			Security: []string{"bearer"},
		}, func(*web.Ctx, storeProduct) (product, error) { return product{}, nil })
		api.Register(g, "DELETE /products/{id}", api.Op{Name: "products.destroy", Deprecated: true},
			func(*web.Ctx, struct{}) (struct{}, error) { return struct{}{}, nil })
		g.Get("/legacy/{path...}", func(*web.Ctx) error { return nil }).Name("legacy")
		g.Post("/webhooks/{provider}", func(*web.Ctx) error { return nil }).Name("webhooks.receive")
		g.Get("/internal", func(*web.Ctx) error { return nil }).Doc(openapi.Skip{})
	})
	r.Get("/", func(*web.Ctx) error { return nil }).Name("home")

	if _, err := r.Handler(); err != nil {
		panic(err)
	}

	return r.Routes()
}

func TestGenerateGolden(t *testing.T) {
	t.Parallel()

	doc := openapi.Generate(routes(), openapi.Info{
		Title: "Shop", Version: "2.1.0", Servers: []string{"https://shop.example"}, PathPrefix: "/api",
	})

	got, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}

	got = append(got, '\n')
	golden := filepath.Join("testdata", "shop.openapi.json")

	if *update {
		if err := os.WriteFile(golden, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(string(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))), string(got)); diff != "" {
		t.Errorf("document (-want +got):\n%s", diff)
	}
}

func TestGenerateDefaultsAndFilters(t *testing.T) {
	t.Parallel()

	doc := openapi.Generate(routes(), openapi.Info{})

	if doc.Info.Title != "API" || doc.Info.Version != "1.0.0" || doc.OpenAPI != "3.1.0" {
		t.Errorf("info = %+v %s", doc.Info, doc.OpenAPI)
	}

	if _, ok := doc.Paths["/"]["get"]; !ok {
		t.Error("root route missing without prefix")
	}

	if _, ok := doc.Paths["/api/v1/internal"]; ok {
		t.Error("Skip route listed")
	}

	if doc.Components.SecuritySchemes["bearer"]["scheme"] != "bearer" {
		t.Errorf("schemes = %v", doc.Components.SecuritySchemes)
	}

	if doc.Paths["/api/v1/legacy/{path}"]["get"].Parameters[0].Name != "path" {
		t.Error("wildcard path param not converted")
	}
}

func TestTypeScriptGolden(t *testing.T) {
	t.Parallel()

	doc := openapi.Generate(routes(), openapi.Info{PathPrefix: "/api"})

	for _, tc := range []struct {
		file string
		zod  bool
	}{{"shop.types.ts", false}, {"shop.zod.ts", true}} {
		got, err := openapi.TypeScript(doc, tc.zod)
		if err != nil {
			t.Fatal(err)
		}

		golden := filepath.Join("testdata", tc.file)
		if *update {
			if err := os.WriteFile(golden, []byte(got), 0o600); err != nil {
				t.Fatal(err)
			}
		}

		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff(string(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))), got); diff != "" {
			t.Errorf("%s (-want +got):\n%s", tc.file, diff)
		}
	}
}

func TestTypeScriptZod4(t *testing.T) {
	t.Parallel()

	got, err := openapi.TypeScript(openapi.Generate(routes(), openapi.Info{PathPrefix: "/api"}), true)
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		snippet string
		want    bool
	}{
		{"z.ZodTypeDef", false},         // removed in zod 4
		{"z.string().datetime(", false}, // deprecated string method in zod 4
		{"z.iso.datetime({ offset: true })", true},
		{"export const ProductsStoreBodySchema: z.ZodType<ProductsStoreBody> =", true}, // inline body
	} {
		if strings.Contains(got, tt.snippet) != tt.want {
			t.Errorf("TypeScript(zod) contains %q = %v, want %v", tt.snippet, !tt.want, tt.want)
		}
	}
}

func TestSchemasRequiredString(t *testing.T) {
	t.Parallel()

	type in struct {
		Name  string  `json:"name"  validate:"required"`
		Code  string  `json:"code"  validate:"required,min=3"`
		Zero  string  `json:"zero"  validate:"required,min=0"`
		Note  *string `json:"note"  validate:"required"`
		Plain string  `json:"plain"`
	}

	props := openapi.NewSchemas().Object(reflect.TypeFor[in](), nil)["properties"].(map[string]any)

	got := map[string]any{}
	for k, v := range props {
		got[k] = v.(openapi.Schema)["minLength"]
	}

	want := map[string]any{"name": 1, "code": 3, "zero": 1, "note": 1, "plain": nil}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("minLength (-want +got):\n%s", diff)
	}
}

func TestPathPrefixMatchesWholeSegments(t *testing.T) {
	t.Parallel()

	r := router.New(nil)
	for _, p := range []string{"/api/v1", "/api/v1/ping", "/api/v10/ping", "/api/v1x"} {
		r.Get(p, func(c *web.Ctx) error { return c.NoContent() })
	}

	if _, err := r.Handler(); err != nil {
		t.Fatal(err)
	}

	for _, prefix := range []string{"/api/v1", "/api/v1/"} {
		doc := openapi.Generate(r.Routes(), openapi.Info{PathPrefix: prefix})
		got := slices.Sorted(maps.Keys(doc.Paths))

		if diff := cmp.Diff([]string{"/api/v1", "/api/v1/ping"}, got); diff != "" {
			t.Errorf("Generate(PathPrefix %q) paths (-want +got):\n%s", prefix, diff)
		}
	}
}

func TestGenerateNamesAndBodiesConsistently(t *testing.T) {
	t.Parallel()

	r := router.New(nil)
	r.Group("/api", func(g *router.Group) {
		g.Name("api.")
		api.Register(g, "GET /ping", api.Op{Name: "ping"},
			func(*web.Ctx, struct{}) (page, error) { return page{}, nil })
		g.Get("/status", func(*web.Ctx) error { return nil }).Name("status")
		g.Post("/hooks", func(*web.Ctx) error { return nil }).Name("hooks")
	})

	if _, err := r.Handler(); err != nil {
		t.Fatal(err)
	}

	doc := openapi.Generate(r.Routes(), openapi.Info{})

	got := map[string]string{}
	for path, item := range doc.Paths {
		for method, op := range item {
			got[method+" "+path] = op.OperationID
		}
	}

	want := map[string]string{
		"get /api/ping":   "ping",
		"get /api/status": "status",
		"post /api/hooks": "hooks",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("operationIds (-want +got):\n%s", diff)
	}

	if rb := doc.Paths["/api/hooks"]["post"].RequestBody; rb == nil || rb.Required {
		t.Errorf("plain POST requestBody = %+v, want an optional body", rb)
	}

	if rb := doc.Paths["/api/status"]["get"].RequestBody; rb != nil {
		t.Errorf("plain GET requestBody = %+v, want none", rb)
	}

	for _, body := range []string{`{"ok":true}`, `<p>ok</p>`, ``} {
		if err := doc.ValidateResponse("POST", "/api/hooks", 200, []byte(body)); err != nil {
			t.Errorf("ValidateResponse(plain route, %q) = %v, want nil", body, err)
		}
	}
}

func TestBuildRejectsUnknownSecurityScheme(t *testing.T) {
	t.Parallel()

	r := router.New(nil)
	api.Register(r, "GET /me", api.Op{Name: "me", Security: []string{"cookie"}},
		func(*web.Ctx, struct{}) (page, error) { return page{}, nil })

	if _, err := r.Handler(); err != nil {
		t.Fatal(err)
	}

	if _, err := openapi.Build(r.Routes(), openapi.Info{}); !errors.Is(err, openapi.ErrUnknownSecurityScheme) {
		t.Fatalf("Build(unknown scheme) error = %v, want ErrUnknownSecurityScheme", err)
	}

	doc, err := openapi.Build(r.Routes(), openapi.Info{SecuritySchemes: map[string]openapi.Schema{
		"cookie": {"type": "apiKey", "in": "cookie", "name": "session"},
	}})
	if err != nil || doc.Components.SecuritySchemes["cookie"]["in"] != "cookie" {
		t.Fatalf("Build(declared scheme) = %v, %v", doc, err)
	}

	defer func() {
		if recover() == nil {
			t.Error("Generate(unknown scheme) did not panic")
		}
	}()

	openapi.Generate(r.Routes(), openapi.Info{})
}

func TestBuildRejectsDuplicateOperationIDs(t *testing.T) {
	t.Parallel()

	r := router.New(nil)
	for _, v := range []string{"v1", "v2"} {
		r.Group("/api/"+v, func(g *router.Group) {
			g.Name("api." + v + ".")
			g.Get("/", func(*web.Ctx) error { return nil }).Name("index")
		})
	}

	if _, err := r.Handler(); err != nil {
		t.Fatal(err)
	}

	_, err := openapi.Build(r.Routes(), openapi.Info{})
	if !errors.Is(err, openapi.ErrDuplicateOperationID) ||
		!strings.Contains(err.Error(), "api.v1.index") || !strings.Contains(err.Error(), "api.v2.index") {
		t.Fatalf("Build(two routes named index) error = %v, want ErrDuplicateOperationID naming both", err)
	}

	if _, err := openapi.Build(r.Routes(), openapi.Info{PathPrefix: "/api/v1"}); err != nil {
		t.Fatalf("Build(one version) error = %v", err)
	}
}
