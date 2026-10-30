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

type lookupProduct struct {
	ID    int64  `json:"id"    validate:"required,positive" doc:"Product identifier" example:"42"`
	Trace string `json:"trace"                              doc:"Caller trace id"    example:"abc-123"`
}

// lookupParams builds the parameters of a route whose path parameter and
// query parameter both carry a doc and an example tag.
func lookupParams(t *testing.T) map[string]openapi.Parameter {
	t.Helper()

	r := router.New(nil)
	api.Register(r, "GET /products/{id}", api.Op{Name: "products.show"},
		func(*web.Ctx, lookupProduct) (page, error) { return page{}, nil })

	if _, err := r.Handler(); err != nil {
		t.Fatal(err)
	}

	doc, err := openapi.Build(r.Routes(), openapi.Info{})
	if err != nil {
		t.Fatal(err)
	}

	params := map[string]openapi.Parameter{}
	for _, p := range doc.Paths["/products/{id}"]["get"].Parameters {
		params[p.Name] = p
	}

	return params
}

func TestPathParameterDescriptionFromDocTag(t *testing.T) {
	t.Parallel()

	params := lookupParams(t)
	if got := params["id"].Description; got != "Product identifier" {
		t.Errorf("path parameter id description = %q, want the doc tag", got)
	}

	if got := params["trace"].Description; got != "Caller trace id" {
		t.Errorf("query parameter trace description = %q, want the doc tag", got)
	}
}

func TestParameterExampleFromTag(t *testing.T) {
	t.Parallel()

	params := lookupParams(t)
	if got := params["id"].Example; got != int64(42) {
		t.Errorf("path parameter id example = %#v, want int64(42)", got)
	}

	if got := params["trace"].Example; got != "abc-123" {
		t.Errorf("query parameter trace example = %#v, want \"abc-123\"", got)
	}
}

type exampleAuthor struct {
	Name string `json:"name"`
}

type exampleBody struct {
	Name   string        `json:"name"   validate:"required" doc:"Display name" example:"Widget"`
	Price  float64       `json:"price"                                         example:"9.99"`
	Active bool          `json:"active"                                        example:"true"`
	Count  int           `json:"count"                                         example:"7"`
	Tags   []string      `json:"tags"                                          example:"[\"new\",\"sale\"]"`
	Author exampleAuthor `json:"author"                     doc:"Owner"        example:"{\"name\":\"ACME\"}"`
}

func TestBodyExamplesFromTags(t *testing.T) {
	t.Parallel()

	r := router.New(nil)
	api.Register(r, "POST /products", api.Op{Name: "products.create"},
		func(*web.Ctx, exampleBody) (page, error) { return page{}, nil })

	if _, err := r.Handler(); err != nil {
		t.Fatal(err)
	}

	doc, err := openapi.Build(r.Routes(), openapi.Info{})
	if err != nil {
		t.Fatal(err)
	}

	body := doc.Paths["/products"]["post"].RequestBody
	if body == nil {
		t.Fatal("POST /products has no request body")
	}

	want := `{
		"type": "object",
		"required": ["name"],
		"properties": {
			"name":   {"type": "string", "minLength": 1, "description": "Display name", "examples": ["Widget"]},
			"price":  {"type": "number", "format": "double", "examples": [9.99]},
			"active": {"type": "boolean", "examples": [true]},
			"count":  {"type": "integer", "examples": [7]},
			"tags":   {"type": "array", "items": {"type": "string"}, "examples": [["new", "sale"]]},
			"author": {
				"allOf":       [{"$ref": "#/components/schemas/exampleAuthor"}],
				"description": "Owner",
				"examples":    [{"name": "ACME"}]
			}
		}
	}`
	if diff := cmp.Diff(asAny(t, want), asAny(t, body.Content["application/json"].Schema)); diff != "" {
		t.Errorf("request schema (-want +got):\n%s", diff)
	}
}

// asAny normalises a JSON string or a marshalable value into plain
// any-typed JSON, so expectations can be written as literal JSON.
func asAny(t *testing.T, v any) any {
	t.Helper()

	raw, ok := v.(string)
	if !ok {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}

		raw = string(b)
	}

	var out any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}

	return out
}

