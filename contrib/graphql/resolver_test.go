package graphql

import (
	"context"
	"testing"
)

type product struct {
	ID         string `json:"id"`
	PriceCents int    `json:"price_cents"`
	Title      string `json:"name,omitempty"`
	Stock      int
	Secret     string `json:"-"`
	Tagless    string `json:",omitempty"`
}

func TestDefaultResolverMatchesJSONAndGoNames(t *testing.T) {
	t.Parallel()

	const sdl = `
type Query { product: Product }
type Product {
  id: ID
  price_cents: Int
  priceCents: Int
  name: String
  title: String
  stock: Int
  tagless: String
  secret: String
}
`

	s, err := New(sdl, Query("product", func(context.Context, NoArgs) (*product, error) {
		return &product{ID: "p1", PriceCents: 1990, Title: "Mug", Stock: 3, Secret: "s", Tagless: "t"}, nil
	}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	tests := []struct{ name, query, want string }{
		{
			"json name",
			`{ product { id price_cents name } }`,
			`{"data":{"product":{"id":"p1","price_cents":1990,"name":"Mug"}}}`,
		},
		{
			"go name of a tagged field", `{ product { priceCents title } }`,
			`{"data":{"product":{"priceCents":1990,"title":"Mug"}}}`,
		},
		{"untagged go name", `{ product { stock tagless } }`, `{"data":{"product":{"stock":3,"tagless":"t"}}}`},
		{
			"json dash stays hidden",
			`{ product { secret } }`,
			`{"errors":[{"message":"Internal server error","locations":[{"line":1,"column":13}],"path":["product","secret"],
			  "extensions":{"code":"INTERNAL_SERVER_ERROR"}}],"data":{"product":{"secret":null}}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			jsonEqual(t, tt.want, run(t, s, tt.query, nil))
		})
	}
}

// A json name wins over another field's folded Go name.
func TestDefaultResolverJSONNameWinsOverGoName(t *testing.T) {
	t.Parallel()

	type row struct {
		Name  string `json:"label"`
		Label string `json:"name"`
	}

	s, err := New(`type Query { row: Row } type Row { name: String label: String }`,
		Query("row", func(context.Context, NoArgs) (row, error) { return row{Name: "N", Label: "L"}, nil }))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	jsonEqual(t, `{"data":{"row":{"name":"L","label":"N"}}}`, run(t, s, `{ row { name label } }`, nil))
}
