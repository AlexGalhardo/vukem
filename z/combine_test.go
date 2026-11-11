package z

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

type createPost struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

type updatePost struct {
	Title *string `json:"title"`
	Body  *string `json:"body"`
}

type titleOnly struct {
	Title string `json:"title"`
}

func createPostSchema() StructSchema[createPost] {
	return Struct[createPost](Shape{
		"title": String().Trim().Min(3).Max(120),
		"body":  String().Trim().Max(10),
	})
}

func TestPick(t *testing.T) {
	t.Parallel()

	s := Pick[titleOnly](createPostSchema(), "title")

	got, err := s.Parse(map[string]any{"title": "  abc ", "body": "ignored"})
	if err != nil || got.Title != "abc" {
		t.Fatalf("Parse = %+v, %v", got, err)
	}

	if _, err := s.Parse(map[string]any{"title": "ab"}); err == nil {
		t.Error("the Min rule of the picked field was lost")
	}

	if diff := cmp.Diff([]string{"title"}, s.Keys()); diff != "" {
		t.Errorf("Keys (-want +got):\n%s", diff)
	}

	mustPanic(t, "Pick of an unknown key", func() { Pick[titleOnly](createPostSchema(), "nope") })
	mustPanic(t, "Pick into a struct without the field", func() { Pick[titleOnly](createPostSchema(), "body") })
}

func TestOmit(t *testing.T) {
	t.Parallel()

	s := Omit[titleOnly](createPostSchema(), "body")

	if diff := cmp.Diff([]string{"title"}, s.Keys()); diff != "" {
		t.Errorf("Keys (-want +got):\n%s", diff)
	}

	if _, err := s.Parse(map[string]any{"title": "abc"}); err != nil {
		t.Errorf("Parse = %v", err)
	}

	mustPanic(t, "Omit of an unknown key", func() { Omit[titleOnly](createPostSchema(), "nope") })
}

func TestExtend(t *testing.T) {
	t.Parallel()

	type withSlug struct {
		Title string `json:"title"`
		Body  string `json:"body"`
		Slug  string `json:"slug"`
	}

	s := Extend[withSlug](createPostSchema(), Shape{
		"slug":  String().Min(1),
		"title": String().Min(1), // replaces the rule of the original
	})

	got, err := s.Parse(map[string]any{"title": "a", "body": "b", "slug": "s"})
	if err != nil || got.Title != "a" || got.Slug != "s" {
		t.Fatalf("Parse = %+v, %v", got, err)
	}

	if _, err := s.Parse(map[string]any{"title": "a", "body": "b"}); err == nil {
		t.Error("the new key is optional")
	}

	// The original is untouched.
	if _, err := createPostSchema().Parse(map[string]any{"title": "a", "body": "b"}); err == nil {
		t.Error("Extend changed the schema it was built from")
	}
}

func TestPartial(t *testing.T) {
	t.Parallel()

	s := Partial[updatePost](createPostSchema())

	got, err := s.Parse(map[string]any{})
	if err != nil || got.Title != nil || got.Body != nil {
		t.Fatalf("Parse({}) = %+v, %v", got, err)
	}

	got, err = s.Parse(map[string]any{"title": " abc "})
	if err != nil || got.Title == nil || *got.Title != "abc" || got.Body != nil {
		t.Fatalf("Parse(title) = %+v, %v", got, err)
	}

	if _, err := s.Parse(map[string]any{"title": "ab"}); err == nil {
		t.Error("the Min rule is not applied to a field that is sent")
	}

	// Every kind of schema can be relaxed.
	type mixed struct {
		S  string            `json:"s"`
		N  int               `json:"n"`
		L  []string          `json:"l"`
		M  map[string]int    `json:"m"`
		E  string            `json:"e"`
		O  *struct{ X int }  `json:"o"`
		X  map[string]string `json:"x"`
		Lt string            `json:"lt"`
	}

	full := Struct[mixed](Shape{
		"s": String(), "n": Int(), "l": Slice(String()), "m": Map(Int()), "e": Enum("a"),
		"x": Map(String()), "lt": Literal("k"),
	})

	if _, err := Partial[mixed](full).Parse(map[string]any{}); err != nil {
		t.Errorf("Partial of every schema kind rejects {}: %v", err)
	}
}

func TestStrictIsInherited(t *testing.T) {
	t.Parallel()

	s := Pick[titleOnly](createPostSchema().Strict(), "title")

	if _, err := s.Parse(map[string]any{"title": "abc", "other": 1}); err == nil {
		t.Error("an unknown key passed a schema derived from a strict one")
	}
}

func TestPassthrough(t *testing.T) {
	t.Parallel()

	type event struct {
		Name  string         `json:"name"`
		Extra map[string]any `json:"-"`
	}

	s := Struct[event](Shape{"name": String()}).Passthrough(func(dst *event, extra map[string]any) {
		dst.Extra = extra
	})

	got, err := s.Parse(map[string]any{"name": "n", "a": 1, "b": "x"})
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(map[string]any{"a": 1, "b": "x"}, got.Extra); diff != "" {
		t.Errorf("extra (-want +got):\n%s", diff)
	}

	got, _ = s.Parse(map[string]any{"name": "n"})
	if len(got.Extra) != 0 {
		t.Errorf("extra = %v, want none", got.Extra)
	}

	if js := JSONSchema(s); js["additionalProperties"] != true {
		t.Errorf("additionalProperties = %v, want true", js["additionalProperties"])
	}

	mustPanic(t, "Strict then Passthrough", func() {
		Struct[event](Shape{"name": String()}).Strict().Passthrough(func(*event, map[string]any) {})
	})
	mustPanic(t, "Passthrough then Strict", func() { s.Strict() })
	mustPanic(t, "Passthrough(nil)", func() { Struct[event](Shape{"name": String()}).Passthrough(nil) })

	// A derived schema does not inherit the callback written for T.
	type small struct {
		Name string `json:"name"`
	}

	if js := JSONSchema(Pick[small](s, "name")); js["additionalProperties"] != nil {
		t.Errorf("derived schema additionalProperties = %v, want none", js["additionalProperties"])
	}
}
