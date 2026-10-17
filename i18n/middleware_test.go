package i18n

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/web"
	"github.com/AlexGalhardo/vukem/z"
)

type fakeSession struct {
	web.Session
	data map[string]any
}

func (s fakeSession) Get(key string) any { return s.data[key] }

type result struct {
	Locale   string
	Hello    string
	Language string
	Vary     []string
}

func serve(t *testing.T, b *Bundle, cfg MiddlewareConfig, r *http.Request) result {
	t.Helper()

	var got result

	h := Middleware(b, cfg)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got.Locale = Locale(r.Context())
		got.Hello = T(r.Context(), "welcome", "name", "Ana")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	got.Language = rec.Header().Get("Content-Language")
	got.Vary = rec.Header().Values("Vary")

	return got
}

func TestMiddleware_Resolution(t *testing.T) {
	t.Parallel()

	b := loadTest(t, Options{})

	tests := []struct {
		name    string
		target  string
		cookie  string
		session any
		accept  string
		cfg     MiddlewareConfig
		want    result
	}{
		{name: "default", target: "/", want: result{"en", "Hello, Ana!", "en", []string{"Accept-Language"}}},
		{name: "query", target: "/?lang=pt-br", accept: "ru", want: result{"pt-BR", "Olá, Ana!", "pt-BR", nil}},
		{name: "cookie", target: "/", cookie: "pt", accept: "ru", want: result{"pt", "Olá, Ana!", "pt", nil}},
		{name: "session", target: "/", session: "ru", accept: "pt", want: result{"ru", "Hello, Ana!", "ru", nil}},
		{
			name: "query beats cookie and session", target: "/?lang=ru", cookie: "pt", session: "pt",
			want: result{"ru", "Hello, Ana!", "ru", nil},
		},
		{
			name: "invalid query falls through", target: "/?lang=../../etc/passwd", accept: "pt",
			want: result{"pt", "Olá, Ana!", "pt", []string{"Accept-Language"}},
		},
		{
			name: "unavailable cookie falls through", target: "/", cookie: "de", accept: "pt-PT",
			want: result{"pt", "Olá, Ana!", "pt", []string{"Accept-Language"}},
		},
		{
			name:    "non-string session ignored",
			target:  "/",
			session: 42,
			want:    result{"en", "Hello, Ana!", "en", []string{"Accept-Language"}},
		},
		{
			name: "accept-language q-values", target: "/", accept: "de;q=0.9, ru;q=0.5, pt-BR;q=0.8, *;q=0.1",
			want: result{"pt-BR", "Olá, Ana!", "pt-BR", []string{"Accept-Language"}},
		},
		{
			name: "q=0 excluded", target: "/", accept: "pt;q=0, ru;q=0.2",
			want: result{"ru", "Hello, Ana!", "ru", []string{"Accept-Language"}},
		},
		{
			name: "custom names", target: "/?locale=pt", cookie: "ru",
			cfg:  MiddlewareConfig{Query: "locale", Cookie: "-"},
			want: result{"pt", "Olá, Ana!", "pt", nil},
		},
		{
			name: "disabled query", target: "/?lang=pt", accept: "ru",
			cfg:  MiddlewareConfig{Query: "-"},
			want: result{"ru", "Hello, Ana!", "ru", []string{"Accept-Language"}},
		},
		{
			name: "header before query", target: "/?lang=ru", accept: "pt",
			cfg:  MiddlewareConfig{Order: []Source{SourceHeader, SourceQuery}},
			want: result{"pt", "Olá, Ana!", "pt", []string{"Accept-Language"}},
		},
		{
			name: "sources left out are off", target: "/", cookie: "pt", session: "ru", accept: "pt",
			cfg:  MiddlewareConfig{Order: []Source{SourceQuery}},
			want: result{"en", "Hello, Ana!", "en", nil},
		},
		{
			name: "session before cookie", target: "/", cookie: "pt", session: "ru",
			cfg:  MiddlewareConfig{Order: []Source{SourceSession, SourceCookie}},
			want: result{"ru", "Hello, Ana!", "ru", nil},
		},
		{
			name: "locales skip a disallowed query", target: "/?lang=ru", accept: "pt-BR",
			cfg:  MiddlewareConfig{Locales: []string{"en", "pt"}},
			want: result{"pt", "Olá, Ana!", "pt", []string{"Accept-Language"}},
		},
		{
			name: "locales match the same language", target: "/", accept: "pt",
			cfg:  MiddlewareConfig{Locales: []string{"en", "pt-BR"}},
			want: result{"pt-BR", "Olá, Ana!", "pt-BR", []string{"Accept-Language"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			if tt.session != nil {
				ctx = web.WithSession(ctx, fakeSession{data: map[string]any{"locale": tt.session}})
			}

			r := httptest.NewRequestWithContext(ctx, http.MethodGet, tt.target, http.NoBody)
			if tt.cookie != "" {
				r.AddCookie(&http.Cookie{Name: "lang", Value: tt.cookie})
			}

			if tt.accept != "" {
				r.Header.Set("Accept-Language", tt.accept)
			}

			if diff := cmp.Diff(tt.want, serve(t, b, tt.cfg, r)); diff != "" {
				t.Errorf("Middleware (-want +got):\n%s", diff)
			}
		})
	}
}

func TestMiddlewareConfig_Validate(t *testing.T) {
	t.Parallel()

	b := loadTest(t, Options{})

	tests := []struct {
		name    string
		cfg     MiddlewareConfig
		wantErr bool
	}{
		{name: "zero value", cfg: MiddlewareConfig{}},
		{name: "order and locales", cfg: MiddlewareConfig{
			Order:   []Source{SourceHeader, SourceQuery},
			Locales: []string{"en", "pt-br"},
		}},
		{name: "empty order", cfg: MiddlewareConfig{Order: []Source{}}, wantErr: true},
		{name: "unknown source", cfg: MiddlewareConfig{Order: []Source{"ip"}}, wantErr: true},
		{name: "duplicate source", cfg: MiddlewareConfig{Order: []Source{SourceQuery, SourceQuery}}, wantErr: true},
		{name: "invalid locale", cfg: MiddlewareConfig{Locales: []string{"en", "../x"}}, wantErr: true},
		{name: "locale without catalog", cfg: MiddlewareConfig{Locales: []string{"en", "de"}}, wantErr: true},
		{name: "locales without default", cfg: MiddlewareConfig{Locales: []string{"pt"}}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if err := tt.cfg.Validate(b); (err != nil) != tt.wantErr {
				t.Errorf("Validate() = %v, want error %t", err, tt.wantErr)
			}
		})
	}
}

