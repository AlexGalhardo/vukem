package z

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestLiteral(t *testing.T) {
	t.Parallel()

	t.Run("string", func(t *testing.T) {
		t.Parallel()

		got, err := Literal("v1").Parse("v1")
		if err != nil || got != "v1" {
			t.Fatalf("Parse(v1) = %q, %v", got, err)
		}

		_, err = Literal("v1").Parse("v2")
		is, ok := AsIssues(err)

		if !ok || is[0].Code != CodeInvalidLiteral || is[0].Params["expected"] != "v1" ||
			is[0].Message != "The value field must be v1." {
			t.Fatalf("Parse(v2) issues = %+v", is)
		}

		if _, err := Literal("v1").Parse(1); err == nil {
			t.Error("a number passed for a string literal")
		}
	})

	t.Run("several values", func(t *testing.T) {
		t.Parallel()

		got, err := Literal(1, 2).Parse(json.Number("2"))
		if err != nil || got != 2 {
			t.Fatalf("Parse(2) = %d, %v", got, err)
		}

		// From a form the number is text.
		if got, err := Literal(1, 2).Parse("2"); err != nil || got != 2 {
			t.Errorf(`Parse("2") = %d, %v`, got, err)
		}

		_, err = Literal(1, 2).Parse(json.Number("3"))
		if is, _ := AsIssues(err); len(is) != 1 || is[0].Params["expected"] != "1, 2" {
			t.Errorf("Parse(3) issues = %+v", is)
		}

		if _, err := Literal[int8](1).Parse(json.Number("257")); err == nil {
			t.Error("a number out of range matched int8 literal 1")
		}

		if got, err := Literal(0.5).Parse(json.Number("0.5")); err != nil || got != 0.5 {
			t.Errorf("float literal = %v, %v", got, err)
		}
	})

	t.Run("bool", func(t *testing.T) {
		t.Parallel()

		if got, err := Literal(true).Parse("on"); err != nil || !got {
			t.Errorf(`Parse("on") = %v, %v`, got, err)
		}

		if _, err := Literal(true).Parse(false); err == nil {
			t.Error("false matched Literal(true)")
		}
	})

	t.Run("named type", func(t *testing.T) {
		t.Parallel()

		type version string

		got, err := Literal(version("v1")).Parse("v1")
		if err != nil || got != "v1" {
			t.Errorf("Parse = %q, %v", got, err)
		}
	})

	t.Run("check does not coerce", func(t *testing.T) {
		t.Parallel()

		if err := Literal(1).Check(1); err != nil {
			t.Errorf("Check(1) = %v", err)
		}

		if err := Literal(1).Check(2); err == nil {
			t.Error("Check(2) = nil")
		}

		c := &pctx{coerce: false}
		if _, _, ok := Literal(1).parseAny(c, "", "1", true); ok {
			t.Error("a string matched a number literal without coercion")
		}
	})

	t.Run("empty", func(t *testing.T) {
		t.Parallel()

		mustPanic(t, "Literal() with no values", func() { Literal[string]() })
	})

	t.Run("json schema", func(t *testing.T) {
		t.Parallel()

		if diff := cmp.Diff(map[string]any{"const": "v1", "type": "string"}, JSONSchema(Literal("v1"))); diff != "" {
			t.Errorf("const (-want +got):\n%s", diff)
		}

		want := map[string]any{"enum": []any{1, 2}, "type": "integer"}
		if diff := cmp.Diff(want, JSONSchema(Literal(1, 2))); diff != "" {
			t.Errorf("enum (-want +got):\n%s", diff)
		}

		if got := JSONSchema(Literal(true))["type"]; got != "boolean" {
			t.Errorf("bool type = %v", got)
		}
	})

	t.Run("modifiers", func(t *testing.T) {
		t.Parallel()

		_, err := Literal("a").Message(CodeInvalidLiteral, "nope").Parse("b")
		if err == nil || !strings.Contains(err.Error(), "nope") {
			t.Errorf("Message: %v", err)
		}

		if got, err := Literal("a", "b").Default("b").Parse(nil); err != nil || got != "b" {
			t.Errorf("Default: %v, %v", got, err)
		}
	})
}
