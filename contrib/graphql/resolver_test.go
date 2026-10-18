package graphql

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/errs"
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

func TestNewRejectsFieldsWithoutGoField(t *testing.T) {
	t.Parallel()

	type owner struct {
		Name string `json:"name"`
	}

	type item struct {
		ID     string `json:"id"`
		Secret string `json:"-"`
		Owner  *owner `json:"owner"`
	}

	tests := []struct {
		name, sdl string
		want      string // "" means New succeeds
	}{
		{"json dash is not a field", `type Query { item: Item } type Item { id: ID secret: String }`, "Item.secret"},
		{
			"nested type",
			`type Query { item: Item } type Item { id: ID owner: Owner } type Owner { name: String age: Int }`,
			"Owner.age",
		},
		{
			"all fields present",
			`type Query { item: Item } type Item { id: ID owner: Owner } type Owner { name: String }`,
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := New(tt.sdl, Query("item", func(context.Context, NoArgs) (*item, error) { return &item{}, nil }))

			switch {
			case tt.want == "" && err != nil:
				t.Fatalf("New() error = %v, want nil", err)
			case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
				t.Fatalf("New() error = %v, want one naming %s", err, tt.want)
			}
		})
	}

	// A bound resolver covers a field the Go type lacks.
	if _, err := New(`type Query { item: Item } type Item { id: ID secret: String }`,
		Query("item", func(context.Context, NoArgs) (*item, error) { return &item{}, nil }),
		Field("Item", "secret", func(context.Context, *item, NoArgs) (string, error) { return "", nil }),
	); err != nil {
		t.Fatalf("New(bound secret) error = %v", err)
	}
}

type address struct {
	City string `json:"city" validate:"required,min=2"`
}

type signupArgs struct {
	Name    string  `json:"name"`
	Address address `json:"address"`
}

type checkedArgs struct {
	Name string `json:"name"`
}

func (a checkedArgs) Validate() error {
	switch a.Name {
	case "plain":
		return errors.New("pq: duplicate key value violates unique constraint users_email_key")
	case "client":
		return errs.New(422, "Name is reserved.")
	case "graphql":
		return NewError("Name is reserved.", map[string]any{"code": "RESERVED"})
	}

	return nil
}

func TestSecurity_A03_GraphQLValidatesNestedInputs(t *testing.T) {
	t.Parallel()

	s, err := New(`input AddressInput { city: String }
		type Query { ok: Boolean }
		type Mutation { signup(name: String, address: AddressInput): Boolean }`,
		Query("ok", func(context.Context, NoArgs) (bool, error) { return true, nil }),
		Mutation("signup", func(context.Context, signupArgs) (bool, error) { return true, nil }),
	)
	if err != nil {
		t.Fatal(err)
	}

	r := s.Execute(t.Context(), Request{Query: `mutation { signup(name: "a", address: {city: "x"}) }`})
	if firstCode(r) != CodeBadUserInput {
		t.Fatalf("nested city too short: errors = %v, want %s", r.Errors, CodeBadUserInput)
	}

	if r := s.Execute(
		t.Context(),
		Request{Query: `mutation { signup(name: "a", address: {city: "Rio"}) }`},
	); len(
		r.Errors,
	) > 0 {
		t.Fatalf("valid nested input: errors = %v", r.Errors)
	}
}

func TestSecurity_A05_GraphQLMasksPlainValidateErrors(t *testing.T) {
	t.Parallel()

	s, err := New(`type Query { check(name: String): Boolean }`,
		Query("check", func(context.Context, checkedArgs) (bool, error) { return true, nil }))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct{ name, code, message string }{
		{"plain", CodeInternal, "Internal server error"},
		{"client", CodeBadUserInput, "Name is reserved."},
		{"graphql", "RESERVED", "Name is reserved."},
	}

	for _, tt := range tests {
		r := s.Execute(t.Context(), Request{
			Query:     `query ($n: String) { check(name: $n) }`,
			Variables: map[string]any{"n": tt.name},
		})
		if len(r.Errors) != 1 || r.Errors[0].Code() != tt.code || r.Errors[0].Message != tt.message {
			t.Errorf("Validate() returning %s: errors = %+v, want %s %q", tt.name, r.Errors, tt.code, tt.message)
		}
	}
}
