package z

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

type point struct{ Lat, Lng float64 }

func pointSchema() TupleSchema[point] {
	return Tuple[point](Float().Min(-90).Max(90), Float().Min(-180).Max(180))
}

func TestTuple(t *testing.T) {
	t.Parallel()

	s := pointSchema()

	t.Run("parses a list into the fields", func(t *testing.T) {
		t.Parallel()

		got, err := s.Parse([]any{1.5, 2.5})
		if err != nil || got != (point{Lat: 1.5, Lng: 2.5}) {
			t.Errorf("Parse = %+v, %v", got, err)
		}
	})

	t.Run("wrong length", func(t *testing.T) {
		t.Parallel()

		for in, want := range map[string]string{"short": "too_small", "long": "too_big"} {
			list := []any{1.5}
			if in == "long" {
				list = []any{1.5, 2.5, 3.5}
			}

			_, err := s.Parse(list)

			is, ok := AsIssues(err)
			if !ok || len(is) != 1 || is[0].Code != want || is[0].Key != "exact.array" || is[0].Params["len"] != 2 {
				t.Errorf("%s: issues = %+v", in, is)
			}
		}
	})

	t.Run("item errors point at the position", func(t *testing.T) {
		t.Parallel()

		type place struct {
			Location point `json:"location"`
		}

		_, err := Struct[place](Shape{"location": s}).Parse(map[string]any{"location": []any{91.0, 200.0}})
		if diff := cmp.Diff([]string{"location.0:too_big", "location.1:too_big"}, issueSet(t, err)); diff != "" {
			t.Errorf("issues (-want +got):\n%s", diff)
		}
	})

	t.Run("not a list", func(t *testing.T) {
		t.Parallel()

		_, err := s.Parse(map[string]any{"lat": 1})
		if diff := cmp.Diff([]string{":invalid_type"}, issueSet(t, err)); diff != "" {
			t.Errorf("issues (-want +got):\n%s", diff)
		}
	})

	t.Run("check reads the fields in order", func(t *testing.T) {
		t.Parallel()

		if err := s.Check(point{Lat: 10, Lng: 20}); err != nil {
			t.Errorf("Check(valid) = %v", err)
		}

		if err := s.Check(point{Lat: 100}); err == nil {
			t.Error("Check(lat 100) = nil")
		}
	})

	t.Run("boot checks", func(t *testing.T) {
		t.Parallel()

		mustPanic(t, "not a struct", func() { Tuple[int](Int()) })
		mustPanic(t, "too few items", func() { Tuple[point](Float()) })
		mustPanic(t, "item type does not fit", func() { Tuple[point](Float(), String()) })
	})

	t.Run("only exported fields count", func(t *testing.T) {
		t.Parallel()

		type inner struct{ X int }

		type pair struct {
			A    string
			skip string
			C    int
			In   inner
		}

		_ = pair{}.skip

		got, err := Tuple[pair](String(), Int(), Custom(func(any) (inner, error) { return inner{X: 1}, nil })).
			Parse([]any{"a", 3, "x"})
		if err != nil || got.A != "a" || got.C != 3 || got.In.X != 1 {
			t.Errorf("Parse = %+v, %v", got, err)
		}
	})

	t.Run("json schema", func(t *testing.T) {
		t.Parallel()

		want := map[string]any{
			"type": "array",
			"prefixItems": []any{
				map[string]any{"type": "number", "minimum": -90.0, "maximum": 90.0},
				map[string]any{"type": "number", "minimum": -180.0, "maximum": 180.0},
			},
			"items": false, "minItems": 2, "maxItems": 2,
		}
		if diff := cmp.Diff(want, JSONSchema(s)); diff != "" {
			t.Errorf("schema (-want +got):\n%s", diff)
		}
	})
}
