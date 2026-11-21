package admin_test

import (
	"html/template"
	"net/http"
	"testing"

	"github.com/AlexGalhardo/vukem/admin"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

// VF-102: a slug with no resource or page is a 404, never an empty shell.
func TestUnknownSlugIsNotFound(t *testing.T) {
	t.Parallel()

	app := newApp(t, options{})
	c := as(t, app, "admin")

	for _, path := range []string{
		"/admin/revenue", "/admin/revenue/1", "/admin/revenue/1/edit", "/admin/revenue/create",
	} {
		c.Get(path).AssertNotFound()
	}

	// Registered slugs still answer.
	c.Get("/admin/posts").AssertOK()
}

func TestUnknownSlugKeepsCustomRoutes(t *testing.T) {
	t.Parallel()

	app := newApp(t, options{
		pages: func(p *admin.Panel) error {
			return p.AddPage("stats", "Stats", func(*web.Ctx) (template.HTML, error) { return "stats body", nil })
		},
		beside: func(r *router.Router, p *admin.Panel) {
			r.Get("/admin/revenue", func(c *web.Ctx) error {
				return c.String(http.StatusOK, "revenue ok")
			}).Use(p.Guard())
		},
	})
	c := as(t, app, "admin")

	c.Get("/admin/revenue").AssertOK().AssertSee("revenue ok")
	c.Get("/admin/stats").AssertOK().AssertSee("stats body")
	c.Get("/admin/other").AssertNotFound()
	c.Get("/admin/revenue/1").AssertNotFound()
	app.Client(t).Get("/admin/revenue").AssertStatus(http.StatusFound)
}