func TestMiddleware_LocalizesValidation(t *testing.T) {
	t.Parallel()

	b, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}

	err = b.Add("pt", map[string]any{
		"validation": map[string]any{
			"required": "O campo {field} é obrigatório.",
			"too_small": map[string]any{
				"string": "O campo {field} deve ter pelo menos {min} caracteres.",
			},
			"attributes": map[string]any{"title": "título"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	schema := z.Struct[struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}](z.Shape{"title": z.String().Min(3), "body": z.String().Email()})

	var fields map[string][]string

	h := Middleware(b, MiddlewareConfig{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		web.NewCtx(w, r, &web.Services{}).Serve(func(c *web.Ctx) error {
			_, err := web.ParseQuery(c, schema)
			if ve, ok := errors.AsType[*errs.ValidationError](err); ok {
				fields = ve.Fields
			}

			return nil
		})
	}))

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/?title=ab&body=x&lang=pt", http.NoBody)
	h.ServeHTTP(httptest.NewRecorder(), r)

	want := map[string][]string{
		"title": {"O campo título deve ter pelo menos 3 caracteres."},
		"body":  {"The body field must be a valid email address."},
	}

	if diff := cmp.Diff(want, fields); diff != "" {
		t.Errorf("validation messages (-want +got):\n%s", diff)
	}
}

func TestParseAcceptLanguage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"en", []string{"en"}},
		{"da, en-GB;q=0.8, en;q=0.7", []string{"da", "en-GB", "en"}},
		{"en;q=0.5, fr;q=0.9, de", []string{"de", "fr", "en"}},
		{"en;q=0.5, fr;q=0.5", []string{"en", "fr"}},
		{"*, fr;q=0.1", []string{"fr"}},
		{"en;q=2, fr;q=abc, de;q=0, it ; q=0.3", []string{"it"}},
		{"xx-invalid-tag-here, ,;q=1, pt", []string{"pt"}},
		{strings.Repeat("en,", 100), []string{"en"}},
	}

	for _, tt := range tests {
		if diff := cmp.Diff(tt.want, parseAcceptLanguage(tt.in)); diff != "" {
			t.Errorf("parseAcceptLanguage(%q) (-want +got):\n%s", tt.in, diff)
		}
	}
}

func TestContextHelpers(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	if got := Locale(ctx); got != "" {
		t.Errorf("Locale(empty) = %q, want empty", got)
	}

	if got := T(ctx, "welcome"); got != "welcome" {
		t.Errorf("T(no bundle) = %q, want key", got)
	}

	b := loadTest(t, Options{})
	ctx = WithBundle(ctx, b)

	if got := T(ctx, "welcome", "name", "Ana"); got != "Hello, Ana!" {
		t.Errorf("T(default locale) = %q", got)
	}

	ctx = WithLocale(ctx, "pt")
	if got := T(ctx, "welcome", "name", "Ana"); got != "Olá, Ana!" {
		t.Errorf("T(pt) = %q", got)
	}

	if BundleFrom(ctx) != b {
		t.Error("BundleFrom() did not return the stored bundle")
	}
}

// TestSecurity_A05_LocaleInputRejected checks that locale values from the
// query, cookies, session and Accept-Language are only used when they are
// well-formed tags of an available catalog, so raw input never reaches
// catalog lookups, headers or file paths.
func TestSecurity_A05_LocaleInputRejected(t *testing.T) {
	t.Parallel()

	b := loadTest(t, Options{})

	for _, bad := range []string{
		"../en", "..%2Fen", "en/../../etc/passwd", "en\r\nSet-Cookie: x=1", "en\x00", "C:\\lang\\en",
		"<script>", strings.Repeat("e", 4096), "de",
	} {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
		r.URL.RawQuery = "lang=" + bad
		r.AddCookie(&http.Cookie{Name: "lang", Value: "x"})
		r.Header.Set("Accept-Language", bad)

		got := serve(t, b, MiddlewareConfig{}, r)
		if got.Locale != "en" || got.Language != "en" {
			t.Errorf(
				"locale input %q resolved to %q (Content-Language %q), want default en",
				bad,
				got.Locale,
				got.Language,
			)
		}
	}
}
