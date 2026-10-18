package router

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/web"
)

func newRouter() *Router {
	return New(&web.Services{Log: slog.New(slog.DiscardHandler)})
}

func serve(t *testing.T, h http.Handler, method, target string, hdr ...string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), method, target, http.NoBody)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

func text(s string) web.HandlerFunc {
	return func(c *web.Ctx) error { return c.String(200, s) }
}

func TestVerbsAndParams(t *testing.T) {
	t.Parallel()

	r := newRouter()
	r.Get("/", text("home"))
	r.Get("/posts/{id}", func(c *web.Ctx) error { return c.String(200, "post "+c.Param("id")) })
	r.Post("/posts", text("store"))
	r.Put("/posts/{id}", text("put"))
	r.Patch("/posts/{id}", text("patch"))
	r.Delete("/posts/{id}", text("delete"))
	r.Options("/posts", text("options"))
	r.Get("/files/{path...}", func(c *web.Ctx) error { return c.String(200, c.Param("path")) })
	r.Get("/exact/{$}", text("exact"))
	r.Match([]string{"get", "post"}, "/both", text("both"))
	r.Any("/any", text("any"))

	tests := []struct {
		method, path, want string
		status             int
	}{
		{"GET", "/", "home", 200},
		{"GET", "/posts/7", "post 7", 200},
		{"HEAD", "/posts/7", "post 7", 200}, // net/http drops HEAD bodies on the wire; the recorder keeps them
		{"POST", "/posts", "store", 200},
		{"PUT", "/posts/1", "put", 200},
		{"PATCH", "/posts/1", "patch", 200},
		{"DELETE", "/posts/1", "delete", 200},
		{"OPTIONS", "/posts", "options", 200},
		{"GET", "/files/a/b/c.txt", "a/b/c.txt", 200},
		{"GET", "/exact/", "exact", 200},
		{"POST", "/both", "both", 200},
		{"DELETE", "/any", "any", 200},
	}

	for _, tt := range tests {
		rec := serve(t, r, tt.method, tt.path)
		if rec.Code != tt.status || rec.Body.String() != tt.want {
			t.Errorf("%s %s = %d %q, want %d %q", tt.method, tt.path, rec.Code, rec.Body.String(), tt.status, tt.want)
		}
	}
}

func TestNotFoundAndMethodNotAllowed(t *testing.T) {
	t.Parallel()

	r := newRouter()
	r.Get("/posts", text("x"))
	r.Post("/posts", text("y"))

	rec := serve(t, r, "GET", "/nope", "Accept", "application/json")
	if rec.Code != 404 || rec.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("404 = %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}

	rec = serve(t, r, "DELETE", "/posts", "Accept", "application/json")
	if rec.Code != 405 || rec.Header().Get("Allow") != "GET, HEAD, POST" {
		t.Fatalf("405 = %d Allow=%q", rec.Code, rec.Header().Get("Allow"))
	}

	rec = serve(t, r, "GET", "/nope")
	if rec.Code != 404 || !strings.Contains(rec.Body.String(), "404 Not Found") {
		t.Fatalf("HTML 404 = %d %q", rec.Code, rec.Body.String())
	}
}

func TestGroupsNamesAndMiddleware(t *testing.T) {
	t.Parallel()

	var order []string
	mark := func(name string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, req)
			})
		}
	}

	r := newRouter()
	r.UseServer(mark("server"))
	r.Use(mark("router"))

	r.Group("/admin", func(g *Group) {
		g.Use(mark("admin"))
		g.Get("/dash", text("dash")).Name("dash")

		g.Group("api/", func(g *Group) {
			g.Use(mark("api"))
			g.Get("/users/{id}", text("user")).Name("users.show").Use(mark("route"))
		}).Name("api.")
	}).Name("admin.")

	rec := serve(t, r, "GET", "/admin/api/users/5")
	if rec.Body.String() != "user" {
		t.Fatalf("nested route = %d %q", rec.Code, rec.Body.String())
	}
	if want := []string{"server", "router", "admin", "api", "route"}; !slices.Equal(order, want) {
		t.Fatalf("middleware order = %v, want %v", order, want)
	}

	order = nil
	serve(t, r, "GET", "/missing")
	if want := []string{"server"}; !slices.Equal(order, want) {
		t.Fatalf("unmatched middleware = %v, want only server", order)
	}

	u, err := r.URL("admin.api.users.show", "id", 5)
	if err != nil || u != "/admin/api/users/5" {
		t.Fatalf("URL() = %q, %v", u, err)
	}
	if u, _ := r.URL("admin.dash"); u != "/admin/dash" {
		t.Fatalf("URL(admin.dash) = %q", u)
	}
}

