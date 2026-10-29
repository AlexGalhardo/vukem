package challenge_test

import (
	"net/http"

	"github.com/AlexGalhardo/vukem/challenge"
	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

func ExampleMiddleware() {
	verify := challenge.Turnstile(config.NewSecret("turnstile-secret"))
	verify.Hostname = "example.com"

	r := router.New(&web.Services{})
	r.Group("", func(g *router.Group) {
		g.Use(challenge.Middleware(verify, nil))
		g.Post("/register", func(c *web.Ctx) error { return c.String(http.StatusCreated, "welcome") })
	})
}
