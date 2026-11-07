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

func crossOriginApp(t *testing.T, stateless bool) *App {
	t.Helper()

	return newTestApp(t, WithRoutes(func(r *router.Router, app *App) error {
		sessionRoutes(r)
		r.Post("/subscribe/{list}", func(c *web.Ctx) error {
			if c.Session() != nil {
				return c.String(http.StatusOK, "session")
			}

			return c.String(http.StatusOK, "no session")
		})
		r.Post("/api/profile", func(c *web.Ctx) error { return c.String(http.StatusOK, "profile") })

		return DefaultMiddlewareWith(app, r, MiddlewareOptions{
			Stateless: stateless,
			CrossOrigin: []CrossOriginRoute{{
				Pattern: "POST /subscribe/{list}",
				CORS:    middleware.CORSConfig{AllowedOrigins: []string{"https://site.example"}},
			}},
		})
	}))
}

func TestCrossOriginRoute(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, method, target string
		header               map[string]string
		code                 int
		allowOrigin          string
	}{
		{"allowed origin", http.MethodPost, "/subscribe/x", map[string]string{
			"Origin": "https://site.example", "Sec-Fetch-Site": "cross-site",
		}, http.StatusOK, "https://site.example"},
		{"other origin", http.MethodPost, "/subscribe/x", map[string]string{
			"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site",
		}, http.StatusForbidden, ""},
		{"fetch metadata without origin", http.MethodPost, "/subscribe/x", map[string]string{
			"Sec-Fetch-Site": "cross-site",
		}, http.StatusForbidden, ""},
		{"same origin", http.MethodPost, "/subscribe/x", map[string]string{
			"Origin": "http://example.com", "Sec-Fetch-Site": "same-origin",
		}, http.StatusOK, ""},
		{"non-browser client", http.MethodPost, "/subscribe/x", nil, http.StatusOK, ""},
		{"preflight allowed", http.MethodOptions, "/subscribe/x", map[string]string{
			"Origin": "https://site.example", "Access-Control-Request-Method": "POST",
		}, http.StatusNoContent, "https://site.example"},
		{"preflight other origin", http.MethodOptions, "/subscribe/x", map[string]string{
			"Origin": "https://evil.example", "Access-Control-Request-Method": "POST",
		}, http.StatusMethodNotAllowed, ""},
		{"trust does not leak to other routes", http.MethodPost, "/api/profile", map[string]string{
			"Origin": "https://site.example", "Sec-Fetch-Site": "cross-site",
		}, http.StatusForbidden, ""},
		{"trust does not leak to form routes", http.MethodPost, "/form", map[string]string{
			"Origin": "https://site.example", "Sec-Fetch-Site": "cross-site",
		}, http.StatusForbidden, ""},
	}

	for _, stateless := range []bool{false, true} {
		app := crossOriginApp(t, stateless)

		for _, tt := range tests {
			h, err := app.Handler()
			if err != nil {
				t.Fatal(err)
			}

			req := httptest.NewRequestWithContext(t.Context(), tt.method, tt.target, http.NoBody)
			for k, v := range tt.header {
				req.Header.Set(k, v)
			}

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if got := rec.Header().Get("Access-Control-Allow-Origin"); rec.Code != tt.code || got != tt.allowOrigin {
				t.Errorf("stateless=%v %s: %d allow-origin %q, want %d %q",
					stateless, tt.name, rec.Code, got, tt.code, tt.allowOrigin)
			}

			if tt.code == http.StatusOK && strings.Contains(tt.target, "subscribe") &&
				(rec.Body.String() != "no session" || rec.Header().Get("Set-Cookie") != "") {
				t.Errorf("stateless=%v %s: body %q, cookie %q, want a cookieless request",
					stateless, tt.name, rec.Body.String(), rec.Header().Get("Set-Cookie"))
			}
		}
	}
}

func TestCrossOriginRouteValidation(t *testing.T) {
	t.Parallel()

	ok := middleware.CORSConfig{AllowedOrigins: []string{"https://site.example"}}
	tests := map[string]CrossOriginRoute{
		"no origins": {Pattern: "POST /a"},
		"credentials": {
			Pattern: "POST /a",
			CORS:    middleware.CORSConfig{AllowedOrigins: ok.AllowedOrigins, AllowCredentials: true},
		},
		"origin has path": {
			Pattern: "POST /a",
			CORS:    middleware.CORSConfig{AllowedOrigins: []string{"https://site.example/x"}},
		},
		"origin no scheme": {Pattern: "POST /a", CORS: middleware.CORSConfig{AllowedOrigins: []string{"site.example"}}},
		"bad pattern":      {Pattern: "/{bad", CORS: ok},
		"empty pattern":    {Pattern: "", CORS: ok},
	}

	for name, route := range tests {
		r := router.New(&web.Services{})
		if err := DefaultMiddlewareWith(
			newTestApp(t),
			r,
			MiddlewareOptions{CrossOrigin: []CrossOriginRoute{route}},
		); err == nil {
			t.Errorf("DefaultMiddlewareWith(%s) = nil, want an error", name)
		}
	}
}

func TestSecurity_A01_CrossOriginRouteDoesNotTrustOriginGlobally(t *testing.T) {
	t.Parallel()

	app := crossOriginApp(t, false)
	h, err := app.Handler()
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/profile", http.NoBody)
	req.Header.Set("Origin", "https://site.example")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.AddCookie(&http.Cookie{Name: "vukem_session", Value: "abc"})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("credentialed cross-site POST to a non-cross-origin route = %d, want 403", rec.Code)
	}
}
