package cli

import (
	"strings"
	"testing"
)

func TestSuggest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, want string
	}{
		{"make:controler", "make:controller"},
		{"make:mdoel", "make:model"},
		{"servee", "serve"},
		{"vresion", "version"},
		{"lsit", "list"},
		{"deploy", ""},
		{"migrate", ""},
		{"x", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := suggest(tt.name); got != tt.want {
				t.Errorf("suggest(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

func TestUnknownCommandSuggests(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{"make:controler", "Foo"}, {"help", "make:controler"}} {
		code, _, errOut := vukem(t, t.TempDir(), args...)
		if code != 2 || !strings.Contains(errOut, `did you mean "make:controller"?`) {
			t.Errorf("vukem %v = %d %q, want 2 and a suggestion", args, code, errOut)
		}
	}
}
