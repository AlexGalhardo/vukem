package z_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/z"
)

// A payload whose shape varies by a discriminator field cannot be
// described field by field, and before JSON/JSONObject there was no honest
// way to describe it at all: map[string]any and any panicked in For, and
// json.RawMessage was silently taken for its underlying []byte, so the
// document said "array of integers" and the validator refused every object.
type submitIn struct {
	Tool    string         `json:"tool"    validate:"required"`
	Options map[string]any `json:"options"`
}

func TestForAcceptsADynamicPayload(t *testing.T) {
	t.Parallel()

	s := z.For[submitIn]()

	got, err := s.Parse(map[string]any{
		"tool":    "pixelsvg",
		"options": map[string]any{"colors": float64(8), "nested": map[string]any{"on": true}},
	})
	if err != nil {
		t.Fatalf("Parse() = %v", err)
	}

	want := submitIn{
		Tool:    "pixelsvg",
		Options: map[string]any{"colors": float64(8), "nested": map[string]any{"on": true}},
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Parse() diff (-want +got):\n%s", diff)
	}

	// The document says "an object", not "an array of integers".
	schema := z.JSONSchema(s)

	props, _ := schema["properties"].(map[string]any)
	if diff := cmp.Diff(map[string]any{"type": "object"}, props["options"]); diff != "" {
		t.Errorf("options schema diff (-want +got):\n%s", diff)
	}
}

func TestForAcceptsAnyJSONValue(t *testing.T) {
	t.Parallel()

	type anyIn struct {
		Tool    string `json:"tool"    validate:"required"`
		Options any    `json:"options"`
	}

	s := z.For[anyIn]()

	for _, in := range []any{
		map[string]any{"a": float64(1)},
		[]any{float64(1), "two"},
		"a string",
		float64(3),
		true,
	} {
		got, err := s.Parse(map[string]any{"tool": "t", "options": in})
		if err != nil {
			t.Errorf("Parse(%v) = %v", in, err)
			continue
		}

		if diff := cmp.Diff(in, got.Options); diff != "" {
			t.Errorf("Parse(%v) diff (-want +got):\n%s", in, diff)
		}
	}

	// No constraint at all is the honest schema for "any JSON value".
	props, _ := z.JSONSchema(s)["properties"].(map[string]any)
	if diff := cmp.Diff(map[string]any{}, props["options"]); diff != "" {
		t.Errorf("options schema diff (-want +got):\n%s", diff)
	}
}

// json.RawMessage is refused with the alternative named, not described as
// the []byte it is built on. Parse receives decoded JSON, so producing the
// caller's exact bytes is impossible: re-encoding changes key order and
// number formatting, and a type that promises the original bytes must not
// be filled with different ones.
func TestForRefusesJSONRawMessage(t *testing.T) {
	t.Parallel()

	type rawIn struct {
		Options json.RawMessage `json:"options"`
	}

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("z.For[rawIn]() did not panic")
		}

		msg, _ := r.(string)
		for _, want := range []string{"json.RawMessage", "map[string]any", "any"} {
			if !strings.Contains(msg, want) {
				t.Errorf("panic %q does not name %q", msg, want)
			}
		}
	}()

	_ = z.For[rawIn]()
}

func TestJSONSchemaModifiers(t *testing.T) {
	t.Parallel()

	// An object is required to be one.
	if _, err := z.JSONObject().Parse([]any{float64(1)}); err == nil {
		t.Error("JSONObject().Parse(array) = nil, want an error")
	}

	if _, err := z.JSONObject().Parse(map[string]any{}); err != nil {
		t.Errorf("JSONObject().Parse({}) = %v", err)
	}

	// Any JSON value takes an array.
	if got, err := z.JSON().Parse([]any{float64(1)}); err != nil {
		t.Errorf("JSON().Parse(array) = %v, %v", got, err)
	}

	// null is refused unless Nullable.
	if _, err := z.JSONObject().Parse(nil); err == nil {
		t.Error("JSONObject().Parse(nil) = nil, want an error")
	}

	if got, err := z.JSONObject().Nullable().Parse(nil); err != nil || got != nil {
		t.Errorf("JSONObject().Nullable().Parse(nil) = %v, %v", got, err)
	}

	// Describe reaches the exported schema.
	if got := z.JSONSchema(z.JSONObject().Describe("tool options")); got["description"] != "tool options" {
		t.Errorf("Describe() schema = %v", got)
	}

	// Refine runs on the decoded value.
	s := z.JSONObject().Refine(func(v any) error {
		if m, _ := v.(map[string]any); m["tool"] == nil {
			return errMissingTool
		}

		return nil
	})

	if _, err := s.Parse(map[string]any{}); err == nil {
		t.Error("Refine() did not run")
	}

	if _, err := s.Parse(map[string]any{"tool": "x"}); err != nil {
		t.Errorf("Refine() rejected a valid value: %v", err)
	}
}

var errMissingTool = testError("options need a tool")

type testError string

func (e testError) Error() string { return string(e) }
