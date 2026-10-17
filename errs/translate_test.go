package errs

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

// fakeTranslator knows "greet" ("Olá, {name}") only.
func fakeTranslator(key string, params map[string]any) (string, bool) {
	if key != "greet" {
		return "", false
	}

	return fmt.Sprintf("Olá, %v", params["name"]), true
}

func TestTranslate(t *testing.T) {
	t.Parallel()

	tr := WithTranslator(t.Context(), fakeTranslator)

	tests := []struct {
		name string
		ctx  context.Context
		key  string
		want string
	}{
		{"no translator", t.Context(), "greet", "Hello"},
		{"translated", tr, "greet", "Olá, Ana"},
		{"unknown key", tr, "other", "Hello"},
		{"empty key", tr, "", "Hello"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := Translate(tt.ctx, tt.key, "Hello", map[string]any{"name": "Ana"})
			if got != tt.want {
				t.Errorf("Translate(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}

	if TranslatorFrom(t.Context()) != nil {
		t.Error("TranslatorFrom(empty ctx) != nil")
	}
}

func TestHTTPErrorLocalizedText(t *testing.T) {
	t.Parallel()

	tr := WithTranslator(t.Context(), fakeTranslator)
	keyed := NewKey(http.StatusForbidden, "greet", "Hello", map[string]any{"name": "Bia"})

	tests := []struct {
		name string
		ctx  context.Context
		err  *HTTPError
		want string
	}{
		{"keyed, translated", tr, keyed, "Olá, Bia"},
		{"keyed, no translator", t.Context(), keyed, "Hello"},
		{"unkeyed", tr, New(http.StatusForbidden, "nope"), "nope"},
		{"status text", tr, &HTTPError{Status: http.StatusNotFound}, "Not Found"},
		{"unknown key", tr, NewKey(http.StatusForbidden, "missing", "Fallback", nil), "Fallback"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.err.LocalizedText(tt.ctx); got != tt.want {
				t.Errorf("LocalizedText() = %q, want %q", got, tt.want)
			}
		})
	}

	if got := keyed.Error(); got != "403 Hello" {
		t.Errorf("Error() = %q, want the English fallback", got)
	}
}