func TestMediaTypeAndParameterCarryExamples(t *testing.T) {
	t.Parallel()

	resp := openapi.Response{
		Description: "Created",
		Content: map[string]openapi.MediaType{"application/json": {
			Schema:  openapi.Schema{"type": "object"},
			Example: map[string]any{"ok": true},
			Examples: map[string]openapi.Example{"rejected": {
				Summary:     "Rejected",
				Description: "the gateway refused the charge",
				Value:       map[string]any{"ok": false},
			}},
		}},
	}

	wantResp := `{
		"description": "Created",
		"content": {"application/json": {
			"schema":   {"type": "object"},
			"example":  {"ok": true},
			"examples": {"rejected": {
				"summary":     "Rejected",
				"description": "the gateway refused the charge",
				"value":       {"ok": false}
			}}
		}}
	}`
	if diff := cmp.Diff(asAny(t, wantResp), asAny(t, resp)); diff != "" {
		t.Errorf("response (-want +got):\n%s", diff)
	}

	param := openapi.Parameter{
		Name: "id", In: "path", Required: true, Schema: openapi.Schema{"type": "string"},
		Example:  "p_42",
		Examples: map[string]openapi.Example{"legacy": {Value: "42"}},
	}

	wantParam := `{
		"name": "id", "in": "path", "required": true, "schema": {"type": "string"},
		"example": "p_42", "examples": {"legacy": {"value": "42"}}
	}`
	if diff := cmp.Diff(asAny(t, wantParam), asAny(t, param)); diff != "" {
		t.Errorf("parameter (-want +got):\n%s", diff)
	}
}

func TestBuildRejectsPrefixMatchingNoRoute(t *testing.T) {
	t.Parallel()

	r := router.New(nil)
	r.Get("/api/v1/ping", func(c *web.Ctx) error { return c.NoContent() })

	if _, err := r.Handler(); err != nil {
		t.Fatal(err)
	}

	// Git Bash rewrites OPENAPI_PREFIX=/api/v1 into an absolute Windows path.
	_, err := openapi.Build(r.Routes(), openapi.Info{PathPrefix: "C:/Program Files/Git/api/v1"})
	if !errors.Is(err, openapi.ErrEmptyDocument) {
		t.Fatalf("Build(prefix matching no route) error = %v, want ErrEmptyDocument", err)
	}

	if _, err := openapi.Build(r.Routes(), openapi.Info{PathPrefix: "/api/v1"}); err != nil {
		t.Fatalf("Build(matching prefix) error = %v", err)
	}

	empty := router.New(nil)
	if _, err := empty.Handler(); err != nil {
		t.Fatal(err)
	}

	doc, err := openapi.Build(empty.Routes(), openapi.Info{PathPrefix: "/api"})
	if err != nil || len(doc.Paths) != 0 {
		t.Fatalf("Build(router with no routes) = %v, %v, want an empty document and no error", doc, err)
	}

	defer func() {
		if recover() == nil {
			t.Error("Generate(prefix matching no route) did not panic")
		}
	}()

	openapi.Generate(r.Routes(), openapi.Info{PathPrefix: "/nope"})
}

// An endpoint whose body shape varies by a discriminator field: the
// payload is described as an open object rather than modelled field by
// field, and that has to survive into the document and into both
// generators. Before z.JSON/z.JSONObject there was no way to express it:
// map[string]any panicked in z.For and json.RawMessage was taken for its
// underlying []byte, so the document claimed "array of integers".
func TestDynamicPayloadReachesTheGeneratedClients(t *testing.T) {
	t.Parallel()

	type submitIn struct {
		Tool    string         `json:"tool"    validate:"required" doc:"Which tool to run"`
		Options map[string]any `json:"options"                     doc:"Options for the chosen tool"`
		Extra   any            `json:"extra"`
	}

	type submitOut struct {
		ID string `json:"id"`
	}

	r := router.New(nil)
	r.Group("/api/v1", func(g *router.Group) {
		api.Register(g, "POST /conversions", api.Op{Name: "conversions.store", Summary: "Submit a conversion"},
			func(*web.Ctx, submitIn) (submitOut, error) { return submitOut{}, nil })
	})

	if _, err := r.Handler(); err != nil {
		t.Fatal(err)
	}

	doc := openapi.Generate(r.Routes(), openapi.Info{PathPrefix: "/api"})

	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}

	var parsed struct {
		Paths map[string]struct {
			Post struct {
				RequestBody struct {
					Content map[string]struct {
						Schema map[string]any `json:"schema"`
					} `json:"content"`
				} `json:"requestBody"`
			} `json:"post"`
		} `json:"paths"`
	}

	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}

	body := parsed.Paths["/api/v1/conversions"].Post.RequestBody.Content["application/json"].Schema
	if body == nil {
		t.Fatalf("no request body in:\n%s", raw)
	}

	props, _ := body["properties"].(map[string]any)

	// An open object, with the doc comment carried over: the description is
	// the only thing that tells a reader what belongs in the value.
	want := map[string]any{
		"type":                 "object",
		"additionalProperties": map[string]any{},
		"description":          "Options for the chosen tool",
	}
	if diff := cmp.Diff(want, props["options"]); diff != "" {
		t.Errorf("options schema diff (-want +got):\n%s", diff)
	}

	// Any JSON value is the empty schema, which is how OpenAPI 3.1 spells
	// "anything".
	if diff := cmp.Diff(map[string]any{}, props["extra"]); diff != "" {
		t.Errorf("extra schema diff (-want +got):\n%s", diff)
	}

	// What the clients make of it: a typed discriminator next to an open
	// payload, never a number array.
	ts, err := openapi.TypeScript(doc, false)
	if err != nil {
		t.Fatal(err)
	}

	zod, err := openapi.TypeScript(doc, true)
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		out, name, snippet string
		want               bool
	}{
		{ts, "ts", "options?: Record<string, unknown>;", true},
		{ts, "ts", "extra?: unknown;", true},
		{ts, "ts", "number[]", false},
		{zod, "zod", "options: z.record(z.string(), z.unknown()).optional()", true},
		{zod, "zod", "extra: z.unknown().optional()", true},
		{zod, "zod", "z.array(z.int())", false},
	} {
		if strings.Contains(tt.out, tt.snippet) != tt.want {
			t.Errorf("%s contains %q = %v, want %v\n%s", tt.name, tt.snippet, !tt.want, tt.want, tt.out)
		}
	}
}

