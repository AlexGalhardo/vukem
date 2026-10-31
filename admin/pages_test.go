package admin_test

import (
	"errors"
	"html/template"
	"net/http"
	"testing"

	"github.com/AlexGalhardo/vukem/admin"
	"github.com/AlexGalhardo/vukem/vukemtest"
	"github.com/AlexGalhardo/vukem/web"
)

func pagesApp(t *testing.T) *vukemtest.App {
	t.Helper()

	return newApp(t, options{
		harden: func(c *admin.Config) { c.Title = "Acme admin" },
		pages: func(p *admin.Panel) error {
			if err := p.AddPage("ingestion", "Ingestion", func(*web.Ctx) (template.HTML, error) {
				return "<p>queue is empty</p>", nil
			}); err != nil {
				return err
			}

			return p.AddPage("broken", "Broken", func(*web.Ctx) (template.HTML, error) {
				return "", errors.New("boom")
			})
		},
	})
}

func TestAdminCustomPage(t *testing.T) {
	t.Parallel()

	c := as(t, pagesApp(t), "admin")

	page := c.Get("/admin/ingestion").AssertOK()
	for _, want := range []string{
		"queue is empty", "<h1>Ingestion</h1>", "Acme admin", `href="/admin/posts"`, `href="/admin/ingestion" aria-current="page"`,
	} {
		page.AssertSee(want)
	}

	c.Get("/admin/posts").AssertOK().AssertSee(`href="/admin/ingestion"`)
	c.Get("/admin").AssertOK().AssertSee(`href="/admin/ingestion"`)
	c.Get("/admin/broken").AssertStatus(http.StatusInternalServerError)
}

func TestSecurity_A01_AdminCustomPageIsGuarded(t *testing.T) {
	t.Parallel()

	app := pagesApp(t)

	app.Client(t).Get("/admin/ingestion").AssertRedirect("/login")
	as(t, app, "guest").Get("/admin/ingestion").AssertStatus(http.StatusForbidden)
}

func TestAdminAddPageErrors(t *testing.T) {
	t.Parallel()

	panel := newPanel(t)

	h := func(*web.Ctx) (template.HTML, error) { return "", nil }

	if err := panel.AddPage("ok", "Ok", h); err != nil {
		t.Fatal(err)
	}

	for name, call := range map[string]error{
		"duplicate":   panel.AddPage("ok", "Again", h),
		"reserved":    panel.AddPage("_audit", "Audit", h),
		"bad slug":    panel.AddPage("A b", "Bad", h),
		"empty label": panel.AddPage("x", "", h),
		"nil handler": panel.AddPage("y", "Y", nil),
	} {
		if call == nil {
			t.Errorf("%s: AddPage returned nil", name)
		}
	}
}
