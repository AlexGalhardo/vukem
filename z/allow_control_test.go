package z

import (
	"testing"
)

func TestStringAllowControl(t *testing.T) {
	t.Parallel()

	type rename struct {
		Name string `json:"name" validate:"required,rawtext,max=20"`
	}

	tests := []struct {
		name    string
		schema  AnySchema
		in      any
		wantKey string // "" means valid
	}{
		{"default refuses a bell", String(), "Beep\acat", "string.utf8"},
		{"AllowControl passes a bell", String().AllowControl(), "Beep\acat", ""},
		{"AllowControl passes NUL to the rules", String().AllowControl().Max(3), "ab\x00cd", "too_big.string"},
		{"AllowControl still refuses invalid UTF-8", String().AllowControl(), "a\xffb", "string.utf8"},
		{"rawtext tag", For[rename](), map[string]any{"name": "Beep\acat"}, ""},
		{"rawtext keeps other rules", For[rename](), map[string]any{"name": ""}, "required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := parseRoot[any](tt.schema, tt.in)

			switch is, _ := AsIssues(err); {
			case tt.wantKey == "" && err != nil:
				t.Errorf("Parse(%q) = %v, want no error", tt.in, err)
			case tt.wantKey != "" && (len(is) != 1 || is[0].Key != tt.wantKey):
				t.Errorf("Parse(%q) = %v, want one %s issue", tt.in, err, tt.wantKey)
			}
		})
	}
}

// A Preprocess func must not slip in what the first check would refuse,
// with or without AllowControl.
func TestStringAllowControlRevalidatesPreprocess(t *testing.T) {
	t.Parallel()

	_, err := String().AllowControl().Preprocess(func(s string) string { return s + "\xff" }).Parse("ok")
	if is, _ := AsIssues(err); len(is) != 1 || is[0].Key != "string.utf8" {
		t.Errorf("Parse(ok) = %v, want one string.utf8 issue", err)
	}
}
