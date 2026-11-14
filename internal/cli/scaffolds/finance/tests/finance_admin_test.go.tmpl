package tests

import (
	"net/url"
	"strconv"
	"testing"
)

func TestAdminPanelListsUsersWorkspacesAndSubscriptions(t *testing.T) {
	t.Parallel()

	app := newApp(t)
	ana := signUp(t, app, "Ana", "ana@example.com")
	makePro(t, app, ana)

	boss := newVerifiedUser(t, app, "Boss", "boss@example.com")
	c := app.Client(t).WithHeader("Sec-Fetch-Site", "same-origin").ActingAs("web", boss)

	users := c.GetJSON("/admin-api/users").AssertOK()
	users.AssertJSONPath("total", 2).AssertSee("ana@example.com").AssertDontSee("password")

	c.GetJSON("/admin-api/workspaces").AssertOK().AssertJSONPath("total", 1).AssertSee("Ana household")
	c.GetJSON("/admin-api/workspace-subscriptions").AssertOK().AssertJSONPath("total", 1)

	// The pages are the panel's React shells, not a 404.
	c.Get("/admin/users").AssertOK()

	// Support reads these records; nothing in the panel changes them.
	id := strconv.FormatInt(ana.User.ID, 10)
	c.GetJSON("/admin-api/users/" + id).AssertOK()
	c.PostForm("/admin-api/users/"+id+"/delete", url.Values{}).AssertForbidden()
}
