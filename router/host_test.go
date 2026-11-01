package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/web"
)

// VF-015 (HexaScore): routes served on one host only, e.g. the paid API at
// api.example.com/v1 next to the app on example.com.
func TestHostGroups(t *testing.T) {
	t.Parallel()

	r := newRouter()
	r.Get("/v1/games", text("app"))
	r.Host("api.example.com", func(g *Group) {
		g.Group("/v1", func(v1 *Group) {
			v1.Name("api.").Get("/games", text("api")).Name("games")
			v1.Get("/games/{id}", text("api one")).Name("game")
		})
	})

	tests := []struct {
		host, path string
		code       int
		body       string
	}{
		{"api.example.com", "/v1/games", 200, "api"},
		{"api.example.com:8443", "/v1/games", 200, "api"},
		{"example.com", "/v1/games", 200, "app"},
		{"other.example.com", "/v1/games/1", 404, ""},
	}

	for _, tt := range tests {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.path, http.NoBody)
		req.Host = tt.host

		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != tt.code || tt.body != "" && rec.Body.String() != tt.body {
			t.Errorf("GET %s%s = %d %q, want %d %q", tt.host, tt.path, rec.Code, rec.Body.String(), tt.code, tt.body)
		}
	}

	if got, err := r.URL("api.games"); err != nil || got != "//api.example.com/v1/games" {
		t.Errorf(`URL("api.games") = %q, %v, want "//api.example.com/v1/games"`, got, err)
	}

	if got, err := r.URL("api.game", "id", 7); err != nil || got != "//api.example.com/v1/games/7" {
		t.Errorf(`URL("api.game") = %q, %v`, got, err)
	}

	var hosts []string
	for _, info := range r.Routes() {
		hosts = append(hosts, info.Host+info.Path)
	}

	if diff := cmp.Diff(
		[]string{"/v1/games", "api.example.com/v1/games", "api.example.com/v1/games/{id}"},
		hosts,
	); diff != "" {
		t.Errorf("Routes() host+path (-want +got):\n%s", diff)
	}
}

func TestHostValidation(t *testing.T) {
	t.Parallel()

	for _, host := range []string{"", "api.example.com/v1", "{tenant}.example.com", "API.example.com"} {
		r := newRouter()
		r.Host(host, func(g *Group) { g.Get("/x", text("x")) })

		if _, err := r.Handler(); err == nil {
			t.Errorf("Host(%q) compiled, want an error", host)
		}
	}
}

func TestHostOnly(t *testing.T) {
	t.Parallel()

	stamp := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Web-Stack", "1")
			next.ServeHTTP(w, r)
		})
	}

	api := func(g *Group) {
		g.Get("/games", text("api games")).Name("games")
	}

	r := newRouter()
	r.Use(stamp)
	r.Get("/login", text("login"))
	r.Group("/api/v1", api).Name("api.")
	r.Host("api.example.com", func(g *Group) {
		g.Get("/", text("index"))
		g.Group("/v1", api).Name("public.")
	}).Name("host.").Only()

	tests := []struct {
		host, method, path string
		code               int
		body               string
		webStack           bool
	}{
		{"api.example.com", "GET", "/v1/games", 200, "api games", true},
		{"api.example.com", "GET", "/", 200, "index", true},
		{"api.example.com", "GET", "/login", 404, "", false},
		{"api.example.com:8443", "GET", "/api/v1/games", 404, "", false},
		{"api.example.com", "POST", "/v1/games", 405, "", false},
		{"example.com", "GET", "/login", 200, "login", true},
		{"example.com", "GET", "/api/v1/games", 200, "api games", true},
		{"example.com", "GET", "/v1/games", 404, "", false},
	}

	for _, tt := range tests {
		req := httptest.NewRequestWithContext(t.Context(), tt.method, tt.path, http.NoBody)
		req.Host = tt.host

		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if got := rec.Header().Get("X-Web-Stack") == "1"; rec.Code != tt.code ||
			tt.body != "" && rec.Body.String() != tt.body || got != tt.webStack {
			t.Errorf("%s %s%s = %d %q web=%v, want %d %q web=%v",
				tt.method, tt.host, tt.path, rec.Code, rec.Body.String(), got, tt.code, tt.body, tt.webStack)
		}

		if tt.code == 405 && rec.Header().Get("Allow") != "GET, HEAD" {
			t.Errorf("Allow = %q", rec.Header().Get("Allow"))
		}
	}

	if got, err := r.URL("host.public.games"); err != nil || got != "//api.example.com/v1/games" {
		t.Errorf("URL = %q, %v", got, err)
	}

	bad := newRouter()
	bad.Group("/x", nil).Only()

	if _, err := bad.Handler(); err == nil {
		t.Error("Only on a group without a host compiled")
	}
}

func TestHostOnlyUsesGroupErrorHandler(t *testing.T) {
	t.Parallel()

	r := newRouter()
	r.Host("api.example.com", func(g *Group) {
		g.Get("/v1/games", text("games"))
	}).ErrorHandler(func(next web.ErrorHandler) web.ErrorHandler {
		return func(c *web.Ctx, err error) {
			c.Writer().Header().Set("X-API-Errors", "1")
			next(c, err)
		}
	}).Only()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/nope", http.NoBody)
	req.Host = "api.example.com"

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound || rec.Header().Get("X-API-Errors") != "1" {
		t.Errorf("GET /nope = %d, X-API-Errors %q", rec.Code, rec.Header().Get("X-API-Errors"))
	}
}
