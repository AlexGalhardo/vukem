package openapi_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/openapi"
)

// camelEnvelope is an app envelope as api.PageOf builds it (VF-014).
type camelEnvelope[R any] struct {
	Items      []R     `json:"items"`
	PerPage    int     `json:"perPage"`
	NextCursor string  `json:"nextCursor,omitempty"`
	Next       *string `json:"next"`
}

// TestGenericEnvelopeDocumented: a typed handler returning the app's own
// generic envelope gets a component with the app's keys and a valid name.
func TestGenericEnvelopeDocumented(t *testing.T) {
	t.Parallel()

	s := openapi.NewSchemas()
	ref, _ := s.For(reflect.TypeFor[camelEnvelope[product]]())["$ref"].(string)

	name := strings.TrimPrefix(ref, "#/components/schemas/")
	if name == ref || strings.ContainsAny(name, "[]/* ") {
		t.Fatalf("ref = %q, want a component name without brackets", ref)
	}

	props, _ := s.Components()[name]["properties"].(map[string]any)

	var keys []string
	for k := range props {
		keys = append(keys, k)
	}

	slices.Sort(keys)

	if want := []string{"items", "next", "nextCursor", "perPage"}; !slices.Equal(keys, want) {
		t.Errorf("properties = %v, want %v", keys, want)
	}
}
