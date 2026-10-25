package routes

import (
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/examples/api/app/accounts"
	"github.com/AlexGalhardo/vukem/examples/api/app/controllers/apiv1"
	"github.com/AlexGalhardo/vukem/middleware"
	"github.com/AlexGalhardo/vukem/ratelimit"
	"github.com/AlexGalhardo/vukem/router"
)

// API registers /api/v1: token issuance and revocation, /me, CORS from
// CORS_ALLOWED_ORIGINS and rate limiting (vukem install:api).
func API(r *router.Router, app *vukem.App) error {
	acc, err := accounts.New(app)
	if err != nil {
		return err
	}

	var origins []string

	for o := range strings.SplitSeq(app.Source.Get("CORS_ALLOWED_ORIGINS"), ",") {
		if o = strings.TrimSpace(o); o != "" {
			origins = append(origins, o)
		}
	}

	cors, err := middleware.CORS(middleware.CORSConfig{AllowedOrigins: origins, MaxAge: time.Hour})
	if err != nil {
		return err
	}

	r.UseServer(cors) // preflight OPTIONS requests have no route

	h := apiv1.NewTokens(acc)
	perIP := ratelimit.Middleware(ratelimit.NewMemory(120, time.Minute), ratelimit.ByIP)
	perToken := ratelimit.Middleware(ratelimit.NewMemory(60, time.Minute), ratelimit.ByToken)
	issue := ratelimit.Middleware(ratelimit.NewMemory(5, time.Minute), ratelimit.ByIP)

	r.Group("/api/v1", func(g *router.Group) {
		g.Use(middleware.JSONOnly(), perIP)
		g.Post("/tokens", h.Issue).Name("tokens.store").Use(issue)

		g.Group("", func(p *router.Group) {
			// The per-token limiter runs after the guard: before it, a client
			// rotating made-up tokens would get a fresh bucket per request.
			p.Use(auth.Required(acc.API), perToken)
			p.Get("/me", h.Me).Name("me").Use(auth.Ability(acc.API, apiv1.AbilityRead))
			p.Delete("/tokens/current", h.Revoke).Name("tokens.destroy")
		})
	}).Name("api.v1.")

	return nil
}
