package admin_test

import (
	"io/fs"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/admin"
	"github.com/AlexGalhardo/vukem/vukemtest"
)

func TestAdminCRUD(t *testing.T) {
	t.Parallel()

	app := newApp(t, options{})
	c := as(t, app, "admin")

	c.Get("/admin").AssertOK().AssertSee("Dashboard").AssertSee("Posts")
	c.Get("/admin/posts/create").AssertOK().AssertSee(`name="title"`).AssertSee(`name="_token"`).
		AssertSee(`<option value="draft"`)

	c.PostForm("/admin/posts", url.Values{
		"title": {"Hello admin"}, "body": {"First."}, "status": {"live"},
		"published": {"false", "true"},
	}).AssertRedirect("/admin/posts/1").AssertSessionHas(admin.FlashKey)

	vukemtest.AssertDatabaseHas(
		t,
		app,
		"posts",
		map[string]any{"title": "Hello admin", "status": "live", "published": true},
	)

	c.Get("/admin/posts/1").AssertOK().AssertSee("Hello admin").AssertSee("First.").AssertSee("Post created.")
	c.Get("/admin/posts/1/edit").AssertOK().AssertSee(`value="Hello admin"`).AssertSee("checked")

	c.PostForm("/admin/posts/1", url.Values{
		"title": {"Hello again"}, "body": {"First."}, "status": {"live"}, "published": {"false"},
	}).AssertRedirect("/admin/posts/1")

	vukemtest.AssertDatabaseHas(t, app, "posts", map[string]any{"id": 1, "title": "Hello again", "published": false})

	c.Get("/admin/posts/1/delete").AssertOK().AssertSee("cannot be undone").AssertSee(`action="/admin/posts/1/delete"`)
	vukemtest.AssertDatabaseCount(t, app, "posts", 1) // the confirmation page deletes nothing

	c.PostForm("/admin/posts/1/delete", url.Values{}).AssertRedirect("/admin/posts")
	vukemtest.AssertDatabaseCount(t, app, "posts", 0)
	c.Get("/admin/posts/1").AssertNotFound()

	logs := auditLogs(t, app)

	got := make([]string, 0, len(logs))
	for _, l := range logs {
		got = append(got, l.Action+" "+l.Resource+"/"+l.RecordID+" by "+l.ActorID)
	}

	want := []string{"create posts/1 by 1", "update posts/1 by 1", "delete posts/1 by 1"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("audit log (-want +got):\n%s", diff)
	}

	wantUpdate := map[string]admin.Change{
		"title":     {Before: "Hello admin", After: "Hello again"},
		"published": {Before: true, After: false},
	}
	if diff := cmp.Diff(wantUpdate, logs[1].Changes.V); diff != "" {
		t.Errorf("update changes (-want +got):\n%s", diff)
	}

	if ch := logs[2].Changes.V["title"]; ch.Before != "Hello again" || ch.After != nil {
		t.Errorf("delete change of title = %+v, want the old value only", ch)
	}

	c.Get("/admin/_audit").AssertOK().AssertSee("Hello admin").AssertSee("Hello again")
}

func TestAdminListSearchSortFilterPaginate(t *testing.T) {
	t.Parallel()

	app := newApp(t, options{})
	c := as(t, app, "admin")

	createPost(t, app, Post{Title: "Alpha", Body: "go", Status: "live", Published: true})
	createPost(t, app, Post{Title: "Bravo", Body: "rust", Status: "draft"})
	createPost(t, app, Post{Title: "Charlie", Body: "go go", Status: "live"})

	// Default order: newest id first, two per page.
	c.Get("/admin/posts").AssertOK().AssertSee("Charlie").AssertSee("Bravo").AssertDontSee("Alpha").
		AssertSee("Page 1 of 2").AssertSee("3 records")
	c.Get("/admin/posts?page=2").AssertOK().AssertSee("Alpha").AssertDontSee("Charlie")

	c.Get("/admin/posts?sort=title&dir=asc").AssertOK().AssertSee("Alpha").AssertSee("Bravo").AssertDontSee("Charlie")
	c.Get("/admin/posts?q=char").AssertOK().AssertSee("Charlie").AssertDontSee("Bravo").AssertSee("1 record")
	c.Get("/admin/posts?q=rust").AssertOK().AssertSee("Bravo").AssertDontSee("Charlie") // body is searchable

	c.Get("/admin/posts?" + url.Values{"filter[status]": {"draft"}}.Encode()).
		AssertOK().AssertSee("Bravo").AssertDontSee("Charlie")
	c.Get("/admin/posts?" + url.Values{"filter[published]": {"true"}}.Encode()).
		AssertOK().AssertSee("Alpha").AssertDontSee("Bravo")

	// Computed column.
	c.Get("/admin/posts?q=charlie").AssertSee("<td>5</td>")

	// htmx gets only the list fragment.
	frag := c.HTMX().Get("/admin/posts?q=alpha").AssertOK().AssertFragment()
	frag.AssertSee(`id="admin-list"`).AssertSee("Alpha").AssertDontSee("<html")
}

