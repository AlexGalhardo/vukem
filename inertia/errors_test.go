package inertia_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/inertia"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/view"
	"github.com/AlexGalhardo/vukem/web"
)

const secret = `pq: relation "users" does not exist at C:\app\models\user.go:42`

func errorRoutes(r *router.Router) {
	r.Get("/missing", func(*web.Ctx) error { return errs.New(http.StatusNotFound, "Record not found") })
	r.Get("/forbidden", func(*web.Ctx) error { return errs.ErrForbidden })
	r.Get("/conflict", func(*web.Ctx) error { return errs.ErrConflict })
	r.Get("/boom", func(*web.Ctx) error { return errors.New(secret) })
	r.Get("/invalid", func(*web.Ctx) error { return errs.NewValidation().Add("q", "too short").Add("q", "bad") })
	r.Get("/wrapped", func(*web.Ctx) error { return errs.Wrap(http.StatusNotFound, errors.New(secret)) })
	r.Get("/unavailable", func(*web.Ctx) error {
		return errs.New(http.StatusServiceUnavailable, "replica "+secret)
	})
}

// errorHarness serves errorRoutes behind the Inertia middleware with the
// adapter's ErrorPage installed, as vukem.WithInertia does.
func errorHarness(t *testing.T, cfg inertia.Config, debug bool) http.Handler {
	t.Helper()

	views, err := view.New(view.Config{FS: fstest.MapFS{
		"app.html": {Data: []byte(`{{/* layout: none */}}<html><body>{{.Inertia}}</body></html>`)},
	}})
	if err != nil {
		t.Fatal(err)
	}

	in, err := inertia.New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	r := router.New(&web.Services{Views: views, ErrorPage: in.ErrorPage, Debug: debug})
	r.Use(in.Middleware, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			inertia.Share(req.Context(), "appName", "Shop")
			next.ServeHTTP(w, req)
		})
	})
	errorRoutes(r)

	h, err := r.Handler()
	if err != nil {
		t.Fatal(err)
	}

	return h
}

func serve(t *testing.T, h http.Handler, target string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

func TestErrorPageForInertiaRequests(t *testing.T) {
	t.Parallel()

	h := errorHarness(t, baseConfig(), false)

	tests := []struct {
		target  string
		status  int
		message string
	}{
		{"/missing", 404, "Record not found"},
		{"/forbidden", 403, "Forbidden"},
		{"/conflict", 409, "Conflict"},
		{"/boom", 500, "Internal Server Error"},
		{"/unavailable", 503, "Service Unavailable"},
		// Unmatched routes never run route middleware; the page is still an
		// Inertia page.
		{"/no-such-page", 404, "Not Found"},
	}

	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			t.Parallel()

			rec := serve(t, h, tt.target, xhr())
			if rec.Code != tt.status || rec.Header().Get("X-Inertia") != "true" ||
				!strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
				t.Fatalf(
					"GET %s = %d %v %s, want %d Inertia JSON",
					tt.target,
					rec.Code,
					rec.Header(),
					rec.Body,
					tt.status,
				)
			}

			p := page(t, rec)
			if p["component"] != "Error" || p["url"] != tt.target || p["version"] != "v1" {
				t.Errorf("page = %v, want component Error, url %s, version v1", p, tt.target)
			}

			props, _ := p["props"].(map[string]any)
			want := map[string]any{"status": float64(tt.status), "message": tt.message}
			got := map[string]any{"status": props["status"], "message": props["message"]}

			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("props (-want +got):\n%s", diff)
			}

			if _, ok := props["errors"]; !ok {
				t.Errorf("props lack errors: %v", props)
			}

			if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Vary") != "X-Inertia" {
				t.Errorf("headers = %v, want Cache-Control no-store and Vary X-Inertia", rec.Header())
			}
		})
	}

	// Field errors of a 422 reach the page as Inertia's errors prop (first
	// message per field).
	rec := serve(t, h, "/invalid", xhr())
	props, _ := page(t, rec)["props"].(map[string]any)

	if diff := cmp.Diff(map[string]any{
		"status": float64(422), "title": "Validation failed", "message": "1 field(s) are invalid.",
		"errors": map[string]any{"q": "too short"},
	}, map[string]any{
		"status": props["status"], "title": props["title"], "message": props["message"], "errors": props["errors"],
	}); rec.Code != 422 || diff != "" {
		t.Errorf("GET /invalid = %d, props (-want +got):\n%s", rec.Code, diff)
	}

	// Props shared by middleware on matched routes reach the error page.
	props, _ = page(t, serve(t, h, "/missing", xhr()))["props"].(map[string]any)
	if props["appName"] != "Shop" {
		t.Errorf("shared props = %v, want appName", props)
	}
}

