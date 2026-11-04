package admin_test

import (
	"net/url"
	"testing"

	"github.com/AlexGalhardo/vukem/flags"
)

// The list and detail routes answer JSON to a client that asks for it, behind the same gate and with the same masking.
func TestAdminListAndDetailAsJSON(t *testing.T) {
	t.Parallel()

	app := newApp(t, options{})
	boss := as(t, app, "admin")

	boss.PostForm("/admin/accounts", url.Values{
		"email": {"a@example.com"}, "password": {"hunter2-hash-xyz"}, "api_token": {"tok_secret_123"},
	}).AssertRedirect("/admin/accounts/1")

	list := boss.GetJSON("/admin/accounts").AssertOK().AssertHeader("Vary", "Accept")
	list.AssertJSONPath("resource.plural", "Accounts").AssertJSONPath("total", 1).AssertJSONPath("rows.0.id", "1")
	list.AssertDontSee("hunter2").AssertDontSee("tok_secret")

	detail := boss.GetJSON("/admin/accounts/1").AssertOK()
	detail.AssertJSONPath("id", "1").AssertJSONPath("resource.singular", "Account").
		AssertDontSee("hunter2").AssertDontSee("tok_secret")

	// A browser (HTML) request still gets the page.
	boss.Get("/admin/accounts").AssertOK().AssertSee("<html")

	// The gate is the same: a user without the panel ability gets no JSON, and a guest gets a 401 problem+json.
	as(t, app, "editor").GetJSON("/admin/accounts").AssertForbidden()
	app.Client(t).GetJSON("/admin/accounts").AssertStatus(401)
}

func TestAdminDashboardAsJSON(t *testing.T) {
	t.Parallel()

	app := newApp(t, options{})
	boss := as(t, app, "admin")

	boss.PostForm("/admin/accounts", url.Values{"email": {"a@example.com"}, "password": {"hunter2-hash-xyz"}, "api_token": {"tok_secret_123"}}).
		AssertRedirect("/admin/accounts/1")

	d := boss.GetJSON("/admin").AssertOK().AssertHeader("Vary", "Accept")
	d.AssertJSONPath("panel.home", "/admin").AssertJSONPath("panel.nav.1.url", "/admin/accounts").
		AssertJSONPath("cards.1.count", 1).AssertDontSee("htmx.min.js")

	app.Client(t).GetJSON("/admin").AssertStatus(401)
}

func TestAdminFlagsAndAuditAsJSON(t *testing.T) {
	t.Parallel()

	app := newApp(t, options{flags: []flags.AnyFlag{betaFlag, lockedFlag}})
	boss := as(t, app, "admin")

	boss.PostForm("/admin/_flags/beta", url.Values{"value": {"true"}}).AssertRedirect("/admin/_flags")

	f := boss.GetJSON("/admin/_flags").AssertOK().AssertHeader("Vary", "Accept")
	f.AssertJSONPath("flags.0.name", "beta").AssertJSONPath("flags.0.value", "true").
		AssertJSONPath("flags.0.stored", true).AssertJSONPath("flags.1.locked", true)

	a := boss.GetJSON("/admin/_audit").AssertOK()
	a.AssertJSONPath("entries.0.resource", "_flags").AssertJSONPath("entries.0.recordId", "beta").
		AssertJSONPath("entries.0.changes.0.column", "value").AssertJSONPath("pager.page", 1)

	// The same gates as the HTML pages: no flags ability, no JSON.
	editor := as(t, app, "editor")
	editor.GetJSON("/admin/_flags").AssertForbidden()
	editor.GetJSON("/admin/_audit").AssertForbidden()
}