func TestURLGeneration(t *testing.T) {
	t.Parallel()

	r := newRouter()
	r.Get("/posts/{id}/comments/{comment}", text("")).Name("comments.show")
	r.Get("/files/{path...}", text("")).Name("files")
	r.Get("/home/{$}", text("")).Name("home")

	tests := []struct {
		name    string
		params  []any
		want    string
		wantErr string
	}{
		{name: "comments.show", params: []any{"id", 1, "comment", "a b"}, want: "/posts/1/comments/a%20b"},
		{name: "files", params: []any{"path", "a/b c/d"}, want: "/files/a/b%20c/d"},
		{name: "home", want: "/home/"},
		{name: "comments.show", params: []any{"id", 1}, wantErr: `missing param "comment"`},
		{name: "comments.show", params: []any{"id", 1, "comment", 2, "extra", 3}, wantErr: `unknown param "extra"`},
		{name: "comments.show", params: []any{"id"}, wantErr: "name/value pairs"},
		{name: "comments.show", params: []any{1, 2}, wantErr: "is not a string"},
		{name: "nope", wantErr: `no route named "nope"`},
	}

	for _, tt := range tests {
		got, err := r.URL(tt.name, tt.params...)
		if tt.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("URL(%s, %v) error = %v, want %q", tt.name, tt.params, err, tt.wantErr)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("URL(%s, %v) = %q, %v, want %q", tt.name, tt.params, got, err, tt.want)
		}
	}

	// Handlers reach the generator through the Ctx.
	r2 := newRouter()
	r2.Get("/p/{id}", text("")).Name("p")
	r2.Get("/go", func(c *web.Ctx) error { return c.RedirectRoute("p", "id", 9) })
	if rec := serve(t, r2, "GET", "/go"); rec.Header().Get("Location") != "/p/9" {
		t.Fatalf("RedirectRoute Location = %q", rec.Header().Get("Location"))
	}
}

func TestBuildErrors(t *testing.T) {
	t.Parallel()

	t.Run("conflict", func(t *testing.T) {
		t.Parallel()

		r := newRouter()
		r.Get("/posts/{id}", text("")).Name("a")
		r.Get("/posts/{slug}", text("")).Name("b")

		_, err := r.Handler()
		if err == nil || !strings.Contains(err.Error(), "GET /posts/{slug} [b]") ||
			!strings.Contains(err.Error(), "GET /posts/{id} [a]") {
			t.Fatalf("Handler() error = %v, want both routes named", err)
		}
		if _, err := r.URL("a"); err == nil {
			t.Fatal("URL after build error = nil")
		}

		defer func() {
			if recover() == nil {
				t.Fatal("ServeHTTP did not panic on build error")
			}
		}()
		serve(t, r, "GET", "/posts/1")
	})

	t.Run("duplicate name", func(t *testing.T) {
		t.Parallel()

		r := newRouter()
		r.Get("/a", text("")).Name("x")
		r.Get("/b", text("")).Name("x")

		if _, err := r.Handler(); err == nil || !strings.Contains(err.Error(), `duplicate route name "x"`) {
			t.Fatalf("Handler() error = %v", err)
		}
	})

	t.Run("invalid pattern", func(t *testing.T) {
		t.Parallel()

		r := newRouter()
		r.Get("/a/{bad", text(""))

		if _, err := r.Handler(); err == nil {
			t.Fatal("Handler() error = nil for invalid pattern")
		}
	})
}

type postController struct{}

func (postController) Index(c *web.Ctx) error   { return c.String(200, "index") }
func (postController) Create(c *web.Ctx) error  { return c.String(200, "create") }
func (postController) Store(c *web.Ctx) error   { return c.String(200, "store") }
func (postController) Show(c *web.Ctx) error    { return c.String(200, "show "+c.Param("post")) }
func (postController) Edit(c *web.Ctx) error    { return c.String(200, "edit") }
func (postController) Update(c *web.Ctx) error  { return c.String(200, "update") }
func (postController) Destroy(c *web.Ctx) error { return c.String(200, "destroy") }

