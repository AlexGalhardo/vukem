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
	"github.com/AlexGalhardo/vukem/z"
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

func TestDefaultMiddlewareWithI18n(t *testing.T) {
	t.Parallel()

	lang := fstest.MapFS{
		"en.json": {Data: []byte(`{"hello": "Hello"}`)},
		"pt.json": {Data: []byte(`{"hello": "Olá"}`)},
		"ru.json": {Data: []byte(`{"hello": "Привет"}`)},
	}

	app := newTestApp(t, WithTranslations(lang), WithRoutes(func(r *router.Router, app *App) error {
		r.Get("/", func(c *web.Ctx) error { return c.String(http.StatusOK, i18n.T(c.Context(), "hello")) })

		return DefaultMiddlewareWith(app, r, MiddlewareOptions{I18n: &i18n.MiddlewareConfig{
			Order:   []i18n.Source{i18n.SourceQuery, i18n.SourceHeader},
			Locales: []string{"en", "pt"},
		}})
	}))

	h, err := app.Handler()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name, target, cookie, accept, want string
	}{
		{"cookie source off", "/", "pt", "", "Hello"},
		{"query", "/?lang=pt", "", "", "Olá"},
		{"disallowed query falls to header", "/?lang=ru", "", "pt", "Olá"},
		{"disallowed header", "/", "", "ru", "Hello"},
	}

	for _, tt := range tests {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.target, http.NoBody)
		if tt.cookie != "" {
			r.AddCookie(&http.Cookie{Name: "lang", Value: tt.cookie})
		}

		if tt.accept != "" {
			r.Header.Set("Accept-Language", tt.accept)
		}

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)

		if got := rec.Body.String(); got != tt.want {
			t.Errorf("%s: GET %s body = %q, want %q", tt.name, tt.target, got, tt.want)
		}
	}
}

func TestDefaultMiddlewareWithI18n_BootErrors(t *testing.T) {
	t.Parallel()

	lang := fstest.MapFS{"en.json": {Data: []byte(`{"hello": "Hello"}`)}}

	tests := map[string]struct {
		opts []Option
		cfg  i18n.MiddlewareConfig
	}{
		"without translations": {nil, i18n.MiddlewareConfig{}},
		"unknown source":       {[]Option{WithTranslations(lang)}, i18n.MiddlewareConfig{Order: []i18n.Source{"ip"}}},
		"locale without catalog": {
			[]Option{WithTranslations(lang)},
			i18n.MiddlewareConfig{Locales: []string{"en", "de"}},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := router.New(&web.Services{})
			if err := DefaultMiddlewareWith(
				newTestApp(t, tt.opts...),
				r,
				MiddlewareOptions{I18n: &tt.cfg},
			); err == nil {
				t.Errorf("DefaultMiddlewareWith(%s) = nil, want an error", name)
			}
		})
	}
}

func TestAPIMiddlewareWithI18n(t *testing.T) {
	t.Parallel()

	lang := fstest.MapFS{
		"en.json": {Data: []byte(`{"hello": "Hello"}`)},
		"pt.json": {Data: []byte(`{"hello": "Olá", "validation": {"too_small": {"string": "Curto demais."}}}`)},
	}

	schema := z.Struct[struct {
		Q string `json:"q"`
	}](z.Shape{"q": z.String().Min(3)})

	tests := []struct {
		name     string
		opts     APIMiddlewareOptions
		target   string
		cookie   string
		accept   string
		wantCode int
		wantBody string
		wantLang string
	}{
		{"query", APIMiddlewareOptions{}, "/?lang=pt&q=abc", "", "", 200, "Olá", "pt"},
		{"header", APIMiddlewareOptions{}, "/?q=abc", "", "pt", 200, "Olá", "pt"},
		{"cookie ignored", APIMiddlewareOptions{}, "/?q=abc", "pt", "", 200, "Hello", "en"},
		{"localized 422", APIMiddlewareOptions{}, "/?q=a", "", "pt", 422, "Curto demais.", "pt"},
		{
			"custom order",
			APIMiddlewareOptions{I18n: &i18n.MiddlewareConfig{Order: []i18n.Source{i18n.SourceCookie}}},
			"/?lang=en&q=abc", "pt", "", 200, "Olá", "pt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			app := newTestApp(t, WithTranslations(lang), WithRoutes(func(r *router.Router, app *App) error {
				r.Get("/", func(c *web.Ctx) error {
					if _, err := web.ParseQuery(c, schema); err != nil {
						return err
					}

					return c.String(http.StatusOK, i18n.T(c.Context(), "hello"))
				})

				return APIMiddlewareWith(app, r, tt.opts)
			}))

			h, err := app.Handler()
			if err != nil {
				t.Fatal(err)
			}

			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.target, http.NoBody)
			r.Header.Set("Accept", "application/json")

			if tt.cookie != "" {
				r.AddCookie(&http.Cookie{Name: "lang", Value: tt.cookie})
			}

			if tt.accept != "" {
				r.Header.Set("Accept-Language", tt.accept)
			}

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)

			if rec.Code != tt.wantCode || !strings.Contains(rec.Body.String(), tt.wantBody) ||
				rec.Header().Get("Content-Language") != tt.wantLang {
				t.Errorf(
					"GET %s = %d %q (Content-Language %q), want %d containing %q (%q)",
					tt.target,
					rec.Code,
					rec.Body.String(),
					rec.Header().Get("Content-Language"),
					tt.wantCode,
					tt.wantBody,
					tt.wantLang,
				)
			}
		})
	}
}

func TestAPIMiddlewareWithI18n_BootErrors(t *testing.T) {
	t.Parallel()

	lang := fstest.MapFS{"en.json": {Data: []byte(`{"hello": "Hello"}`)}}

	tests := map[string]struct {
		opts []Option
		cfg  i18n.MiddlewareConfig
	}{
		"without translations": {nil, i18n.MiddlewareConfig{}},
		"locales without default": {
			[]Option{WithTranslations(lang)},
			i18n.MiddlewareConfig{Locales: []string{"pt"}},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := router.New(&web.Services{})
			if err := APIMiddlewareWith(newTestApp(t, tt.opts...), r, APIMiddlewareOptions{I18n: &tt.cfg}); err == nil {
				t.Errorf("APIMiddlewareWith(%s) = nil, want an error", name)
			}
		})
	}
}
