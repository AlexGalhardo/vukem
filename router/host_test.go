package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"
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
