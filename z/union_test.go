package z

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

type payment interface{ isPayment() }

type card struct {
	Method string `json:"method"`
	Number string `json:"number"`
}

type pix struct {
	Method string `json:"method"`
	Key    string `json:"key"`
}

func (card) isPayment() {}
func (pix) isPayment()  {}

func paymentSchema() DiscriminatedUnionSchema[payment] {
	return DiscriminatedUnion[payment]("method", map[string]AnySchema{
		"card": Struct[card](Shape{"method": Literal("card"), "number": String().Len(16)}),
		"pix":  Struct[pix](Shape{"method": Literal("pix"), "key": String().Min(1)}),
	})
}

func mustPanic(t *testing.T, name string, fn func()) {
	t.Helper()

	defer func() {
		if recover() == nil {
			t.Errorf("%s did not panic", name)
		}
	}()

	fn()
}

func TestUnion(t *testing.T) {
	t.Parallel()

	t.Run("first variant that fits", func(t *testing.T) {
		t.Parallel()

		s := Union[any](Int(), String())

		if got, err := s.Parse(json.Number("5")); err != nil || got != 5 {
			t.Errorf("Parse(5) = %#v, %v", got, err)
		}

		if got, err := s.Parse("abc"); err != nil || got != "abc" {
			t.Errorf(`Parse("abc") = %#v, %v`, got, err)
		}
	})

	t.Run("order matters with coercion", func(t *testing.T) {
		t.Parallel()

		if got, _ := Union[any](Int(), String()).Parse("5"); got != 5 {
			t.Errorf("Int first: %#v, want 5", got)
		}

		if got, _ := Union[any](String(), Int()).Parse("5"); got != "5" {
			t.Errorf("String first: %#v, want a string", got)
		}
	})

	t.Run("one issue when nothing fits", func(t *testing.T) {
		t.Parallel()

		type holder struct {
			V any `json:"v"`
		}

		s := Struct[holder](Shape{"v": Union[any](Int().Min(10), String().Min(5))})

		_, err := s.Parse(map[string]any{"v": json.Number("3")})
		if diff := cmp.Diff([]string{"v:invalid_union"}, issueSet(t, err)); diff != "" {
			t.Errorf("issues (-want +got):\n%s", diff)
		}
	})

	t.Run("variants of a struct union", func(t *testing.T) {
		t.Parallel()

		s := Union[payment](
			Struct[card](Shape{"method": Literal("card"), "number": String()}),
			Struct[pix](Shape{"method": Literal("pix"), "key": String()}),
		)

		got, err := s.Parse(map[string]any{"method": "pix", "key": "k"})
		if err != nil || got != (pix{Method: "pix", Key: "k"}) {
			t.Errorf("Parse(pix) = %#v, %v", got, err)
		}
	})

	t.Run("boot checks", func(t *testing.T) {
		t.Parallel()

		mustPanic(t, "one variant", func() { Union[any](Int()) })
		mustPanic(t, "variant not assignable", func() { Union[payment](Int(), String()) })
		mustPanic(t, "variant not assignable to a concrete type", func() { Union[int](Int(), String()) })
	})

	t.Run("modifiers", func(t *testing.T) {
		t.Parallel()

		s := Union[any](Int(), String())

		if _, err := s.Optional().Parse(nil); err != nil {
			t.Errorf("Optional: %v", err)
		}

		if _, err := s.Nullable().Parse(nil); err != nil {
			t.Errorf("Nullable: %v", err)
		}

		if got, err := s.Default("d").Parse(nil); err != nil || got != "d" {
			t.Errorf("Default: %v, %v", got, err)
		}

		if _, err := s.Parse(nil); err == nil {
			t.Error("a missing value passed a required union")
		}

		if _, err := s.Refine(func(any) error { return errNope }).Parse("x"); err == nil {
			t.Error("Refine did not run")
		}

		if _, err := s.Parse(true); err == nil {
			t.Error("a bool matched Union(Int, String)")
		}

		if err := s.Check("x"); err != nil {
			t.Errorf("Check: %v", err)
		}
	})

	t.Run("json schema", func(t *testing.T) {
		t.Parallel()

		want := map[string]any{"anyOf": []any{
			map[string]any{"type": "integer"},
			map[string]any{"type": "string"},
		}}
		if diff := cmp.Diff(want, JSONSchema(Union[any](Int(), String()))); diff != "" {
			t.Errorf("anyOf (-want +got):\n%s", diff)
		}

		nullable := JSONSchema(Union[any](Int(), String()).Nullable().Describe("d"))
		if list, _ := nullable["anyOf"].([]any); len(list) != 3 || nullable["description"] != "d" {
			t.Errorf("nullable union = %v", nullable)
		}
	})
}

