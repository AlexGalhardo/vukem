package vukem

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/i18n"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

func TestWithTranslations(t *testing.T) {
	t.Parallel()

	lang := fstest.MapFS{
		"en.json": {Data: []byte(`{"hello": "Hello"}`)},
		"pt.json": {Data: []byte(`{"hello": "Olá"}`)},
	}
	views := fstest.MapFS{"home.html": {Data: []byte(`{{/* layout: none */}}{{t "hello"}}`)}}

	routes := WithRoutes(func(r *router.Router, app *App) error {
		r.Get("/", func(c *web.Ctx) error { return c.Render("home", nil) })
		r.Get("/go", func(c *web.Ctx) error { return c.String(http.StatusOK, i18n.T(c.Context(), "hello")) })

		return DefaultMiddleware(app, r)
	})

	tests := []struct {
		name     string
		env      map[string]string
		target   string
		accept   string
		want     string
		wantLang string
	}{
		{"default", nil, "/", "", "Hello", "en"},
		{"accept-language", nil, "/", "pt-BR,en;q=0.5", "Olá", "pt"},
		{"query in handler", nil, "/go?lang=pt", "", "Olá", "pt"},
		{"APP_LOCALE", map[string]string{"APP_LOCALE": "pt"}, "/", "", "Olá", "pt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			app, err := New(WithSource(testSource(tt.env)), WithLogger(discardLogger()),
				WithTranslations(lang), WithViews(views), routes)
			if err != nil {
				t.Fatal(err)
			}

			if app.I18n == nil {
				t.Fatal("App.I18n = nil, want the bundle")
			}

			h, err := app.Handler()
			if err != nil {
				t.Fatal(err)
			}

			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.target, http.NoBody)
			if tt.accept != "" {
				r.Header.Set("Accept-Language", tt.accept)
			}

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)

			if got := rec.Body.String(); got != tt.want {
				t.Errorf("GET %s body = %q, want %q", tt.target, got, tt.want)
			}

			if got := rec.Header().Get("Content-Language"); got != tt.wantLang {
				t.Errorf("GET %s Content-Language = %q, want %q", tt.target, got, tt.wantLang)
			}
		})
	}
}

func TestWithTranslations_BootErrors(t *testing.T) {
	t.Parallel()

	for name, tt := range map[string]struct {
		lang fstest.MapFS
		env  map[string]string
	}{
		"broken catalog":     {fstest.MapFS{"en.json": {Data: []byte(`{`)}}, nil},
		"invalid APP_LOCALE": {fstest.MapFS{}, map[string]string{"APP_LOCALE": "../en"}},
	} {
		if _, err := New(
			WithSource(testSource(tt.env)),
			WithLogger(discardLogger()),
			WithTranslations(tt.lang),
		); err == nil {
			t.Errorf("%s: New() error = nil, want error", name)
		}
	}
}

// The framework's built-in messages (i18n.Bundle.AddBuiltin) translate
// keyed errors in the request locale, under the app's own catalog.
func TestWithTranslations_BuiltinMessages(t *testing.T) {
	t.Parallel()

	lang := fstest.MapFS{
		"en.json": {Data: []byte(`{"auth": {"unverified": "Please verify your email first."}}`)},
		"pt.json": {Data: []byte(`{}`)},
	}

	routes := WithRoutes(func(r *router.Router, app *App) error {
		r.Get("/", func(*web.Ctx) error {
			return errs.NewKey(http.StatusForbidden, "auth.unverified", "Your email address is not verified.", nil)
		})

		return DefaultMiddleware(app, r)
	})

	app, err := New(WithSource(testSource(nil)), WithLogger(discardLogger()), WithTranslations(lang), routes)
	if err != nil {
		t.Fatal(err)
	}

	h, err := app.Handler()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct{ lang, want string }{
		{"pt-BR", "Seu endereço de e-mail não foi verificado."},
		{"en", "Please verify your email first."},
	}
	for _, tt := range tests {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
		r.Header.Set("Accept", "application/json")
		r.Header.Set("Accept-Language", tt.lang)

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)

		if !strings.Contains(rec.Body.String(), tt.want) {
			t.Errorf("GET / in %s = %d %s, want detail %q", tt.lang, rec.Code, rec.Body.String(), tt.want)
		}
	}
}
