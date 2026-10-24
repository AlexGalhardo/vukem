package z

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestTagRules_Map(t *testing.T) {
	t.Parallel()

	type doc struct {
		Labels map[string]string `json:"labels" validate:"required,min=1,max=2,dive,min=2"`
		Nested map[string][]int  `json:"nested" validate:"dive,dive,min=1"`
	}

	s := FromTags[doc]()

	tests := []struct {
		name    string
		in      map[string]any
		want    doc
		wantErr string
	}{
		{
			name: "valid",
			in:   map[string]any{"labels": map[string]any{"a": "ab"}, "nested": map[string]any{"k": []any{1, 2}}},
			want: doc{Labels: map[string]string{"a": "ab"}, Nested: map[string][]int{"k": {1, 2}}},
		},
		{name: "too few entries", in: map[string]any{"labels": map[string]any{}}, wantErr: "labels"},
		{
			name:    "too many entries",
			in:      map[string]any{"labels": map[string]any{"a": "ab", "b": "ab", "c": "ab"}},
			wantErr: "labels",
		},
		{name: "value rule", in: map[string]any{"labels": map[string]any{"a": "x"}}, wantErr: "labels.a"},
		{
			name:    "wrong value type",
			in:      map[string]any{"labels": map[string]any{"a": 1}},
			wantErr: "labels.a",
		},
		{name: "required", in: map[string]any{}, wantErr: "labels"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := s.Parse(tt.in)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Parse(%v) error = %v, want one naming %q", tt.in, err, tt.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("Parse(%v) = %v", tt.in, err)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Fatalf("Parse(%v) mismatch (-want +got):\n%s", tt.in, diff)
			}
		})
	}
}

func TestTagRules_MapPanics(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		build func()
		want  string
	}{
		"int key": {func() { FromTags[struct{ A map[int]string }]() }, "key must be string"},
		"unknown rule": {func() {
			FromTags[struct {
				A map[string]string `validate:"unique"`
			}]()
		}, "unknown rule"},
		"bad arg": {func() {
			FromTags[struct {
				A map[string]string `validate:"min=x"`
			}]()
		}, "needs an integer"},
		"unsupported kind": {func() { FromTags[struct{ A chan int }]() }, "want a string, number"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			defer func() {
				msg, _ := recover().(string)
				if !strings.Contains(msg, tc.want) {
					t.Fatalf("panic = %q, want it to contain %q", msg, tc.want)
				}
			}()

			tc.build()
		})
	}
}
