package openapi_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
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