type partialController struct{}

func (partialController) Index(c *web.Ctx) error { return c.String(200, "i") }
func (partialController) Show(c *web.Ctx) error  { return c.String(200, "s") }
func (partialController) Store(c *web.Ctx) error { return c.String(200, "st") }

func TestResources(t *testing.T) {
	t.Parallel()

	r := newRouter()
	r.Resource("posts", postController{})
	r.APIResource("posts.comments", postController{}).Only("index", "show")
	r.Resource("categories", partialController{}).Except("store")
	r.Group("/api", func(g *Group) {
		g.Name("api.")
		g.APIResource("posts.comments", postController{}).Shallow()
		g.APIResource("tags", postController{}).Only("index").Shallow() // not nested: no-op
	})

	got := r.Routes()
	var lines []string
	for _, ri := range got {
		lines = append(lines, fmt.Sprintf("%s %s %s", ri.Method, ri.Path, ri.Name))
	}

	want := []string{
		"GET /posts posts.index",
		"GET /posts/create posts.create",
		"POST /posts posts.store",
		"GET /posts/{post} posts.show",
		"GET /posts/{post}/edit posts.edit",
		"PUT /posts/{post} posts.update",
		"PATCH /posts/{post} ",
		"DELETE /posts/{post} posts.destroy",
		"GET /posts/{post}/comments posts.comments.index",
		"GET /posts/{post}/comments/{comment} posts.comments.show",
		"GET /categories categories.index",
		"GET /categories/{category} categories.show",
		"GET /api/posts/{post}/comments api.posts.comments.index",
		"POST /api/posts/{post}/comments api.posts.comments.store",
		"GET /api/comments/{comment} api.posts.comments.show",
		"PUT /api/comments/{comment} api.posts.comments.update",
		"PATCH /api/comments/{comment} ",
		"DELETE /api/comments/{comment} api.posts.comments.destroy",
		"GET /api/tags api.tags.index",
	}
	if diff := cmp.Diff(want, lines); diff != "" {
		t.Fatalf("routes mismatch (-want +got):\n%s", diff)
	}

	if rec := serve(t, r, "PATCH", "/posts/3"); rec.Body.String() != "update" {
		t.Fatalf("PATCH = %q", rec.Body.String())
	}
	if rec := serve(t, r, "GET", "/posts/3"); rec.Body.String() != "show 3" {
		t.Fatalf("GET = %q", rec.Body.String())
	}
	if u, _ := r.URL("posts.comments.show", "post", 1, "comment", 2); u != "/posts/1/comments/2" {
		t.Fatalf("nested URL = %q", u)
	}
}

func TestResourceUseAndSingular(t *testing.T) {
	t.Parallel()

	hit := 0
	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { hit++; next.ServeHTTP(w, req) })
	}

	r := newRouter()
	r.Resource("boxes", partialController{}).Use(mw)
	serve(t, r, "GET", "/boxes")
	serve(t, r, "GET", "/boxes/1")
	if hit != 2 {
		t.Fatalf("resource middleware hits = %d", hit)
	}

	for in, want := range map[string]string{"posts": "post", "categories": "category", "boxes": "box", "classes": "class", "glass": "glass", "data": "data", "dishes": "dish"} {
		if got := singular(in); got != want {
			t.Errorf("singular(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHandleAndAdapters(t *testing.T) {
	t.Parallel()

	r := newRouter()
	r.Handle("GET /metrics", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "m") }))
	r.Handle("/static/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "s") }))
	r.Get("/wrapped", Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "w") })))

	for path, want := range map[string]string{"/metrics": "m", "/static/x.css": "s", "/wrapped": "w"} {
		if got := serve(t, r, "GET", path).Body.String(); got != want {
			t.Errorf("GET %s = %q, want %q", path, got, want)
		}
	}

	h := Adapt(func(c *web.Ctx) error { return c.JSON(200, web.Map{"ok": true}) }, nil)
	rec := serve(t, h, "GET", "/")
	var body map[string]bool
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || !body["ok"] {
		t.Fatalf("Adapt() = %q", rec.Body.String())
	}
}

