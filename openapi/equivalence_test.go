package openapi_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/api"
	"github.com/AlexGalhardo/vukem/openapi"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
	"github.com/AlexGalhardo/vukem/z"
)

// eqField documents one field V of type typ tagged validate:"tag" and returns
// its schema.
func eqField(typ reflect.Type, tag string) openapi.Schema {
	st := reflect.StructOf([]reflect.StructField{{
		Name: "V", Type: typ, Tag: reflect.StructTag(fmt.Sprintf(`json:"v" validate:%q`, tag)),
	}})

	return openapi.NewSchemas().Object(st, nil)["properties"].(map[string]any)["v"].(openapi.Schema)
}

// TestTagKeywordsComeFromZ pins what the document says for every `validate`
// rule: the keywords come from z, which enforces the rules, so the document
// describes what the server accepts.
//
// Differences from the legacy parser (v0.40, docs/upgrade/1.0.md):
//   - omitempty, string: the empty string is allowed next to the constraint (anyOf with enum [""])
//   - default=V, string: default is emitted
//   - ulid, string: ^[0-9A-HJKMNP-TV-Z]{26}$ → ^[0-7][0-9A-HJKMNP-TV-Za-hjkmnp-tv-z]{25}$ (z accepts lowercase)
//   - alpha, string: ^[a-zA-Z]+$ → ^\p{L}+$ (any letter)
//   - alphanum, string: ^[a-zA-Z0-9]+$ → ^[\p{L}\p{N}]+$
//   - numeric, string: ^[0-9]+$ → ^-?\d+(\.\d+)?$ (sign and decimals)
func TestTagKeywordsComeFromZ(t *testing.T) {
	t.Parallel()

	str, i, slice := reflect.TypeFor[string](), reflect.TypeFor[int](), reflect.TypeFor[[]string]()

	tests := []struct {
		name string
		typ  reflect.Type
		tag  string
		want string // JSON with sorted keys
	}{
		{"string required", str, "required", `{"minLength":1,"type":"string"}`},
		{
			"string omitempty min", str, "omitempty,min=2",
			`{"anyOf":[{"minLength":2,"type":"string"},{"enum":[""]}],"type":"string"}`,
		},
		{"string default", str, "default=x", `{"default":"x","type":"string"}`},
		{"string min max", str, "min=2,max=5", `{"maxLength":5,"minLength":2,"type":"string"}`},
		{"string len", str, "len=3", `{"maxLength":3,"minLength":3,"type":"string"}`},
		{"string email", str, "email", `{"format":"email","type":"string"}`},
		{"string url", str, "url", `{"format":"uri","type":"string"}`},
		{"string uuid", str, "uuid", `{"format":"uuid","type":"string"}`},
		{"string ulid", str, "ulid", `{"pattern":"^[0-7][0-9A-HJKMNP-TV-Za-hjkmnp-tv-z]{25}$","type":"string"}`},
		{"string alpha", str, "alpha", `{"pattern":"^\\p{L}+$","type":"string"}`},
		{"string alphanum", str, "alphanum", `{"pattern":"^[\\p{L}\\p{N}]+$","type":"string"}`},
		{"string numeric", str, "numeric", `{"pattern":"^-?\\d+(\\.\\d+)?$","type":"string"}`},
		{"string oneof", str, "oneof=a b", `{"enum":["a","b"],"type":"string"}`},
		{"string regex", str, "regex=^[a-z,]+$", `{"pattern":"^[a-z,]+$","type":"string"}`},
		{"string startswith", str, "startswith=ab", `{"pattern":"^ab","type":"string"}`},
		{"string endswith", str, "endswith=yz", `{"pattern":"yz$","type":"string"}`},
		{"string contains", str, "contains=mm", `{"pattern":"mm","type":"string"}`},
		{"string trim lowercase", str, "trim,lowercase", `{"type":"string"}`},
		{
			"pointer omitempty min", reflect.TypeFor[*string](), "omitempty,min=3",
			`{"anyOf":[{"minLength":3,"type":"string"},{"enum":[""]},{"type":"null"}],"type":["string","null"]}`,
		},
		{
			"pointer omitempty oneof", reflect.TypeFor[*string](), "omitempty,oneof=x y",
			`{"enum":["x","y","",null],"type":["string","null"]}`,
		},
		{"int min max", i, "min=1,max=9", `{"maximum":9,"minimum":1,"type":"integer"}`},
		{"int positive", i, "positive", `{"exclusiveMinimum":0,"type":"integer"}`},
		{"int oneof", i, "oneof=1 2", `{"type":"integer"}`},
		{"int32 required", reflect.TypeFor[int32](), "required", `{"format":"int32","type":"integer"}`},
		{"uint max", reflect.TypeFor[uint](), "max=7", `{"maximum":7,"minimum":0,"type":"integer"}`},
		{"float64 min", reflect.TypeFor[float64](), "min=0.5", `{"format":"double","minimum":0.5,"type":"number"}`},
		{
			"slice min max unique", slice, "min=1,max=3,unique",
			`{"items":{"type":"string"},"maxItems":3,"minItems":1,"type":"array","uniqueItems":true}`,
		},
		{"slice dive", slice, "dive,email", `{"items":{"format":"email","type":"string"},"type":"array"}`},
		{
			"map min",
			reflect.TypeFor[map[string]int](),
			"min=1",
			`{"additionalProperties":{"type":"integer"},"type":"object"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := json.Marshal(eqField(tt.typ, tt.tag))
			if err != nil {
				t.Fatal(err)
			}

			if string(got) != tt.want {
				t.Errorf("validate:%q on %s = %s, want %s", tt.tag, tt.typ, got, tt.want)
			}
		})
	}
}

type overlayOwner struct {
	Name string `json:"name"`
}

type overlayInput struct {
	Title string       `json:"title" validate:"max=5"`
	Owner overlayOwner `json:"owner"`
}

// TestRegisterWithOverlayKeepsRefs checks a RegisterWith schema adds its
// keywords to leaf properties only: a struct property keeps its $ref (no
// minLength beside it), required follows the schema, and a key the schema
// has but the input type lacks is ignored.
func TestRegisterWithOverlayKeepsRefs(t *testing.T) {
	t.Parallel()

	schema := z.Struct[overlayInput](z.Shape{
		"title": z.String().Min(3),
		"owner": z.Struct[overlayOwner](z.Shape{"name": z.String().Min(1)}),
	})

	r := router.New(nil)
	api.RegisterWith(r, "POST /items", api.Op{Name: "items.store"}, schema,
		func(*web.Ctx, overlayInput) (overlayInput, error) { return overlayInput{}, nil })

	if _, err := r.Handler(); err != nil {
		t.Fatal(err)
	}

	doc, err := openapi.Build(r.Routes(), openapi.Info{})
	if err != nil {
		t.Fatal(err)
	}

	body := doc.Paths["/items"]["post"].RequestBody.Content["application/json"].Schema
	props := body["properties"].(map[string]any)

	if got := props["title"].(openapi.Schema)["minLength"]; got != 3 {
		t.Errorf("title minLength = %v, want 3", got)
	}

	// The schema replaces the tag rules: max=5 is not what the server checks.
	if got, ok := props["title"].(openapi.Schema)["maxLength"]; ok {
		t.Errorf("title maxLength = %v, want none (the schema has no max)", got)
	}

	owner := props["owner"].(openapi.Schema)
	if _, ok := owner["$ref"]; !ok {
		t.Errorf("owner = %v, want the $ref kept", owner)
	}

	if _, ok := owner["minLength"]; ok {
		t.Errorf("owner = %v, want no keyword beside the $ref", owner)
	}

	if diff := cmp.Diff([]string{"owner", "title"}, body["required"]); diff != "" {
		t.Errorf("required (-want +got):\n%s", diff)
	}
}
