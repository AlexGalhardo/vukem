package openapi_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/api"
	"github.com/AlexGalhardo/vukem/openapi"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
	"github.com/AlexGalhardo/vukem/z"
)

type editCmd interface{ editName() string }

type addClip struct {
	Op    string `json:"op"`
	Track int    `json:"track"`
}

type delClip struct {
	Op string `json:"op"`
	ID string `json:"id"`
}

func (addClip) editName() string { return "add" }
func (delClip) editName() string { return "del" }

type editInput struct {
	One  editCmd   `json:"one"`
	Many []editCmd `json:"many"`
}

// TestRegisterWithDiscriminatedUnion checks a z.DiscriminatedUnion reaches the
// document as oneOf plus discriminator, as a body property and inside a slice
// (the shape of a batch of commands).
func TestRegisterWithDiscriminatedUnion(t *testing.T) {
	t.Parallel()

	union := z.DiscriminatedUnion[editCmd]("op", map[string]z.AnySchema{
		"add": z.Struct[addClip](z.Shape{"op": z.Literal("add"), "track": z.Int()}),
		"del": z.Struct[delClip](z.Shape{"op": z.Literal("del"), "id": z.String()}),
	})
	schema := z.Struct[editInput](z.Shape{"one": union, "many": z.Slice[editCmd](union).Min(1)})

	r := router.New(nil)
	api.RegisterWith(r, "POST /edit", api.Op{Name: "edit"}, schema,
		func(*web.Ctx, editInput) (editInput, error) { return editInput{}, nil })

	if _, err := r.Handler(); err != nil {
		t.Fatal(err)
	}

	doc, err := openapi.Build(r.Routes(), openapi.Info{})
	if err != nil {
		t.Fatal(err)
	}

	props := doc.Paths["/edit"]["post"].RequestBody.Content["application/json"].Schema["properties"].(map[string]any)

	check := func(name string, got any) {
		t.Helper()

		sc, _ := got.(map[string]any)
		if diff := cmp.Diff(map[string]any{"propertyName": "op"}, sc["discriminator"]); diff != "" {
			t.Errorf("%s discriminator (-want +got):\n%s", name, diff)
		}

		if variants, _ := sc["oneOf"].([]any); len(variants) != 2 {
			t.Errorf("%s oneOf = %v, want the two variants", name, sc["oneOf"])
		}
	}

	check("one", props["one"])

	many, _ := props["many"].(map[string]any)
	if many["type"] != "array" || many["minItems"] != 1 {
		t.Errorf("many = %v, want an array that keeps its minItems", many)
	}

	check("many.items", many["items"])
}
