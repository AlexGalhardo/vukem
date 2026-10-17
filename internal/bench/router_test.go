package bench

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/labstack/echo/v4"

	"github.com/AlexGalhardo/vukem/web"
)

func BenchmarkRouterStatic(b *testing.B) {
	run(b, routerImpls(b, func(add adder) { add("/hello", "") }), "/hello")
}

func BenchmarkRouterParam(b *testing.B) {
	run(b, routerImpls(b, func(add adder) { add("/users/{id}", "id") }), "/users/42")
}

// BenchmarkRouterTable20 registers 20 parameterised routes and looks up the
// last one registered.
func BenchmarkRouterTable20(b *testing.B) {
	impls := routerImpls(b, func(add adder) {
		for i := range 20 {
			add(fmt.Sprintf("/api/v1/resource%d/{id}", i), "id")
		}
	})
	run(b, impls, "/api/v1/resource19/42")
}

func BenchmarkMiddleware5(b *testing.B) {
	const n = 5

	mux := http.NewServeMux()
	mux.HandleFunc("GET /hello", func(w http.ResponseWriter, _ *http.Request) { writeText(w, "ok") })

	var std http.Handler = mux
	for range n {
		std = noop(std)
	}

	vr := vukemRouter()
	for range n {
		vr.Use(noop)
	}

	vr.Get("/hello", func(c *web.Ctx) error { return c.String(http.StatusOK, "ok") })

	cr := chi.NewRouter()
	for range n {
		cr.Use(noop)
	}

	cr.Get("/hello", func(w http.ResponseWriter, _ *http.Request) { writeText(w, "ok") })

	e := echo.New()
	for range n {
		e.Use(func(next echo.HandlerFunc) echo.HandlerFunc { return func(c echo.Context) error { return next(c) } })
	}

	e.GET("/hello", func(c echo.Context) error { return c.String(http.StatusOK, "ok") })

	run(b, []impl{
		{"nethttp", std},
		{"vukem", mustHandler(b, vr)},
		{"chi", cr},
		{"echo", e},
	}, "/hello")
}

// adder registers a GET route in ServeMux syntax; param names the path
// parameter the handler echoes ("" for a static body).
type adder func(path, param string)

func routerImpls(b *testing.B, routes func(add adder)) []impl {
	b.Helper()

	mux := http.NewServeMux()
	routes(func(path, param string) {
		mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
			writeText(w, body(param, r.PathValue))
		})
	})

	vr := vukemRouter()
	routes(func(path, param string) {
		vr.Get(path, func(c *web.Ctx) error { return c.String(http.StatusOK, body(param, c.Param)) })
	})

	cr := chi.NewRouter()
	routes(func(path, param string) {
		cr.Get(path, func(w http.ResponseWriter, r *http.Request) {
			writeText(w, body(param, func(k string) string { return chi.URLParam(r, k) }))
		})
	})

	e := echo.New()
	routes(func(path, param string) {
		e.GET(echoPath(path), func(c echo.Context) error { return c.String(http.StatusOK, body(param, c.Param)) })
	})

	return []impl{
		{"nethttp", mux},
		{"vukem", mustHandler(b, vr)},
		{"chi", cr},
		{"echo", e},
	}
}

func body(param string, get func(string) string) string {
	if param == "" {
		return "ok"
	}

	return get(param)
}

// echoPath turns "/users/{id}" into echo's "/users/:id".
func echoPath(p string) string { return strings.NewReplacer("{", ":", "}", "").Replace(p) }
