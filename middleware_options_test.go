package vukem

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/middleware"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

func sessionRoutes(r *router.Router) {
	hasSession := func(c *web.Ctx) error {
		if c.Session() == nil {
			return c.String(http.StatusOK, "no session")
		}

		return c.String(http.StatusOK, "session")
	}

	r.Get("/page", hasSession)
	r.Get("/webhooks/{name}", hasSession)
	r.Post("/webhooks/{name}", hasSession)
	r.Post("/form", hasSession)
}

func serve(t *testing.T, app *App, method, target string, crossSite bool) *httptest.ResponseRecorder {
	t.Helper()

	h, err := app.Handler()
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequestWithContext(t.Context(), method, target, http.NoBody)
	if crossSite {
		req.Header.Set("Sec-Fetch-Site", "cross-site")
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

func TestDefaultMiddlewareWith(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, WithRoutes(func(r *router.Router, app *App) error {
		sessionRoutes(r)

		return DefaultMiddlewareWith(app, r, MiddlewareOptions{
			SecureHeaders: func(cfg *middleware.SecureHeadersConfig) {
				cfg.CSPSources = map[string][]string{"script-src": {"https://plausible.io"}}
				cfg.ReportURI = "/csp-reports"
			},
			SessionBypass: []string{"/webhooks/"},
			CSRFBypass:    []string{"POST /webhooks/{name}"},
		})
	}))

	tests := []struct {
		method, target string
		crossSite      bool
		code           int
		body           string
	}{
		{http.MethodGet, "/page", false, http.StatusOK, "session"},
		{http.MethodGet, "/webhooks/stripe", false, http.StatusOK, "no session"},
		{http.MethodPost, "/webhooks/stripe", true, http.StatusOK, "no session"},
		{http.MethodPost, "/form", true, http.StatusForbidden, ""},
		{http.MethodPost, "/form", false, http.StatusOK, "session"},
	}

	for _, tt := range tests {
		rec := serve(t, app, tt.method, tt.target, tt.crossSite)
		if rec.Code != tt.code || (tt.body != "" && rec.Body.String() != tt.body) {
			t.Errorf("%s %s (cross-site %v) = %d %q, want %d %q",
				tt.method, tt.target, tt.crossSite, rec.Code, rec.Body.String(), tt.code, tt.body)
		}
	}

	rec := serve(t, app, http.MethodGet, "/page", false)
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "https://plausible.io") ||
		!strings.Contains(csp, "report-uri /csp-reports") || !strings.Contains(csp, "'nonce-") {
		t.Errorf("CSP = %q, want the extra script source, the report URI and the nonce", csp)
	}
}

func TestDefaultMiddlewareWithStateless(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, WithRoutes(func(r *router.Router, app *App) error {
		sessionRoutes(r)

		return DefaultMiddlewareWith(app, r, MiddlewareOptions{Stateless: true})
	}))

	if rec := serve(t, app, http.MethodGet, "/page", false); rec.Body.String() != "no session" ||
		rec.Header().Get("Set-Cookie") != "" {
		t.Errorf("stateless GET /page = %q (Set-Cookie %q), want no session and no cookie",
			rec.Body.String(), rec.Header().Get("Set-Cookie"))
	}

	// CSRF protection stays on without sessions.
	if rec := serve(t, app, http.MethodPost, "/form", true); rec.Code != http.StatusForbidden {
		t.Errorf("stateless cross-site POST /form = %d, want 403", rec.Code)
	}
}

func TestSecurity_A02_DefaultMiddlewareWithRefusesBadOptions(t *testing.T) {
	t.Parallel()

	tests := map[string]MiddlewareOptions{
		"unsafe CSP source": {SecureHeaders: func(cfg *middleware.SecureHeadersConfig) {
			cfg.CSPSources = map[string][]string{"style-src": {"'unsafe-inline'"}}
		}},
		"bad report URI": {SecureHeaders: func(cfg *middleware.SecureHeadersConfig) {
			cfg.ReportURI = "javascript:alert(1)"
		}},
		"bad session bypass":   {SessionBypass: []string{"/{bad"}},
		"empty session bypass": {SessionBypass: []string{""}},
		"bad CSRF bypass":      {CSRFBypass: []string{"/{bad"}},
	}

	for name, opts := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := router.New(&web.Services{})
			if err := DefaultMiddlewareWith(newTestApp(t), r, opts); err == nil {
				t.Errorf("DefaultMiddlewareWith(%s) = nil, want an error", name)
			}
		})
	}
}
