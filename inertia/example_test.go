package inertia_test

import (
	"os"

	"github.com/AlexGalhardo/vukem/inertia"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/vite"
	"github.com/AlexGalhardo/vukem/web"
)

func Example() {
	v, _ := vite.New(vite.Config{FS: os.DirFS("public")})
	in, _ := inertia.New(inertia.Config{Version: v.Version})

	r := router.New(nil)
	r.Use(in.Middleware)
	r.Get("/events/{id}", func(c *web.Ctx) error {
		return inertia.Render(c, "Events/Show", inertia.Props{"id": c.Param("id")})
	})
	// Output:
}
