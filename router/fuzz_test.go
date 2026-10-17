package router

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/web"
)

// FuzzServePath routes arbitrary methods and request targets: the router
// never answers with a server error, and any redirect it issues (path
// cleaning, trailing slashes) stays on the same origin.
func FuzzServePath(f *testing.F) {
	r := newRouter()
	r.Get("/", text("home"))
	r.Get("/posts/{id}", func(c *web.Ctx) error { return c.String(200, c.Param("id")) })
	r.Post("/posts", text("created"))
	r.Get("/files/{path...}", func(c *web.Ctx) error { return c.String(200, c.Param("path")) })
	r.Get("/dir/", text("dir"))
	r.Group("/admin", func(g *Group) { g.Get("/users/{id}", text("user")) })

	h, err := r.Handler()
	if err != nil {
		f.Fatal(err)
	}

	for _, s := range []string{
		"/", "/posts/1", "//evil.com/", "/posts/../../etc/passwd", "/files/a/../b", "/dir", "/%2e%2e/",
		"/./\\evil.com", "/admin/users/1?x=1", "/files/%00", "/posts/1/", "/a//b", `/\evil.com/..`,
	} {
		f.Add("GET", s)
	}

	f.Add("POST", "/posts")
	f.Add("DELETE", "/posts/1")
	f.Add("CONNECT", "evil.com:443")

	f.Fuzz(func(t *testing.T, method, target string) {
		if !strings.HasPrefix(target, "/") {
			return
		}

		req, err := http.NewRequestWithContext(t.Context(), method, "http://example.com"+target, http.NoBody)
		if err != nil {
			return
		}

		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		if w.Code >= 500 {
			t.Fatalf("%s %q = %d, want no server error", method, target, w.Code)
		}

		loc := w.Header().Get("Location")
		if loc == "" {
			return
		}

		u, err := url.Parse(loc)
		if err != nil || u.Scheme != "" || u.Host != "" || strings.HasPrefix(loc, "//") ||
			strings.HasPrefix(loc, `/\`) {
			t.Fatalf("%s %q redirected to %q, off the origin", method, target, loc)
		}
	})
}
