package i18n

import (
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/errs"
)

// Every built-in key exists in every built-in locale, with the same
// placeholders.
func TestBuiltin_CatalogsComplete(t *testing.T) {
	t.Parallel()

	cats, err := builtinCatalogs()
	if err != nil {
		t.Fatal(err)
	}

	if got := slices.Sorted(maps.Keys(cats)); !slices.Equal(got, []string{"en", "pt-BR"}) {
		t.Fatalf("built-in locales = %v, want [en pt-BR]", got)
	}

	for locale, msgs := range cats {
		if diff := cmp.Diff(slices.Sorted(maps.Keys(cats["en"])), slices.Sorted(maps.Keys(msgs))); diff != "" {
			t.Errorf("%s keys differ from en (-en +%s):\n%s", locale, locale, diff)
		}

		for key, m := range msgs {
			want := placeholder.FindAllString(cats["en"][key].text, -1)
			if got := placeholder.FindAllString(m.text, -1); !slices.Equal(got, want) {
				t.Errorf("%s %q placeholders = %v, want %v", locale, key, got, want)
			}
		}
	}
}

func TestBundle_AddBuiltin(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		def     string
		catalog map[string]map[string]any
		locale  string
		want    string
		locales []string
	}{
		{
			name:    "pt-BR served",
			catalog: map[string]map[string]any{"en": {}, "pt-BR": {}},
			locale:  "pt-BR",
			want:    "Essas credenciais não correspondem aos nossos registros.",
			locales: []string{"en", "pt-BR"},
		},
		{
			name:    "english",
			catalog: map[string]map[string]any{"en": {}, "pt-BR": {}},
			locale:  "en",
			want:    "These credentials do not match our records.",
			locales: []string{"en", "pt-BR"},
		},
		{
			name:    "app key wins",
			catalog: map[string]map[string]any{"en": {}, "pt-BR": {"auth.failed": "Dados inválidos."}},
			locale:  "pt-BR",
			want:    "Dados inválidos.",
			locales: []string{"en", "pt-BR"},
		},
		{
			name:    "app language key wins",
			catalog: map[string]map[string]any{"en": {}, "pt": {"auth.failed": "Dados inválidos."}, "pt-BR": {}},
			locale:  "pt-BR",
			want:    "Dados inválidos.",
			locales: []string{"en", "pt", "pt-BR"},
		},
		{
			name:    "language catalog gets the regional messages",
			catalog: map[string]map[string]any{"en": {}, "pt": {}},
			locale:  "pt",
			want:    "Essas credenciais não correspondem aos nossos registros.",
			locales: []string{"en", "pt"},
		},
		{
			name:    "default locale without a catalog gets none",
			catalog: map[string]map[string]any{"pt-BR": {}},
			locale:  "en",
			want:    "auth.failed",
			locales: []string{"pt-BR"},
		},
		{
			name:    "pt and pt-BR both filled",
			catalog: map[string]map[string]any{"en": {}, "pt": {}, "pt-BR": {}},
			locale:  "pt",
			want:    "Essas credenciais não correspondem aos nossos registros.",
			locales: []string{"en", "pt", "pt-BR"},
		},
		{
			name:    "other regions keep English",
			catalog: map[string]map[string]any{"en": {}, "pt-PT": {}},
			locale:  "pt-PT",
			want:    "These credentials do not match our records.",
			locales: []string{"en", "pt-PT"},
		},
		{
			name:    "no locale added",
			def:     "de",
			catalog: map[string]map[string]any{"de": {"auth.failed": "Falsche Daten."}},
			locale:  "en",
			want:    "Falsche Daten.",
			locales: []string{"de"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			b, err := New(Options{Default: tt.def})
			if err != nil {
				t.Fatal(err)
			}

			for l, msgs := range tt.catalog {
				if err := b.Add(l, msgs); err != nil {
					t.Fatal(err)
				}
			}

			if err := b.AddBuiltin(); err != nil {
				t.Fatalf("AddBuiltin() error = %v", err)
			}

			if got := b.T(tt.locale, "auth.failed"); got != tt.want {
				t.Errorf("T(%q, auth.failed) = %q, want %q", tt.locale, got, tt.want)
			}

			if diff := cmp.Diff(tt.locales, b.Locales()); diff != "" {
				t.Errorf("Locales() (-want +got):\n%s", diff)
			}
		})
	}
}

func TestBundle_Translator(t *testing.T) {
	t.Parallel()

	b := loadTest(t, Options{OnMissing: func(_, key string) { t.Errorf("OnMissing(%q) called", key) }})
	tr := b.Translator("pt-BR")

	if got, ok := tr("welcome", map[string]any{"name": "Ana"}); !ok || got != "Olá, Ana!" {
		t.Errorf(`Translator("pt-BR")("welcome") = %q, %v, want "Olá, Ana!", true`, got, ok)
	}

	if got, ok := tr("posts.count", map[string]any{"count": 2}); !ok || got != "2 posts" {
		t.Errorf(`Translator("pt-BR")("posts.count") = %q, %v, want "2 posts", true`, got, ok)
	}

	if got, ok := tr("nope", nil); ok {
		t.Errorf(`Translator("pt-BR")("nope") = %q, true, want false`, got)
	}
}

func TestMiddleware_InstallsErrorTranslator(t *testing.T) {
	t.Parallel()

	b, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}

	if err := b.Add("pt-BR", map[string]any{}); err != nil {
		t.Fatal(err)
	}

	if err := b.AddBuiltin(); err != nil {
		t.Fatal(err)
	}

	var got string

	h := Middleware(b, MiddlewareConfig{})(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = errs.Translate(r.Context(), "auth.token_ability", "fallback", map[string]any{"ability": "posts:delete"})
	}))

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/?lang=pt-BR", http.NoBody)
	h.ServeHTTP(httptest.NewRecorder(), r)

	if want := "Este token não tem permissão para posts:delete."; got != want {
		t.Errorf("errs.Translate in pt-BR = %q, want %q", got, want)
	}
}
