package social

import (
	"testing"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/i18n"
)

// Callback errors carry a key with a built-in translation; the English
// message stays the fallback.
func TestErrorsTranslated(t *testing.T) {
	t.Parallel()

	b, err := i18n.New(i18n.Options{})
	if err != nil {
		t.Fatal(err)
	}

	if err := b.Add("pt-BR", map[string]any{}); err != nil {
		t.Fatal(err)
	}

	if err := b.AddBuiltin(); err != nil {
		t.Fatal(err)
	}

	pt := errs.WithTranslator(t.Context(), b.Translator("pt-BR"))

	tests := []struct {
		name string
		err  *errs.HTTPError
		want string
	}{
		{"unknown provider", ErrUnknownProvider, "Provedor de login desconhecido."},
		{"state", ErrState, "A tentativa de login expirou ou é inválida. Tente novamente."},
		{"denied", ErrAccessDenied, "O login foi cancelado."},
		{"provider", ErrProvider, "Não foi possível entrar. Tente novamente."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.err.LocalizedText(pt); got != tt.want {
				t.Errorf("LocalizedText(pt-BR) = %q, want %q", got, tt.want)
			}

			if got := tt.err.LocalizedText(t.Context()); got != tt.err.Message {
				t.Errorf("LocalizedText() = %q, want the English %q", got, tt.err.Message)
			}
		})
	}
}
