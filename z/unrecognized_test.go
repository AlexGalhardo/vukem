package z_test

import (
	"reflect"
	"testing"
	"time"

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
