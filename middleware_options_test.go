package vukem

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/csrf"
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
		"bad session bypass":            {SessionBypass: []string{"/{bad"}},
		"empty session bypass":          {SessionBypass: []string{""}},
		"stateless with session bypass": {Stateless: true, SessionBypass: []string{"/webhooks/"}},
		"bad CSRF bypass":               {CSRFBypass: []string{"/{bad"}},
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

func TestDefaultMiddlewareWithStatelessSessionFeatures(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, WithRoutes(func(r *router.Router, app *App) error {
		r.Post("/flash", func(c *web.Ctx) error {
			if err := c.Flash("status", "saved"); err != nil {
				return err
			}

			return c.String(http.StatusOK, "flashed")
		})
		r.Get("/token", func(c *web.Ctx) error { return c.String(http.StatusOK, "token="+csrf.Token(c.Context())) })

		return DefaultMiddlewareWith(app, r, MiddlewareOptions{Stateless: true})
	}))

	// Flash fails closed with a 500 instead of panicking or dropping the message.
	if rec := serve(t, app, http.MethodPost, "/flash", false); rec.Code != http.StatusInternalServerError {
		t.Errorf("stateless POST /flash = %d %q, want 500", rec.Code, rec.Body.String())
	}

	if rec := serve(t, app, http.MethodGet, "/token", false); rec.Body.String() != "token=" {
		t.Errorf("stateless csrf.Token = %q, want empty", rec.Body.String())
	}
}

// TestBypassMatchesRouter checks that a bypass pattern skips exactly the
// requests the router serves with the same pattern.
func TestBypassMatchesRouter(t *testing.T) {
	t.Parallel()

	patterns := []string{"/webhooks/", "POST /hooks/{id}", "/exact", "api.example.com/x"}

	r := router.New(&web.Services{})
	for _, p := range patterns {
		r.Handle(p, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(p)) }))
	}

	routed, err := r.Handler()
	if err != nil {
		t.Fatal(err)
	}

	skipped := false
	mw, err := bypass(patterns, func(http.Handler) http.Handler {
		return http.HandlerFunc(func(http.ResponseWriter, *http.Request) { skipped = false })
	})
	if err != nil {
		t.Fatal(err)
	}

	matcher := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { skipped = true }))

	for _, tt := range []struct{ method, target string }{
		{http.MethodGet, "/webhooks/stripe"},
		{http.MethodGet, "/webhooks/a/b"},
		{http.MethodGet, "/webhooks"},
		{http.MethodPost, "/hooks/1"},
		{http.MethodGet, "/hooks/1"},
		{http.MethodPost, "/hooks/1/x"},
		{http.MethodGet, "/exact"},
		{http.MethodGet, "/exact/"},
		{http.MethodGet, "http://api.example.com/x"},
		{http.MethodGet, "http://other.example.com/x"},
		{http.MethodGet, "/x"},
	} {
		req := httptest.NewRequestWithContext(t.Context(), tt.method, tt.target, http.NoBody)

		rec := httptest.NewRecorder()
		routed.ServeHTTP(rec, req)
		wantSkip := rec.Code == http.StatusOK

		matcher.ServeHTTP(httptest.NewRecorder(), req)
		if skipped != wantSkip {
			t.Errorf("%s %s: bypassed = %v, router served the pattern = %v (%d %q)",
				tt.method, tt.target, skipped, wantSkip, rec.Code, rec.Body.String())
		}
	}
}
