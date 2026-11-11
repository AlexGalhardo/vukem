package z_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/z"
)

func TestSplitRules(t *testing.T) {
	t.Parallel()

	got := z.SplitRules("required,min=3,regex=^a,b$")
	if diff := cmp.Diff([]string{"required", "min=3", "regex=^a,b$"}, got); diff != "" {
		t.Errorf("SplitRules (-want +got):\n%s", diff)
	}

	if got := z.SplitRules(""); got != nil {
		t.Errorf("SplitRules(\"\") = %v, want nil", got)
	}
}

func TestKeywords(t *testing.T) {
	t.Parallel()

	str, num := reflect.TypeFor[string](), reflect.TypeFor[int]()

	tests := []struct {
		name     string
		typ      reflect.Type
		rules    string
		want     map[string]any
		required bool
	}{
		{"string required", str, "required", map[string]any{"minLength": 1}, true},
		{"string min max", str, "min=2,max=5", map[string]any{"minLength": 2, "maxLength": 5}, false},
		{"string email", str, "email", map[string]any{"format": "email"}, false},
		{"string oneof", str, "oneof=a b", map[string]any{"enum": []any{"a", "b"}}, false},
		{"string default", str, "default=x", map[string]any{"default": "x"}, false},
		{"pointer string", reflect.TypeFor[*string](), "min=1", map[string]any{"minLength": 1}, false},
		{"int min max", num, "min=1,max=9", map[string]any{"minimum": 1, "maximum": 9}, false},
		{
			"slice length",
			reflect.TypeFor[[]string](),
			"min=1,max=3,unique",
			map[string]any{"minItems": 1, "maxItems": 3, "uniqueItems": true},
			false,
		},
		{
			"rules after dive ignored",
			reflect.TypeFor[[]string](),
			"min=1,dive,email",
			map[string]any{"minItems": 1},
			false,
		},
		{"struct only required", reflect.TypeFor[struct{ A string }](), "required", map[string]any{}, true},
		{"any", reflect.TypeFor[any](), "required", map[string]any{}, true},
		{"recursive element", reflect.TypeFor[[]recursive](), "min=1", map[string]any{"minItems": 1}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			kw, required, err := z.Keywords(tt.typ, z.SplitRules(tt.rules))
			if err != nil {
				t.Fatalf("Keywords error = %v", err)
			}

			got := map[string]any{}
			for k, v := range kw {
				got[k] = normalize(v)
			}

			if diff := cmp.Diff(normalize(tt.want), any(got)); diff != "" {
				t.Errorf("Keywords(%s, %q) (-want +got):\n%s", tt.typ, tt.rules, diff)
			}

			if required != tt.required {
				t.Errorf("Keywords(%s, %q) required = %v, want %v", tt.typ, tt.rules, required, tt.required)
			}

			for _, k := range []string{"type", "items", "properties", "additionalProperties"} {
				if _, ok := kw[k]; ok {
					t.Errorf("Keywords(%s, %q) returned structural key %q", tt.typ, tt.rules, k)
				}
			}
		})
	}

	if _, _, err := z.Keywords(str, []string{"min=abc"}); err == nil {
		t.Error("Keywords with an invalid rule = nil error, want an error")
	}
}

// normalize turns numbers into float64 and []string into []any, the shapes a
// JSON round trip would give, so the test compares values, not Go types.
func normalize(v any) any {
	switch x := v.(type) {
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case uint64:
		return float64(x)
	case []string:
		out := make([]any, len(x))
		for i, s := range x {
			out[i] = s
		}

		return out
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			out[k] = normalize(e)
		}

		return out
	default:
		return v
	}
}

type recursive struct {
	Children []recursive `json:"children" validate:"dive"`
}

type propsInput struct {
	Name string   `json:"name" validate:"required,max=20"`
	Tags []string `json:"tags"`
}

func TestProperties(t *testing.T) {
	t.Parallel()

	built := z.Struct[propsInput](z.Shape{"name": z.String().Min(1), "tags": z.Slice(z.String()).Max(2)})

	for name, s := range map[string]z.AnySchema{"Struct": built, "For": z.For[propsInput]()} {
		props, ok := z.Properties(s)
		if !ok {
			t.Fatalf("Properties(%s) ok = false", name)
		}

		keys := make([]string, 0, len(props))
		for k := range props {
			keys = append(keys, k)
		}

		slices.Sort(keys)

		if diff := cmp.Diff([]string{"name", "tags"}, keys); diff != "" {
			t.Errorf("Properties(%s) keys (-want +got):\n%s", name, diff)
		}
	}

	if _, ok := z.Properties(z.String()); ok {
		t.Error("Properties(String) ok = true, want false")
	}
}