// VF-053 (HexaScore): a license, a default security requirement and a
// 4XX problem response for plain routes, and error components only when
// an operation references them, so linters (Redocly recommended) pass.
func TestBuildLicenseSecurityAndErrorsForPlainRoutes(t *testing.T) {
	t.Parallel()

	r := router.New(nil)
	r.Get("/me", func(*web.Ctx) error { return nil }).Name("me")
	r.Post("/login", func(*web.Ctx) error { return nil }).Name("login").Doc(openapi.Public{})

	if _, err := r.Handler(); err != nil {
		t.Fatal(err)
	}

	info := openapi.Info{
		License:         &openapi.License{Name: "MIT", Identifier: "MIT"},
		Security:        []string{"session"},
		SecuritySchemes: map[string]openapi.Schema{"session": {"type": "apiKey", "in": "cookie", "name": "session"}},
	}

	doc, err := openapi.Build(r.Routes(), info)
	if err != nil {
		t.Fatal(err)
	}

	if l := doc.Info.License; l == nil || l.Name != "MIT" || l.Identifier != "MIT" {
		t.Errorf("license = %+v", l)
	}

	me := doc.Paths["/me"]["get"]
	if diff := cmp.Diff([]map[string][]string{{"session": {}}}, me.Security); diff != "" {
		t.Errorf("/me security (-want +got):\n%s", diff)
	}

	if me.Responses["4XX"].Ref != "#/components/responses/Problem" {
		t.Errorf("/me responses = %v, want a 4XX Problem", me.Responses)
	}

	if login := doc.Paths["/login"]["post"]; login.Security == nil || len(login.Security) != 0 {
		t.Errorf("/login security = %#v, want an explicit empty list", login.Security)
	}

	if _, ok := doc.Components.Responses["ValidationError"]; ok {
		t.Error("ValidationError emitted while no operation references it")
	}

	if _, ok := doc.Components.Responses["Problem"]; !ok {
		t.Error("Problem missing while referenced")
	}

	for _, bad := range []openapi.Info{
		{License: &openapi.License{}},
		{License: &openapi.License{Name: "MIT", Identifier: "MIT", URL: "https://opensource.org/license/mit"}},
		{Security: []string{"nope"}},
	} {
		if _, err := openapi.Build(r.Routes(), bad); err == nil {
			t.Errorf("Build(%+v) = nil error", bad)
		}
	}
}

func TestStripPrefix(t *testing.T) {
	t.Parallel()

	doc, err := openapi.Build(routes(), openapi.Info{
		Servers: []string{"https://api.shop.example/v1"}, StripPrefix: "/api/v1/",
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := doc.Paths["/legacy/{path}"]; !ok {
		t.Errorf("paths = %v, want /api/v1 stripped", slices.Sorted(maps.Keys(doc.Paths)))
	}

	for p := range doc.Paths {
		if strings.HasPrefix(p, "/api/") || p == "/" {
			t.Errorf("path %q kept, want only routes under /api/v1, stripped", p)
		}
	}

	// Requests still carry the router path; validation strips it too.
	if err := doc.ValidateResponse("POST", "/api/v1/webhooks/stripe", 200, []byte(`{}`)); err != nil {
		t.Errorf("ValidateResponse(router path) = %v, want nil", err)
	}

	if err := doc.ValidateResponse("GET", "/", 200, nil); !errors.Is(err, openapi.ErrNoOperation) {
		t.Errorf("ValidateResponse(/) = %v, want ErrNoOperation", err)
	}

	if _, err := openapi.Build(
		routes(),
		openapi.Info{StripPrefix: "/nowhere"},
	); !errors.Is(
		err,
		openapi.ErrEmptyDocument,
	) {
		t.Errorf("Build(StripPrefix /nowhere) = %v, want ErrEmptyDocument", err)
	}
}
