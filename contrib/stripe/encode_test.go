package stripe

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

type cents int64

func TestEncodeParams(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   Params
		want url.Values
	}{
		{
			name: "scalars",
			in:   Params{"email": "a@b.co", "quantity": 2, "amount": int64(990), "live": true, "rate": 1.5},
			want: url.Values{
				"email":    {"a@b.co"},
				"quantity": {"2"},
				"amount":   {"990"},
				"live":     {"true"},
				"rate":     {"1.5"},
			},
		},
		{
			name: "nested maps",
			in:   Params{"metadata": map[string]string{"user_id": "42"}, "data": Params{"tier": Params{"name": "pro"}}},
			want: url.Values{"metadata[user_id]": {"42"}, "data[tier][name]": {"pro"}},
		},
		{
			name: "slice of map[string]any",
			in:   Params{"line_items": []map[string]any{{"price": "price_a", "quantity": 1}, {"price": "price_b"}}},
			want: url.Values{
				"line_items[0][price]": {"price_a"}, "line_items[0][quantity]": {"1"},
				"line_items[1][price]": {"price_b"},
			},
		},
		{
			name: "slice of map[string]string",
			in:   Params{"items": []map[string]string{{"id": "si_1"}}},
			want: url.Values{"items[0][id]": {"si_1"}},
		},
		{
			name: "slice of Params",
			in:   Params{"line_items": []Params{{"price": "price_a", "quantity": 1}}},
			want: url.Values{"line_items[0][price]": {"price_a"}, "line_items[0][quantity]": {"1"}},
		},
		{
			name: "slices of scalars",
			in:   Params{"expand": []string{"customer"}, "ids": []int{7, 8}, "mixed": []any{"a", 2}},
			want: url.Values{
				"expand[0]": {"customer"},
				"ids[0]":    {"7"},
				"ids[1]":    {"8"},
				"mixed[0]":  {"a"},
				"mixed[1]":  {"2"},
			},
		},
		{
			name: "named scalar, map and slice types",
			in:   Params{"amount": cents(990), "counts": map[string]int{"a": 1}, "big": 1e21, "ptr": new("p")},
			want: url.Values{"amount": {"990"}, "counts[a]": {"1"}, "big": {"1000000000000000000000"}, "ptr": {"p"}},
		},
		{
			name: "nil is omitted",
			in:   Params{"skip": nil, "nilptr": (*time.Time)(nil), "keep": "x"},
			want: url.Values{"keep": {"x"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := encodeParams(tt.in)
			if err != nil {
				t.Fatalf("encodeParams(%v) = %v", tt.in, err)
			}

			parsed, err := url.ParseQuery(got)
			if err != nil {
				t.Fatalf("ParseQuery(%q) = %v", got, err)
			}

			if diff := cmp.Diff(tt.want, parsed); diff != "" {
				t.Errorf("encodeParams(%v) (-want +got):\n%s", tt.in, diff)
			}
		})
	}
}

func TestEncodeParamsRefusesUnknownTypes(t *testing.T) {
	t.Parallel()

	type custom struct{ A int }

	for _, p := range []Params{
		{"bad": custom{A: 1}},
		{"bad": []custom{{A: 1}}},
		{"bad": map[int]string{1: "a"}},
		{"bad": func() {}},
		{"nested": []map[string]any{{"bad": []custom{{A: 1}}}}},
	} {
		_, err := encodeParams(p)
		if err == nil || !strings.Contains(err.Error(), "unsupported") {
			t.Errorf("encodeParams(%v) = %v, want an unsupported type error", p, err)
		}
	}
}
