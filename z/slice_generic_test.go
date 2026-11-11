package z

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// The element type travels with the schema: Slice and Map are Schema[T] like
// every other schema, so they can go wherever one is expected.
var (
	_ Schema[[]string]       = Slice(String())
	_ Schema[[][]int]        = Slice(Slice(Int()))
	_ Schema[map[string]int] = Map(Int())
)

func issueSet(t *testing.T, err error) []string {
	t.Helper()

	var issues Issues
	if !errors.As(err, &issues) {
		t.Fatalf("error = %v, want Issues", err)
	}

	out := make([]string, len(issues))
	for i, is := range issues {
		out[i] = is.Path + ":" + is.Code
	}

	slices.Sort(out)

	return out
}

func TestSliceGeneric(t *testing.T) {
	t.Parallel()

	t.Run("parses to the element type", func(t *testing.T) {
		t.Parallel()

		got, err := Slice(String()).Parse([]any{"a", "b"})
		if err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff([]string{"a", "b"}, got); diff != "" {
			t.Errorf("Parse() (-want +got):\n%s", diff)
		}
	})

	t.Run("a single string is a one item list", func(t *testing.T) {
		t.Parallel()

		got, err := Slice(String()).Parse("a")
		if err != nil || !slices.Equal(got, []string{"a"}) {
			t.Errorf("Parse(\"a\") = %v, %v", got, err)
		}
	})

	t.Run("nested", func(t *testing.T) {
		t.Parallel()

		got, err := Slice(Slice(Int())).Parse([]any{[]any{1, 2}, []any{3}})
		if err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff([][]int{{1, 2}, {3}}, got); diff != "" {
			t.Errorf("Parse() (-want +got):\n%s", diff)
		}
	})

	t.Run("an unset element is the zero value", func(t *testing.T) {
		t.Parallel()

		got, err := Slice(Int().Optional()).Parse([]any{"", 2})
		if err != nil || !slices.Equal(got, []int{0, 2}) {
			t.Errorf("Parse() = %v, %v", got, err)
		}
	})

	for name, tt := range map[string]struct {
		schema SliceSchema[string]
		in     any
		want   []string
	}{
		"min":        {Slice(String()).Min(2), []any{"a"}, []string{":too_small"}},
		"max":        {Slice(String()).Max(1), []any{"a", "b"}, []string{":too_big"}},
		"len":        {Slice(String()).Len(2), []any{"a"}, []string{":too_small"}},
		"len over":   {Slice(String()).Len(1), []any{"a", "b"}, []string{":too_big"}},
		"unique":     {Slice(String()).Unique(), []any{"a", "a"}, []string{":not_unique"}},
		"element":    {Slice(String().Min(2)), []any{"ab", "ab", "c"}, []string{"2:too_small"}},
		"not a list": {Slice(String()), map[string]any{}, []string{":invalid_type"}},
		"empty":      {Slice(String()).NonEmpty(), []any{}, []string{":too_small"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := tt.schema.Parse(tt.in)
			if diff := cmp.Diff(tt.want, issueSet(t, err)); diff != "" {
				t.Errorf("issues (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("modifiers", func(t *testing.T) {
		t.Parallel()

		if got, err := Slice(
			String(),
		).Default([]string{"x"}).
			Parse(nil); err != nil ||
			!slices.Equal(got, []string{"x"}) {
			t.Errorf("Default: %v, %v", got, err)
		}

		sorted, err := Slice(String()).Transform(func(v []string) []string { slices.Sort(v); return v }).
			Parse([]any{"b", "a"})
		if err != nil || !slices.Equal(sorted, []string{"a", "b"}) {
			t.Errorf("Transform: %v, %v", sorted, err)
		}

		refined := Slice(String()).Refine(func(v []string) error {
			if len(v) > 1 {
				return errors.New("no")
			}

			return nil
		})
		if _, err := refined.Parse([]any{"a", "b"}); err == nil {
			t.Errorf("Refine accepted two items: %v", err)
		}

		if err := Slice(String()).Min(1).Check([]string{"a"}); err != nil {
			t.Errorf("Check(valid) = %v", err)
		}

		if err := Slice(String()).Min(2).Check([]string{"a"}); err == nil {
			t.Error("Check(too short) = nil")
		}

		_, err = Slice(String()).Min(2).Message(CodeTooSmall, "more!").Parse([]any{"a"})
		if err == nil || !strings.Contains(err.Error(), "more!") {
			t.Errorf("Message: %v", err)
		}

		_, err = Slice(String()).Message(CodeRequired, "need it").Parse(nil)
		if err == nil || !strings.Contains(err.Error(), "need it") {
			t.Errorf("Message on required: %v", err)
		}
	})

	t.Run("modifiers do not change the original", func(t *testing.T) {
		t.Parallel()

		base := Slice(String())
		_ = base.Min(5).Optional().Message(CodeTooSmall, "x")

		if _, err := base.Parse([]any{"a"}); err != nil {
			t.Errorf("base schema was modified: %v", err)
		}
	})
}

func TestMapGeneric(t *testing.T) {
	t.Parallel()

	t.Run("parses to the value type", func(t *testing.T) {
		t.Parallel()

		got, err := Map(Int()).Parse(map[string]any{"a": 1, "b": 2})
		if err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff(map[string]int{"a": 1, "b": 2}, got); diff != "" {
			t.Errorf("Parse() (-want +got):\n%s", diff)
		}
	})

	for name, tt := range map[string]struct {
		schema MapSchema[int]
		in     any
		want   []string
	}{
		"min":        {Map(Int()).Min(2), map[string]any{"a": 1}, []string{":too_small"}},
		"max":        {Map(Int()).Max(1), map[string]any{"a": 1, "b": 2}, []string{":too_big"}},
		"value path": {Map(Int()), map[string]any{"a": "x"}, []string{"a:invalid_type"}},
		"not object": {Map(Int()), []any{}, []string{":invalid_type"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := tt.schema.Parse(tt.in)
			if diff := cmp.Diff(tt.want, issueSet(t, err)); diff != "" {
				t.Errorf("issues (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("modifiers", func(t *testing.T) {
		t.Parallel()

		def := map[string]int{"x": 1}
		if got, err := Map(Int()).Default(def).Parse(nil); err != nil || got["x"] != 1 {
			t.Errorf("Default: %v, %v", got, err)
		}

		doubled, err := Map(Int()).Transform(func(m map[string]int) map[string]int {
			for k := range m {
				m[k] *= 2
			}

			return m
		}).Parse(map[string]any{"a": 2})
		if err != nil || doubled["a"] != 4 {
			t.Errorf("Transform: %v, %v", doubled, err)
		}

		refined := Map(Int()).Refine(func(m map[string]int) error {
			if len(m) > 1 {
				return errors.New("no")
			}

			return nil
		})
		if _, err := refined.Parse(map[string]any{"a": 1, "b": 2}); err == nil {
			t.Error("Refine accepted two keys")
		}

		if err := Map(Int().Positive()).Check(map[string]int{"a": 1}); err != nil {
			t.Errorf("Check(valid) = %v", err)
		}

		if err := Map(Int().Positive()).Check(map[string]int{"a": -1}); err == nil {
			t.Error("Check(invalid) = nil")
		}

		_, err = Map(Int()).Min(2).Message(CodeTooSmall, "more!").Describe("d").Parse(map[string]any{"a": 1})
		if err == nil || !strings.Contains(err.Error(), "more!") {
			t.Errorf("Message: %v", err)
		}
	})
}

// A default is a value of the schema: a caller that appends to or writes into
// one result must not change what the next Parse returns.
func TestSliceAndMapDefaultsAreNotShared(t *testing.T) {
	t.Parallel()

	slice := Slice(String()).Default(append(make([]string, 0, 4), "x"))

	first, err := slice.Parse(nil)
	if err != nil {
		t.Fatal(err)
	}

	first[0] = "changed"
	_ = append(first, "more") // spare capacity: writes into the shared array

	second, _ := slice.Parse(nil)
	if diff := cmp.Diff([]string{"x"}, second); diff != "" {
		t.Errorf("slice default after mutating a result (-want +got):\n%s", diff)
	}

	if got := second[:cap(second)]; len(got) > 1 && got[1] != "" {
		t.Errorf("spare capacity of the default was written: %v", got)
	}

	m := Map(Int()).Default(map[string]int{"a": 1})

	firstMap, _ := m.Parse(nil)
	firstMap["a"], firstMap["b"] = 9, 2

	secondMap, _ := m.Parse(nil)
	if diff := cmp.Diff(map[string]int{"a": 1}, secondMap); diff != "" {
		t.Errorf("map default after mutating a result (-want +got):\n%s", diff)
	}
}
