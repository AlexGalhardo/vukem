package router_test

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"

	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

func Example() {
	r := router.New(&web.Services{Log: slog.New(slog.DiscardHandler)})
	r.Get("/posts/{id}", func(c *web.Ctx) error {
		return c.String(http.StatusOK, "post "+c.Param("id"))
	}).Name("posts.show")

	h, err := r.Handler()
	if err != nil {
		fmt.Println(err)
		return
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/posts/42", http.NoBody))
	fmt.Println(rec.Code, rec.Body.String())

	link, err := r.URL("posts.show", "id", 7)
	fmt.Println(link, err)
	// Output:
	// 200 post 42
	// /posts/7 <nil>
}

func ExampleRouter_Group() {
	r := router.New(&web.Services{Log: slog.New(slog.DiscardHandler)})
	r.Group("/admin", func(g *router.Group) {
		g.Get("/dashboard", func(c *web.Ctx) error {
			return c.String(http.StatusOK, "dashboard")
		}).Name("dashboard")
	}).Name("admin.")

	for _, route := range r.Routes() {
		fmt.Println(route.Method, route.Path, route.Name)
	}
	// Output:
	// GET /admin/dashboard admin.dashboard
}