func TestAdminValidationErrorsKeepOldInput(t *testing.T) {
	t.Parallel()

	app := newApp(t, options{})
	c := as(t, app, "admin")

	c.PostForm("/admin/posts", url.Values{"title": {"x"}, "body": {"kept body"}, "status": {"nope"}}).
		AssertUnprocessable().
		AssertSee(`aria-invalid="true"`).
		AssertSee("kept body").
		AssertSee(`value="x"`)

	vukemtest.AssertDatabaseCount(t, app, "posts", 0)
	vukemtest.AssertDatabaseCount(t, app, admin.AuditTable, 0)
}

func TestAdminViewsOverride(t *testing.T) {
	t.Parallel()

	views := fstest.MapFS{
		"layouts/admin.html": {Data: []byte(`<!doctype html><title>{{.Title}}</title>` +
			`<p>custom layout</p>{{block "content" .}}{{end}}`)},
	}

	app := newApp(t, options{views: views})
	as(t, app, "admin").Get("/admin").AssertOK().AssertSee("custom layout").AssertSee("Dashboard")
}

func TestAdminAssetsEmbedded(t *testing.T) {
	t.Parallel()

	app := newApp(t, options{})
	c := app.Client(t)

	c.Get("/admin/_assets/htmx.min.js").AssertOK().AssertSee("htmx")
	c.Get("/admin/_assets/admin.css").AssertOK().AssertSee(":root")
	as(t, app, "admin").Get("/admin").AssertSee(`src="/admin/_assets/htmx.min.js"`).AssertDontSee("cdn")
}

func TestAdminConfigErrors(t *testing.T) {
	t.Parallel()

	if _, err := admin.New(admin.Config{}); err == nil {
		t.Error("New without DB = nil error")
	}

	cases := map[string]admin.AnyResource{
		"unknown column":     admin.NewResource[Post](postSchema).List("nope"),
		"sensitive listed":   admin.NewResource[Account](accountSchema).List("email", "password"),
		"sensitive searched": admin.NewResource[Account](accountSchema).Search("api_token"),
		"sensitive filtered": admin.NewResource[Account](accountSchema).Filter("password"),
		"protected key":      admin.NewResource[Post](postWithIDSchema),
		"no column":          admin.NewResource[Note](postSchema),
		"bad slug":           admin.NewResource[Post](postSchema).Slug("../x"),
		"bad input":          admin.NewResource[Post](postSchema).Input("title", "color"),
		"bad per page":       admin.NewResource[Post](postSchema).PerPage(0),
		"not a string":       admin.NewResource[Post](postSchema).Search("published"),
	}

	for name, res := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if err := newPanel(t).Register(res); err == nil {
				t.Errorf("Register(%s) = nil error", name)
			}
		})
	}

	p := newPanel(t)
	if err := p.Register(admin.NewResource[Post](postSchema), admin.NewResource[Post](postSchema)); err == nil {
		t.Error("Register(duplicate slug) = nil error")
	}
}

func TestTemplatesHaveNoInlineCode(t *testing.T) {
	t.Parallel()

	root := admin.Templates()

	err := fs.WalkDir(root, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		b, err := fs.ReadFile(root, p)
		if err != nil {
			return err
		}

		src := strings.ToLower(string(b))
		for _, bad := range []string{"<style", " style=", "<script>", "javascript:", " onclick", " onsubmit", " onchange", "hx-on"} {
			if strings.Contains(src, bad) {
				t.Errorf("%s contains %q (blocked by a strict CSP)", p, bad)
			}
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAdminSearchEscapesWildcards(t *testing.T) {
	t.Parallel()

	app := newApp(t, options{})
	c := as(t, app, "admin")

	for _, title := range []string{"Done 100% here", "Done 1000 here", "snake_case", "snakeXcase", "Wow! yes", "Wowza yes"} {
		createPost(t, app, Post{Title: title, Status: "live"})
	}

	tests := []struct {
		q, see, dontSee string
	}{
		{q: "100%", see: "Done 100% here", dontSee: "Done 1000 here"},
		{q: "e_c", see: "snake_case", dontSee: "snakeXcase"},
		{q: "w!", see: "Wow! yes", dontSee: "Wowza yes"},
	}

	for _, tt := range tests {
		c.Get("/admin/posts?" + url.Values{"q": {tt.q}}.Encode()).
			AssertOK().AssertSee(tt.see).AssertDontSee(tt.dontSee).AssertSee("1 record")
	}
}
