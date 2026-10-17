package openapi_test

import (
	"fmt"

	"github.com/AlexGalhardo/vukem/api"
	"github.com/AlexGalhardo/vukem/openapi"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

type exShowProduct struct {
	ID int `json:"id"`
}

type exProduct struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func ExampleGenerate() {
	r := router.New(nil)
	api.Register(r, "GET /api/products/{id}", api.Op{Name: "products.show", Summary: "Show a product"},
		func(_ *web.Ctx, in exShowProduct) (exProduct, error) {
			return exProduct{ID: in.ID}, nil
		})

	doc := openapi.Generate(r.Routes(), openapi.Info{Title: "Shop", Version: "1.0.0"})
	op := doc.Paths["/api/products/{id}"]["get"]
	fmt.Println(doc.OpenAPI, op.OperationID, op.Summary)
	// Output: 3.1.0 products.show Show a product
}
