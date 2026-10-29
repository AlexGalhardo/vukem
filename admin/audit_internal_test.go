package admin

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestChangeRows(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ch   Change
		want []changeRow
	}{
		{"scalar", Change{Before: "draft", After: "live"}, []changeRow{{Column: "c", Before: "draft", After: "live"}}},
		{"redacted", Change{Redacted: true}, []changeRow{{Column: "c", Redacted: true}}},
		{
			"json maps",
			Change{
				Before: map[string]any{"a": 1.0, "b": map[string]any{"x": "1", "y": "2"}, "gone": true},
				After:  map[string]any{"a": 1.0, "b": map[string]any{"x": "1", "y": "3"}, "new": "n"},
			},
			[]changeRow{
				{Column: "c.b.y", Before: "2", After: "3"},
				{Column: "c.gone", Before: "true", After: "∅"},
				{Column: "c.new", Before: "∅", After: "n"},
			},
		},
		{
			"json text",
			Change{Before: `{"score": 7}`, After: `{"score": 9}`},
			[]changeRow{{Column: "c.score", Before: "7", After: "9"}},
		},
		{
			"json and null",
			Change{Before: nil, After: `{"score": 9}`},
			[]changeRow{{Column: "c", Before: "∅", After: `{"score": 9}`}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tt.want, changeRows("c", tt.ch)); diff != "" {
				t.Errorf("changeRows() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
