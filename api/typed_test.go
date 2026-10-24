package api_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/api"
	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/openapi"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
	"github.com/AlexGalhardo/vukem/z"
)

type storeProduct struct {
	ShopID int    `json:"shop"  validate:"required,min=1"`
	Name   string `json:"name"  validate:"required,max=20"`
	Price  int    `json:"price" validate:"min=0"`
}

type productJSON struct {
	ID    int    `json:"id"`
	Shop  int    `json:"shop"`
	Name  string `json:"name"`
	Price int    `json:"price"`
}

type listQuery struct {
	Q string `json:"q"`
}

func typedRouter() *router.Router {
	r := router.New(nil)

	r.Group("/shops/{shop}", func(g *router.Group) {
		api.Register(g, "POST /products", api.Op{Name: "products.store", Tags: []string{"products"}},
			func(_ *web.Ctx, in storeProduct) (productJSON, error) {
				return productJSON{ID: 7, Shop: in.ShopID, Name: in.Name, Price: in.Price}, nil
			})
	})
	api.Register(r, "GET /products", api.Op{},
		func(_ *web.Ctx, in listQuery) ([]productJSON, error) { return []productJSON{{Name: in.Q}}, nil })
	api.Register(r, "DELETE /products/{id}", api.Op{},
		func(_ *web.Ctx, _ struct{}) (struct{}, error) { return struct{}{}, nil })
	api.Register(r, "GET /fail", api.Op{},
		func(_ *web.Ctx, _ struct{}) (productJSON, error) { return productJSON{}, errs.ErrConflict })

	return r
}

func TestRegisterBindsValidatesAndEncodes(t *testing.T) {
	t.Parallel()

	h := typedRouter()

	tests := []struct {
		name, method, target, body string
		status                     int
		want                       string
	}{
		{
			"path wins over body", "POST", "/shops/3/products", `{"shop":99,"name":"Pen","price":2}`,
			201, `{"id":7,"shop":3,"name":"Pen","price":2}`,
		},
		{"validation", "POST", "/shops/3/products", `{"price":-1}`, 422, `"name"`},
		{"bad path param", "POST", "/shops/abc/products", `{"name":"Pen"}`, 422, `"shop"`},
		{"query input", "GET", "/products?q=pen", "", 200, `[{"id":0,"shop":0,"name":"pen","price":0}]`},
		{"no content", "DELETE", "/products/1", "", 204, ""},
		{"handler error", "GET", "/fail", "", 409, ""},
		{"malformed json", "POST", "/shops/3/products", `{`, 400, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			r := httptest.NewRequestWithContext(t.Context(), tt.method, tt.target, strings.NewReader(tt.body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Accept", "application/json")
			h.ServeHTTP(rec, r)

			if rec.Code != tt.status || !strings.Contains(rec.Body.String(), tt.want) {
				t.Fatalf(
					"%s %s = %d %s, want %d containing %s",
					tt.method,
					tt.target,
					rec.Code,
					rec.Body,
					tt.status,
					tt.want,
				)
			}
		})
	}
}

func TestRegisterAttachesOperation(t *testing.T) {
	t.Parallel()

	routes := typedRouter().Routes()

	op, ok := routes[0].Doc.(*api.Operation)
	if !ok {
		t.Fatalf("Doc = %T", routes[0].Doc)
	}

	if routes[0].Name != "products.store" {
		t.Errorf("name = %q", routes[0].Name)
	}

	got := []any{op.Method, op.Path, op.Params, op.In, op.Out, op.Tags}
	want := []any{
		"POST", "/shops/{shop}/products",
		[]string{"shop"},
		reflect.TypeFor[storeProduct](), reflect.TypeFor[productJSON](),
		[]string{"products"},
	}

	if diff := cmp.Diff(want, got, cmp.Comparer(func(a, b reflect.Type) bool { return a == b })); diff != "" {
		t.Errorf("operation (-want +got):\n%s", diff)
	}
}

func TestRegisterRejectsBadPattern(t *testing.T) {
	t.Parallel()

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("no panic")
		}
	}()

	api.Register(router.New(nil), "/products", api.Op{}, func(*web.Ctx, struct{}) (struct{}, error) {
		return struct{}{}, errors.New("unreachable")
	})
}

type checkedJSON struct {
	Status string `json:"status" validate:"required,oneof=open closed"`
	Data   []int  `json:"data"   validate:"required"`
}

func TestRegisterValidateResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		check    bool // web.Services.CheckResponses
		validate bool // Op.ValidateResponse
		out      checkedJSON
		status   int
	}{
		{"valid", true, true, checkedJSON{Status: "open", Data: []int{}}, 200},
		{"mismatch fails loudly", true, true, checkedJSON{Status: "secret-value", Data: []int{}}, 500},
		{"nil list is null", true, true, checkedJSON{Status: "open"}, 500},
		{"production skips the check", false, true, checkedJSON{Status: "secret-value"}, 200},
		{"not opted in", true, false, checkedJSON{Status: "secret-value"}, 200},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := router.New(&web.Services{CheckResponses: tt.check})
			api.Register(r, "GET /checked", api.Op{ValidateResponse: tt.validate},
				func(*web.Ctx, struct{}) (checkedJSON, error) { return tt.out, nil })

			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), "GET", "/checked", nil)
			req.Header.Set("Accept", "application/json")
			r.ServeHTTP(rec, req)

			if rec.Code != tt.status {
				t.Fatalf("GET /checked = %d %s, want %d", rec.Code, rec.Body, tt.status)
			}

			if tt.status == 500 && strings.Contains(rec.Body.String(), "secret-value") {
				t.Errorf("500 body leaks the response: %s", rec.Body)
			}
		})
	}
}

func TestRegisterValidateResponseNeedsStruct(t *testing.T) {
	t.Parallel()

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("no panic")
		}
	}()

	api.Register(router.New(nil), "GET /list", api.Op{ValidateResponse: true},
		func(*web.Ctx, struct{}) ([]checkedJSON, error) { return nil, nil })
}

func TestRegisterLocalizesValidation(t *testing.T) {
	t.Parallel()

	r := router.New(nil)
	api.Register(r, "POST /products", api.Op{},
		func(_ *web.Ctx, in storeProduct) (productJSON, error) { return productJSON{Name: in.Name}, nil })

	localize := func(is z.Issues) z.Issues {
		return is.Localize(func(key string) (string, bool) {
			if key == "required" {
				return "{field} é obrigatório", true
			}

			return "", false
		})
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(web.WithIssueLocalizer(t.Context(), localize),
		"POST", "/products", strings.NewReader(`{"shop":1}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	r.ServeHTTP(rec, req)

	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "name é obrigatório") {
		t.Fatalf("POST /products = %d %s, want 422 with the localized message", rec.Code, rec.Body)
	}
}

// createKey mirrors the real case of A91: `validate` tags cover the static
// rules, the ability allowlist is only known at runtime.
type createKey struct {
	Name      string   `json:"name"      validate:"required,max=20"`
	Abilities []string `json:"abilities" validate:"required"`
}

type keyJSON struct {
	Name string `json:"name"`
}

// runtimeKeySchema is a schema api.Register cannot express: a bound computed
// from a value and a cross-field refinement.
func runtimeKeySchema(allowed []string) z.Schema[createKey] {
	return z.Struct[createKey](z.Shape{
		"name":      z.String().Min(1).Max(20),
		"abilities": z.Slice(z.String()).Min(1).Max(len(allowed)),
	}).Refine(func(k createKey) error {
		if slices.Contains(k.Abilities, "write") && !strings.HasPrefix(k.Name, "svc-") {
			return z.FieldError("name", "a write key must be named svc-*")
		}

		return nil
	})
}

func keyHandler(_ *web.Ctx, in createKey) (keyJSON, error) { return keyJSON{Name: in.Name}, nil }

func postKey(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/keys", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	h.ServeHTTP(rec, req)

	return rec
}

func TestRegisterWithUsesTheGivenSchema(t *testing.T) {
	t.Parallel()

	r := router.New(nil)
	api.RegisterWith(r, "POST /keys", api.Op{Name: "keys.store"},
		runtimeKeySchema([]string{"read", "write"}), keyHandler)

	tests := []struct {
		name, body string
		status     int
		want       string
	}{
		{"accepted", `{"name":"svc-sync","abilities":["read","write"]}`, 201, `{"name":"svc-sync"}`},
		{"runtime max", `{"name":"ci","abilities":["read","write","admin"]}`, 422, `"abilities"`},
		{"refine", `{"name":"ci","abilities":["write"]}`, 422, "a write key must be named svc-*"},
		{"tag rules still apply through the schema", `{"name":"","abilities":["read"]}`, 422, `"name"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := postKey(t, r, tt.body)
			if rec.Code != tt.status || !strings.Contains(rec.Body.String(), tt.want) {
				t.Fatalf(
					"POST /keys %s = %d %s, want %d containing %s",
					tt.body,
					rec.Code,
					rec.Body,
					tt.status,
					tt.want,
				)
			}
		})
	}
}

