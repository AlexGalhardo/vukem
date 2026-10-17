package api_test

import (
	"errors"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/api"
	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
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