func TestRoutesListsMiddlewareNames(t *testing.T) {
	t.Parallel()

	r := newRouter()
	r.Get("/x", text("")).Use(sampleMiddleware)

	routes := r.Routes()
	if len(routes) != 1 || len(routes[0].Middleware) != 1 ||
		!strings.Contains(routes[0].Middleware[0], "sampleMiddleware") {
		t.Fatalf("Routes() = %+v", routes)
	}
	r.Any("/y", text(""))
	if r.Routes()[1].Method != "ANY" {
		t.Fatal("Any route method label")
	}
}

func TestGroupRootIsExact(t *testing.T) {
	t.Parallel()

	r := newRouter()
	r.Group("", func(g *Group) { g.Get("", text("home")) })
	r.Group("/admin", func(g *Group) {
		g.Get("/", text("dashboard")).Name("admin")
		g.Get("/users", text("users"))
		g.Group("/reports", func(g *Group) { g.Get("", text("reports")) })
	})

	tests := []struct {
		path, want string
		status     int
	}{
		{"/", "home", 200},
		{"/missing", "", 404},
		{"/admin/", "dashboard", 200},
		{"/admin", "", 307}, // ServeMux redirects to "/admin/"
		{"/admin/users", "users", 200},
		{"/admin/anything", "", 404},
		{"/admin/reports", "reports", 200},
		{"/admin/reports/x", "", 404},
	}

	for _, tt := range tests {
		rec := serve(t, r, http.MethodGet, tt.path, "Accept", "application/json")
		if rec.Code != tt.status || (tt.want != "" && rec.Body.String() != tt.want) {
			t.Errorf("GET %s = %d %q, want %d %q", tt.path, rec.Code, rec.Body.String(), tt.status, tt.want)
		}
	}

	if u, err := r.URL("admin"); err != nil || u != "/admin/" {
		t.Errorf(`URL("admin") = %q, %v, want "/admin/"`, u, err)
	}
}

func sampleMiddleware(next http.Handler) http.Handler { return next }

func BenchmarkStaticRoute(b *testing.B) {
	r := newRouter()
	for i := range 50 {
		r.Get(fmt.Sprintf("/static/%d", i), text("x"))
	}
	r.Get("/hello", func(c *web.Ctx) error { return c.NoContent() })

	h, err := r.Handler()
	if err != nil {
		b.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/hello", http.NoBody)
	w := httptest.NewRecorder()

	b.ReportAllocs()
	for b.Loop() {
		h.ServeHTTP(w, req)
	}
}

func BenchmarkParamRoute(b *testing.B) {
	r := newRouter()
	r.Get("/posts/{id}/comments/{c}", func(c *web.Ctx) error { _ = c.Param("id"); return c.NoContent() })

	h, _ := r.Handler()
	req := httptest.NewRequest(http.MethodGet, "/posts/1/comments/2", http.NoBody)
	w := httptest.NewRecorder()

	b.ReportAllocs()
	for b.Loop() {
		h.ServeHTTP(w, req)
	}
}

func FuzzRoutePattern(f *testing.F) {
	for _, s := range []string{"/a", "/a/{id}", "/{x...}", "/a/{$}", "/{", "//x"} {
		f.Add(s)
	}
	f.Fuzz(func(_ *testing.T, pattern string) {
		r := newRouter()
		r.Get("/"+strings.TrimPrefix(pattern, "/"), text("x"))
		_, _ = r.Handler() // must not panic
	})
}

func TestCompiled(t *testing.T) {
	t.Parallel()

	r := New(nil)
	if r.Compiled() {
		t.Fatal("Compiled() = true before Handler, want false")
	}

	if _, err := r.Handler(); err != nil {
		t.Fatal(err)
	}

	if !r.Compiled() {
		t.Fatal("Compiled() = false after Handler, want true")
	}
}

func TestCompiledDuringBuild(t *testing.T) {
	t.Parallel()

	r := New(nil)

	var during bool

	r.Get("/x", func(*web.Ctx) error { return nil }).Use(func(h http.Handler) http.Handler {
		during = r.Compiled()
		return h
	})

	if _, err := r.Handler(); err != nil {
		t.Fatal(err)
	}

	if !during {
		t.Fatal("Compiled() = false while building, want true")
	}
}