// TestRegisterKeepsTagValidation pins the behaviour RegisterWith must not
// change: Register still validates with In's tags only, so a request the
// runtime schema rejects (three abilities, no svc- prefix) is accepted.
func TestRegisterKeepsTagValidation(t *testing.T) {
	t.Parallel()

	r := router.New(nil)
	api.Register(r, "POST /keys", api.Op{Name: "keys.store"}, keyHandler)

	rec := postKey(t, r, `{"name":"ci","abilities":["read","write","admin"]}`)
	if rec.Code != 201 || !strings.Contains(rec.Body.String(), `{"name":"ci"}`) {
		t.Fatalf("POST /keys = %d %s, want 201 with the body", rec.Code, rec.Body)
	}
}

func TestRegisterWithPanicsAtBoot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, want string
		register   func(r *router.Router)
	}{
		{"nil schema", "needs a schema", func(r *router.Router) {
			api.RegisterWith(r, "POST /keys", api.Op{}, nil, keyHandler)
		}},
		{"bad pattern names RegisterWith", "api: RegisterWith pattern", func(r *router.Router) {
			api.RegisterWith(r, "/keys", api.Op{}, runtimeKeySchema([]string{"read"}), keyHandler)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			defer func() {
				r := recover()
				if r == nil {
					t.Fatal("no panic")
				}

				if msg := fmt.Sprint(r); !strings.Contains(msg, tt.want) {
					t.Errorf("panic = %q, want it to contain %q", msg, tt.want)
				}
			}()

			tt.register(router.New(nil))
		})
	}
}

// TestRegisterWithKeepsTagDerivedOpenAPI guards the documentation: the
// OpenAPI document comes from the input and output types, so a custom schema
// must leave it byte-identical to the one Register produces.
func TestRegisterWithKeepsTagDerivedOpenAPI(t *testing.T) {
	t.Parallel()

	build := func(register func(r *router.Router)) string {
		t.Helper()

		r := router.New(nil)
		register(r)

		doc, err := openapi.Build(r.Routes(), openapi.Info{Title: "Keys", Version: "1.0.0"})
		if err != nil {
			t.Fatalf("Build: %v", err)
		}

		out, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}

		return string(out)
	}

	tagged := build(func(r *router.Router) {
		api.Register(r, "POST /keys", api.Op{Name: "keys.store", Summary: "Create a key"}, keyHandler)
	})
	custom := build(func(r *router.Router) {
		api.RegisterWith(r, "POST /keys", api.Op{Name: "keys.store", Summary: "Create a key"},
			runtimeKeySchema([]string{"read", "write"}), keyHandler)
	})

	if diff := cmp.Diff(tagged, custom); diff != "" {
		t.Errorf("document (-Register +RegisterWith):\n%s", diff)
	}

	if !strings.Contains(custom, `"maxLength": 20`) {
		t.Errorf("document lost the tag-derived rules:\n%s", custom)
	}
}

type tagsInput struct {
	Labels map[string]int `json:"labels" validate:"required,min=1,dive,min=1"`
}

func TestRegister_MapInput(t *testing.T) {
	t.Parallel()

	r := router.New(nil)
	api.Register(r, "POST /tags", api.Op{},
		func(_ *web.Ctx, in tagsInput) (map[string]int, error) { return in.Labels, nil })

	tests := []struct {
		name, body string
		status     int
		want       string
	}{
		{"valid", `{"labels":{"a":2}}`, 201, `{"a":2}`},
		{"value rule", `{"labels":{"a":0}}`, 422, `labels.a`},
		{"empty map", `{"labels":{}}`, 422, `labels`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/tags", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(rec, req)

			if rec.Code != tt.status || !strings.Contains(rec.Body.String(), tt.want) {
				t.Fatalf("POST /tags %s = %d %s, want %d containing %q",
					tt.body, rec.Code, rec.Body.String(), tt.status, tt.want)
			}
		})
	}

	doc, err := openapi.Build(r.Routes(), openapi.Info{Title: "Tags", Version: "1.0.0"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if !strings.Contains(string(out), `"additionalProperties":{"minimum":1,"type":"integer"}`) {
		t.Errorf("document lacks a typed additionalProperties with minimum 1: %s", out)
	}
}
