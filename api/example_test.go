package api_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/AlexGalhardo/vukem/api"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
	"github.com/AlexGalhardo/vukem/z"
)

type exStoreProduct struct {
	Name  string `json:"name"  validate:"required"`
	Price int    `json:"price" validate:"min=1"`
}

type exSubscribe struct {
	Plans []string `json:"plans"`
}

type exProductJSON struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func ExampleRegister() {
	r := router.New(nil)
	api.Register(r, "POST /products", api.Op{Name: "products.store", Tags: []string{"products"}},
		func(_ *web.Ctx, in exStoreProduct) (exProductJSON, error) {
			return exProductJSON{ID: 1, Name: in.Name}, nil
		})

	req := httptest.NewRequest(http.MethodPost, "/products", strings.NewReader(`{"name":"Mug","price":12}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	fmt.Println(rec.Code, strings.TrimSpace(rec.Body.String()))
	// Output: 201 {"id":1,"name":"Mug"}
}

func ExampleCollection() {
	r := router.New(nil)
	r.Get("/products", func(c *web.Ctx) error {
		names := []string{"Mug", "Cap"}
		return api.Collection(c, names, func(n string) exProductJSON { return exProductJSON{Name: n} })
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/products", http.NoBody))

	fmt.Println(rec.Code, strings.TrimSpace(rec.Body.String()))
	// Output: 200 {"data":[{"id":0,"name":"Mug"},{"id":0,"name":"Cap"}]}
}

func ExampleRegisterWith() {
	// The allowlist is a runtime value, so `validate:"max=..."` cannot say it.
	plans := []string{"free", "pro"}
	schema := z.Struct[exSubscribe](z.Shape{
		"plans": z.Slice(z.String().OneOf(plans...)).Min(1).Max(len(plans)),
	}).Refine(func(s exSubscribe) error {
		if len(s.Plans) == 2 && s.Plans[0] == s.Plans[1] {
			return z.FieldError("plans", "plans must differ")
		}

		return nil
	})

	r := router.New(nil)
	api.RegisterWith(r, "POST /subscriptions", api.Op{Name: "subscriptions.store"}, schema,
		func(_ *web.Ctx, in exSubscribe) (exProductJSON, error) {
			return exProductJSON{ID: 1, Name: strings.Join(in.Plans, ",")}, nil
		})

	req := httptest.NewRequest(http.MethodPost, "/subscriptions", strings.NewReader(`{"plans":["free","pro"]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	fmt.Println(rec.Code, strings.TrimSpace(rec.Body.String()))
	// Output: 201 {"id":1,"name":"free,pro"}
}
