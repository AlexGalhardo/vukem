package z_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/z"
)

type unrecognizedAddr struct {
	City string `json:"city"`
}

type unrecognizedIn struct {
	Name   string                      `json:"name"`
	Alias  string                      `              query:"alias"`
	Home   unrecognizedAddr            `json:"home"`
	Other  *unrecognizedAddr           `json:"other"`
	Stops  []unrecognizedAddr          `json:"stops"`
	ByName map[string]unrecognizedAddr `json:"byName"`
	Meta   map[string]any              `json:"meta"`
	At     time.Time                   `json:"at"`
	Skip   string                      `json:"-"`
}

func TestUnrecognized(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   any
		want []string
	}{
		{"all known", map[string]any{"name": "a", "alias": "b", "home": map[string]any{"city": "x"}}, nil},
		{"unknown at the top", map[string]any{"name": "a", "extra": 1}, []string{"extra"}},
		{"nested struct", map[string]any{"home": map[string]any{"city": "x", "zip": 1}}, []string{"home.zip"}},
		{"nested pointer", map[string]any{"other": map[string]any{"zip": 1}}, []string{"other.zip"}},
		{
			"slice element",
			map[string]any{"stops": []any{map[string]any{"city": "x"}, map[string]any{"zip": 1}}},
			[]string{"stops.1.zip"},
		},
		{
			"map value",
			map[string]any{"byName": map[string]any{"k": map[string]any{"zip": 1}}},
			[]string{"byName.k.zip"},
		},
		{"free-form is not walked", map[string]any{"meta": map[string]any{"anything": 1}, "at": "2020-01-01"}, nil},
		{"json dash is unknown", map[string]any{"Skip": "x"}, []string{"Skip"}},
		{"several, sorted", map[string]any{"b": 1, "a": 1}, []string{"a", "b"}},
		{"not an object", []any{1}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got []string

			for _, is := range z.Unrecognized(reflect.TypeFor[unrecognizedIn](), tt.in) {
				if is.Code != z.CodeUnrecognizedKeys || is.Message == "" {
					t.Fatalf("issue = %+v, want code %q and a message", is, z.CodeUnrecognizedKeys)
				}

				got = append(got, is.Path)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Fatalf("Unrecognized() paths mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

type unrecognizedHostile struct {
	Items map[string]unrecognizedAddr `json:"items"`
	Rows  []unrecognizedAddr          `json:"rows"`
}

func unknownBody(n int) map[string]any {
	m := make(map[string]any, n)
	for i := range n {
		m[fmt.Sprintf("k%06d", i)] = 1
	}

	return m
}

func TestSecurity_UnrecognizedIsBounded(t *testing.T) {
	t.Parallel()

	typ := reflect.TypeFor[unrecognizedHostile]()

	t.Run("one huge key holding many unknown keys", func(t *testing.T) {
		t.Parallel()

		body := map[string]any{"items": map[string]any{
			strings.Repeat("x", 500_000): map[string]any{"zip": 1, "zap": 1},
			"a":                          unknownBody(50_000),
		}}

		start := time.Now()
		got := z.Unrecognized(typ, body)

		if len(got) > 100 {
			t.Fatalf("got %d issues, want at most 100", len(got))
		}

		for _, is := range got {
			if len(is.Path) > 1024+3 || len(is.Message) > 1024+200 {
				t.Fatalf("issue path %d bytes, message %d bytes, want bounded", len(is.Path), len(is.Message))
			}
		}

		if d := time.Since(start); d > 2*time.Second {
			t.Fatalf("Unrecognized took %v, want it bounded", d)
		}
	})

	t.Run("stops out of nested slices and maps", func(t *testing.T) {
		t.Parallel()

		rows := make([]any, 1000)
		for i := range rows {
			rows[i] = map[string]any{"a": 1, "b": 1}
		}

		got := z.Unrecognized(typ, map[string]any{"rows": rows})
		if len(got) != 100 {
			t.Fatalf("got %d issues, want exactly 100", len(got))
		}
	})

	t.Run("exact paths for ordinary bodies", func(t *testing.T) {
		t.Parallel()

		got := z.Unrecognized(typ, map[string]any{"rows": []any{map[string]any{"zip": 1}}})
		if len(got) != 1 || got[0].Path != "rows.0.zip" {
			t.Fatalf("got %+v, want one issue at rows.0.zip", got)
		}
	})

	t.Run("a long or unsafe key is never repeated", func(t *testing.T) {
		t.Parallel()

		for _, key := range []string{
			strings.Repeat("é", 300),
			strings.Repeat("a", 255) + "é",
			strings.Repeat("a", 65),
			"x\nhint: y",
		} {
			got := z.Unrecognized(typ, map[string]any{key: 1})
			if len(got) != 1 || got[0].Path != "?" || !utf8.ValidString(got[0].Message) {
				t.Errorf("Unrecognized(%.20q) = %+v, want one issue at ?", key, got)
			}
		}
	})

	for _, n := range []int{100, 101} {
		t.Run(fmt.Sprintf("%d unknown keys", n), func(t *testing.T) {
			t.Parallel()

			if got := z.Unrecognized(typ, unknownBody(n)); len(got) != 100 {
				t.Fatalf("got %d issues, want 100", len(got))
			}
		})
	}

	t.Run("99 unknown keys are all reported", func(t *testing.T) {
		t.Parallel()

		if got := z.Unrecognized(typ, unknownBody(99)); len(got) != 99 {
			t.Fatalf("got %d issues, want 99", len(got))
		}
	})
}
