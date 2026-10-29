package admin_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/admin"
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/vukemtest"
)

func withActions() []admin.AnyResource {
	return []admin.AnyResource{
		admin.NewResource[Post](postSchema).Actions(
			admin.Action[Post]{
				Name: "approve", Bulk: true,
				Run: func(_ context.Context, _ *database.Tx, p *Post, _ string) error {
					p.Status = "live"
					return nil
				},
			},
			admin.Action[Post]{
				Name: "reject", Label: "Reject with reason", Reason: true,
				Run: func(_ context.Context, _ *database.Tx, p *Post, _ string) error {
					p.Status = "rejected"
					return nil
				},
			},
		),
	}
}

func TestAdminRowAction(t *testing.T) {
	t.Parallel()

	app := newApp(t, options{resource: withActions})
	createPost(t, app, Post{Title: "First", Status: "draft"})

	c := as(t, app, "admin")
	c.Get("/admin/posts/1").AssertOK().AssertSee(`href="/admin/posts/1/actions/approve"`).
		AssertSee("Reject with reason")
	c.Get("/admin/posts/1/actions/approve").AssertOK().AssertSee("Approve").AssertSee(`name="_token"`)

	c.PostForm("/admin/posts/1/actions/approve", url.Values{}).AssertRedirect("/admin/posts/1")
	vukemtest.AssertDatabaseHas(t, app, "posts", map[string]any{"id": 1, "status": "live"})

	logs := auditLogs(t, app)
	if len(logs) != 1 || logs[0].Action != "approve" || logs[0].Changes.V["status"].After != "live" {
		t.Fatalf("audit = %+v, want one approve entry draft -> live", logs)
	}

	// An unknown action is a 404.
	c.Get("/admin/posts/1/actions/nope").AssertNotFound()
}

func TestAdminActionReason(t *testing.T) {
	t.Parallel()

	app := newApp(t, options{resource: withActions})
	createPost(t, app, Post{Title: "First", Status: "draft"})

	c := as(t, app, "admin")
	c.Get("/admin/posts/1/actions/reject").AssertOK().AssertSee(`name="reason"`)

	for _, reason := range []string{"", "   ", strings.Repeat("x", 1001)} {
		c.PostForm("/admin/posts/1/actions/reject", url.Values{"reason": {reason}}).
			AssertUnprocessable().AssertSee(`name="reason"`)
	}

	vukemtest.AssertDatabaseHas(t, app, "posts", map[string]any{"id": 1, "status": "draft"})

	c.PostForm("/admin/posts/1/actions/reject", url.Values{"reason": {" Spam link "}}).
		AssertRedirect("/admin/posts/1")
	vukemtest.AssertDatabaseHas(t, app, "posts", map[string]any{"id": 1, "status": "rejected"})

	logs := auditLogs(t, app)
	if len(logs) != 1 || logs[0].Changes.V[admin.ReasonKey].After != "Spam link" {
		t.Fatalf("audit = %+v, want the reason recorded", logs)
	}

	c.Get("/admin/_audit").AssertOK().AssertSee("Spam link")
}