func TestDiscriminatedUnion(t *testing.T) {
	t.Parallel()

	s := paymentSchema()

	t.Run("picks the variant", func(t *testing.T) {
		t.Parallel()

		got, err := s.Parse(map[string]any{"method": "card", "number": "1234567812345678"})
		if err != nil || got != (card{Method: "card", Number: "1234567812345678"}) {
			t.Fatalf("Parse(card) = %#v, %v", got, err)
		}

		if _, isCard := got.(card); !isCard {
			t.Errorf("type = %T, want card", got)
		}
	})

	t.Run("issues of the chosen variant", func(t *testing.T) {
		t.Parallel()

		_, err := s.Parse(map[string]any{"method": "card", "number": "12"})
		if diff := cmp.Diff([]string{"number:too_small"}, issueSet(t, err)); diff != "" {
			t.Errorf("issues (-want +got):\n%s", diff)
		}

		type order struct {
			Payment payment `json:"payment"`
		}

		nested := Struct[order](Shape{"payment": s})

		_, err = nested.Parse(map[string]any{"payment": map[string]any{"method": "card", "number": "12"}})
		if diff := cmp.Diff([]string{"payment.number:too_small"}, issueSet(t, err)); diff != "" {
			t.Errorf("nested issues (-want +got):\n%s", diff)
		}
	})

	t.Run("bad discriminator", func(t *testing.T) {
		t.Parallel()

		for name, in := range map[string]any{
			"missing":   map[string]any{},
			"unknown":   map[string]any{"method": "cash"},
			"not text":  map[string]any{"method": 1},
			"nil value": map[string]any{"method": nil},
		} {
			_, err := s.Parse(in)

			is, ok := AsIssues(err)
			if !ok || len(is) != 1 || is[0].Path != "method" || is[0].Code != CodeInvalidDiscriminator ||
				is[0].Params["options"] != "card, pix" {
				t.Errorf("%s: issues = %+v", name, is)
			}
		}
	})

	t.Run("not an object", func(t *testing.T) {
		t.Parallel()

		_, err := s.Parse([]any{})
		if diff := cmp.Diff([]string{":invalid_type"}, issueSet(t, err)); diff != "" {
			t.Errorf("issues (-want +got):\n%s", diff)
		}
	})

	t.Run("check", func(t *testing.T) {
		t.Parallel()

		if err := s.Check(card{Method: "card", Number: "1234567812345678"}); err != nil {
			t.Errorf("Check(valid) = %v", err)
		}

		if err := s.Check(pix{Method: "pix"}); err == nil {
			t.Error("Check(empty key) = nil")
		}
	})

	t.Run("boot checks", func(t *testing.T) {
		t.Parallel()

		mustPanic(t, "no variants", func() { DiscriminatedUnion[payment]("method", nil) })
		mustPanic(t, "key missing in the shape", func() {
			DiscriminatedUnion[payment]("method", map[string]AnySchema{
				"card": Struct[card](Shape{"number": String()}),
			})
		})
		mustPanic(t, "not an object schema", func() {
			DiscriminatedUnion[any]("method", map[string]AnySchema{"a": String()})
		})
		mustPanic(t, "not assignable", func() {
			DiscriminatedUnion[payment]("v", map[string]AnySchema{"a": Struct[struct {
				V string `json:"v"`
			}](Shape{"v": String()})})
		})
	})

	t.Run("json schema", func(t *testing.T) {
		t.Parallel()

		js := JSONSchema(s)

		one, _ := js["oneOf"].([]any)
		if len(one) != 2 {
			t.Fatalf("oneOf = %v", js)
		}

		if diff := cmp.Diff(map[string]any{"propertyName": "method"}, js["discriminator"]); diff != "" {
			t.Errorf("discriminator (-want +got):\n%s", diff)
		}

		first, _ := one[0].(map[string]any)

		props, _ := first["properties"].(map[string]any)
		if _, ok := props["number"]; !ok {
			t.Errorf("the first variant is not the card one: %v", first)
		}
	})

	t.Run("modifiers", func(t *testing.T) {
		t.Parallel()

		if _, err := s.Optional().Parse(nil); err != nil {
			t.Errorf("Optional: %v", err)
		}

		if got, err := s.Default(pix{Method: "pix", Key: "k"}).Parse(nil); err != nil || got == nil {
			t.Errorf("Default: %v, %v", got, err)
		}
	})
}

// A union of two variants that both recurse into the union fails at the leaf
// of a deep input after 2^depth attempts unless the work is bounded.
func TestUnionWorkIsBounded(t *testing.T) {
	t.Parallel()

	var self Schema[any]

	lazy := Lazy(func() Schema[any] { return self })
	self = Union[any](Slice(lazy), Slice(lazy).Min(1))

	in := any("leaf")
	for range 60 {
		in = []any{in}
	}

	start := time.Now()
	_, err := self.Parse(in)

	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("Parse took %v, want a quick refusal", d)
	}

	if err == nil {
		t.Fatal("Parse accepted a list that ends in a string")
	}

	if is, _ := AsIssues(err); len(is) == 0 || (is[0].Code != CodeTooDeep && is[0].Code != CodeInvalidUnion) {
		t.Errorf("issues = %+v", is)
	}
}

// The counter of union work must stop at the limit: a uint16 that wrapped
// would give the budget back after 65536 attempts.
func TestSpendNestedSaturates(t *testing.T) {
	t.Parallel()

	c := &pctx{unions: 1}

	for i := range 70_000 {
		ok := c.spendNested()
		if want := i < maxNestedUnionWork; ok != want {
			t.Fatalf("attempt %d: spendNested() = %v, want %v", i+1, ok, want)
		}
	}
}