func TestErrorPageComponentConfigurable(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.ErrorComponent = "Errors/Show"

	if got := page(t, serve(t, errorHarness(t, cfg, false), "/missing", xhr()))["component"]; got != "Errors/Show" {
		t.Errorf("component = %v, want Errors/Show", got)
	}
}

func TestErrorPageOtherClients(t *testing.T) {
	t.Parallel()

	h := errorHarness(t, baseConfig(), false)

	// Full page loads keep the plain HTML status page.
	rec := serve(t, h, "/missing", map[string]string{"Accept": "text/html"})
	if rec.Code != 404 || rec.Header().Get("X-Inertia") != "" ||
		!strings.Contains(
			rec.Body.String(),
			"<h1>404 Not Found</h1>",
		) || strings.Contains(rec.Body.String(), "data-page") {
		t.Errorf("full load = %d %v %s, want the HTML status page", rec.Code, rec.Header(), rec.Body)
	}

	// API clients keep problem+json.
	rec = serve(t, h, "/missing", map[string]string{"Accept": "application/json"})
	if rec.Code != 404 || rec.Header().Get("Content-Type") != "application/problem+json" {
		t.Errorf("JSON client = %d %v %s, want problem+json", rec.Code, rec.Header(), rec.Body)
	}

	// The version check still runs first.
	if rec := serve(t, h, "/missing", xhr("X-Inertia-Version", "old")); rec.Code != http.StatusConflict {
		t.Errorf("stale version = %d, want 409", rec.Code)
	}
}

func TestErrorPageDebugKeepsTheReport(t *testing.T) {
	t.Parallel()

	// In development a 5xx shows the debug report (in the Inertia modal);
	// 4xx still get the page.
	h := errorHarness(t, baseConfig(), true)

	if rec := serve(t, h, "/boom", xhr()); rec.Code != 500 || rec.Header().Get("X-Inertia") != "" {
		t.Errorf("debug 500 = %d %v, want the debug page", rec.Code, rec.Header())
	}

	if rec := serve(t, h, "/missing", xhr()); rec.Code != 404 || page(t, rec)["component"] != "Error" {
		t.Errorf("debug 404 = %d %s, want the Error page", rec.Code, rec.Body)
	}
}

func TestErrorPageFallsBackWhenPropsFail(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.Shared = inertia.Props{"user": func(context.Context) (any, error) { return nil, errors.New(secret) }}

	rec := serve(t, errorHarness(t, cfg, false), "/missing", xhr())
	if rec.Code != 404 || rec.Header().Get("X-Inertia") != "" || strings.Contains(rec.Body.String(), "pq:") {
		t.Errorf("GET /missing = %d %v %s, want the plain HTML page", rec.Code, rec.Header(), rec.Body)
	}
}

func TestSecurity_A10_InertiaErrorPageHidesInternals(t *testing.T) {
	t.Parallel()

	h := errorHarness(t, baseConfig(), false)

	for _, target := range []string{"/boom", "/wrapped", "/unavailable"} {
		rec := serve(t, h, target, xhr())

		body := rec.Body.String()
		for _, leak := range []string{"pq:", "relation", "user.go", `C:\`, "replica"} {
			if strings.Contains(body, leak) || strings.Contains(strings.ReplaceAll(body, `\\`, `\`), leak) {
				t.Errorf("GET %s leaks %q: %s", target, leak, body)
			}
		}

		if page(t, rec)["component"] != "Error" {
			t.Errorf("GET %s = %s, want the Error page", target, body)
		}
	}
}
