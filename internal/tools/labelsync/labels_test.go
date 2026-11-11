package main

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestParse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      string
		want    []label
		wantErr string // substring; empty means valid
	}{
		{
			name: "valid with comment and blank line",
			in:   "# type\n\nbug|d73a4a|Something broke\n",
			want: []label{{Name: "bug", Color: "d73a4a", Description: "Something broke"}},
		},
		{
			name: "description with colon and spaces",
			in:   "priority:p1|b60205|Fix: before the next release\n",
			want: []label{{Name: "priority:p1", Color: "b60205", Description: "Fix: before the next release"}},
		},
		{name: "two fields", in: "bug|d73a4a\n", wantErr: "line 1"},
		{name: "four fields", in: "# c\nbug|d73a4a|a|b\n", wantErr: "line 2"},
		{name: "invalid color letters", in: "bug|zzzzzz|x\n", wantErr: "line 1"},
		{name: "invalid color length", in: "bug|12345|x\n", wantErr: "color"},
		{name: "empty name", in: "|d73a4a|x\n", wantErr: "name"},
		{name: "duplicate name", in: "bug|d73a4a|x\nbug|d73a4a|y\n", wantErr: "line 2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parse(strings.NewReader(tt.in))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("parse() error = %v, want substring %q", err, tt.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("parse() error = %v", err)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("parse() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestCommands(t *testing.T) {
	t.Parallel()

	l := label{Name: "area:orm", Color: "c5def5", Description: "orm stuff"}
	want := []string{"label", "create", "area:orm", "--color", "c5def5", "--description", "orm stuff", "--force"}

	if diff := cmp.Diff(want, args(l)); diff != "" {
		t.Errorf("args() mismatch (-want +got):\n%s", diff)
	}
}