func TestAdminBulkAction(t *testing.T) {
	t.Parallel()

	app := newApp(t, options{resource: withActions})
	createPost(t, app, Post{Title: "First", Status: "draft"})
	createPost(t, app, Post{Title: "Second", Status: "draft"})
	createPost(t, app, Post{Title: "Third", Status: "draft"})

	c := as(t, app, "admin")
	c.Get("/admin/posts").AssertOK().AssertSee(`name="ids"`).AssertSee(`formaction="/admin/posts/_bulk/approve"`)
	c.Get("/admin/posts/_bulk/approve?ids=1&ids=2").AssertOK().AssertSee("2 posts").
		AssertSee(`<input type="hidden" name="ids" value="1">`)

	c.PostForm("/admin/posts/_bulk/approve", url.Values{"ids": {"1", "2", "2"}}).AssertRedirect("/admin/posts")
	vukemtest.AssertDatabaseHas(t, app, "posts", map[string]any{"id": 1, "status": "live"})
	vukemtest.AssertDatabaseHas(t, app, "posts", map[string]any{"id": 2, "status": "live"})
	vukemtest.AssertDatabaseHas(t, app, "posts", map[string]any{"id": 3, "status": "draft"})

	if n := len(auditLogs(t, app)); n != 2 {
		t.Fatalf("audit entries = %d, want 2", n)
	}

	// Nothing selected, a bad id or a missing record: nothing runs.
	c.PostForm("/admin/posts/_bulk/approve", url.Values{}).AssertUnprocessable()
	c.PostForm("/admin/posts/_bulk/approve", url.Values{"ids": {"3", "x"}}).AssertNotFound()
	c.PostForm("/admin/posts/_bulk/approve", url.Values{"ids": {"3", "99"}}).AssertNotFound()
	vukemtest.AssertDatabaseHas(t, app, "posts", map[string]any{"id": 3, "status": "draft"})

	// Reject is not a bulk action.
	c.PostForm("/admin/posts/_bulk/reject", url.Values{"ids": {"3"}}).AssertNotFound()
}

func TestSecurity_A01_AdminActionsRequirePolicy(t *testing.T) {
	t.Parallel()

	app := newApp(t, options{resource: withActions})
	createPost(t, app, Post{Title: "First", Status: "draft"})
	createPost(t, app, Post{Title: "Locked", Status: "draft"})

	// The editor holds the panel ability but no Approve policy.
	editor := as(t, app, "editor")
	editor.Get("/admin/posts/1/actions/approve").AssertForbidden()
	editor.PostForm("/admin/posts/1/actions/approve", url.Values{}).AssertForbidden()
	editor.PostForm("/admin/posts/_bulk/approve", url.Values{"ids": {"1"}}).AssertForbidden()

	// One denied record denies the whole batch.
	c := as(t, app, "admin")
	c.Get("/admin/posts/2").AssertOK().AssertDontSee("/admin/posts/2/actions/approve")
	c.PostForm("/admin/posts/_bulk/approve", url.Values{"ids": {"1", "2"}}).AssertForbidden()
	vukemtest.AssertDatabaseHas(t, app, "posts", map[string]any{"id": 1, "status": "draft"})

	// Cross-site posts are refused by the CSRF check.
	app.Client(t).
		WithHeader("Sec-Fetch-Site", "cross-site").
		ActingAs("web", createUser(t, app, "a2@example.com", "admin")).
		PostForm("/admin/posts/1/actions/approve", url.Values{}).
		AssertForbidden()

	// Too many ids at once.
	ids := make([]string, 101)
	for i := range ids {
		ids[i] = "1"
	}

	c.PostForm("/admin/posts/_bulk/approve", url.Values{"ids": ids}).AssertStatus(http.StatusUnprocessableEntity)
	vukemtest.AssertDatabaseHas(t, app, "posts", map[string]any{"id": 1, "status": "draft"})
	vukemtest.AssertDatabaseCount(t, app, admin.AuditTable, 0)
}

func TestActionsConfigErrors(t *testing.T) {
	t.Parallel()

	run := func(context.Context, *database.Tx, *Post, string) error { return nil }

	for name, a := range map[string]admin.Action[Post]{
		"reserved":  {Name: "delete", Run: run},
		"edit":      {Name: "edit", Run: run},
		"bad chars": {Name: "do/it", Run: run},
		"too long":  {Name: "abcdefghijklmnopq", Run: run},
		"no run":    {Name: "approve"},
	} {
		if err := newPanel(t).Register(admin.NewResource[Post](postSchema).Actions(a)); err == nil {
			t.Errorf("Register(%s) = nil error, want an error", name)
		}
	}

	dup := admin.NewResource[Post](postSchema).Actions(
		admin.Action[Post]{Name: "approve", Run: run}, admin.Action[Post]{Name: "approve", Run: run},
	)
	if err := newPanel(t).Register(dup); err == nil {
		t.Error("Register(duplicate action) = nil error, want an error")
	}
}
