package admin_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/admin"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/vukemtest"
)

type action struct {
	method, path string
	form         url.Values
}

// actions covers every route of a resource, for record 1.
func actions(slug string) []action {
	form := url.Values{"title": {"Changed"}, "status": {"live"}, "text": {"changed"}, "email": {"x@example.com"}}

	return []action{
		{http.MethodGet, "/admin/" + slug, nil},
		{http.MethodGet, "/admin/" + slug + "/create", nil},
		{http.MethodPost, "/admin/" + slug, form},
		{http.MethodGet, "/admin/" + slug + "/1", nil},
		{http.MethodGet, "/admin/" + slug + "/1/edit", nil},
		{http.MethodPost, "/admin/" + slug + "/1", form},
		{http.MethodGet, "/admin/" + slug + "/1/delete", nil},
		{http.MethodPost, "/admin/" + slug + "/1/delete", url.Values{}},
	}
}

func do(c *vukemtest.Client, a action) *vukemtest.Response {
	if a.method == http.MethodPost {
		return c.PostForm(a.path, a.form)
	}

	return c.Get(a.path)
}

func seed(t *testing.T, app *vukemtest.App) {
	t.Helper()

	db, err := app.DB()
	if err != nil {
		t.Fatal(err)
	}

	createPost(t, app, Post{Title: "Seed", Status: "draft"})

	if err := orm.Create(t.Context(), db, &Note{Text: "seed"}); err != nil {
		t.Fatal(err)
	}
}

//nolint:tparallel // subtests share one seeded app; the checks after them must see every request
func TestSecurity_A01_AdminRequiresAuthAndPolicy(t *testing.T) {
	t.Parallel()

	app := newApp(t, options{})
	seed(t, app)

	all := append(
		[]action{{http.MethodGet, "/admin", nil}, {http.MethodGet, "/admin/_audit", nil}},
		actions("posts")...)

	t.Run("anonymous browser is sent to login", func(t *testing.T) {
		c := app.Client(t).WithHeader("Sec-Fetch-Site", "same-origin")
		c.Get("/").AssertNotFound() // start a session, like a browser that visited the site

		for _, a := range all {
			do(c, a).AssertRedirect("/login")
		}
	})

	t.Run("anonymous API client gets 401", func(t *testing.T) {
		c := app.Client(t).AcceptJSON()
		for _, a := range all {
			do(c, a).AssertUnauthorized()
		}
	})

	t.Run("policy denies every action", func(t *testing.T) {
		// "editor" has the panel ability but the post policy denies it.
		c := as(t, app, "editor")
		c.Get("/admin").AssertOK().AssertDontSee(`href="/admin/posts"`)

		for _, a := range actions("posts") {
			do(c, a).AssertForbidden()
		}
	})

	t.Run("missing policy denies every action", func(t *testing.T) {
		// No policy is registered for notes: fail closed, even for admins.
		c := as(t, app, "admin")
		for _, a := range actions("notes") {
			do(c, a).AssertForbidden()
		}
	})

	vukemtest.AssertDatabaseHas(t, app, "posts", map[string]any{"id": 1, "title": "Seed"})
	vukemtest.AssertDatabaseCount(t, app, "notes", 1)
	vukemtest.AssertDatabaseCount(t, app, admin.AuditTable, 0)
}

func TestSecurity_A01_AdminRequiresAbility(t *testing.T) {
	t.Parallel()

	app := newApp(t, options{})
	seed(t, app)

	// A plain user lacks the "admin" gate ability: every page is 403, even
	// before policies run.
	c := as(t, app, "user")

	for _, a := range append([]action{{http.MethodGet, "/admin", nil}}, actions("posts")...) {
		do(c, a).AssertForbidden()
	}

	// Without any Authorizer the panel denies everything.
	bare := newApp(t, options{noGate: true})
	as(t, bare, "admin").Get("/admin").AssertForbidden()
}

func TestSecurity_A01_AdminCSRF(t *testing.T) {
	t.Parallel()

	app := newApp(t, options{})
	u := createUser(t, app, "admin@example.com", "admin")
	form := url.Values{"title": {"Forged"}, "status": {"live"}}

	app.Client(t).WithHeader("Sec-Fetch-Site", "cross-site").ActingAs("web", u).
		PostForm("/admin/posts", form).AssertForbidden()

	// No Fetch Metadata or Origin (old browser) with a session: the
	// session token is required.
	app.Client(t).ActingAs("web", u).PostForm("/admin/posts", form).AssertForbidden()

	vukemtest.AssertDatabaseCount(t, app, "posts", 0)
}

func TestSecurity_A01_AdminMassAssignment(t *testing.T) {
	t.Parallel()

	app := newApp(t, options{})
	c := as(t, app, "admin")
	posted := "2001-01-01T00:00:00Z"

	c.PostForm("/admin/posts", url.Values{
		"title": {"Mass"}, "status": {"draft"},
		"id": {"999"}, "author_id": {"7"}, "created_at": {posted}, "updated_at": {posted}, "is_admin": {"true"},
	}).AssertRedirect("/admin/posts/1")

	createPost(t, app, Post{Title: "Other", Status: "draft", AuthorID: 3})

	c.PostForm("/admin/posts/2", url.Values{"title": {"Other edited"}, "status": {"draft"}, "author_id": {"9"}, "id": {"5"}}).
		AssertRedirect("/admin/posts/2")

	db, err := app.DB()
	if err != nil {
		t.Fatal(err)
	}

	posts, err := orm.QueryOf[Post](db).OrderBy("id", orm.Asc).Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if len(posts) != 2 {
		t.Fatalf("posts = %d, want 2", len(posts))
	}

	first, second := posts[0], posts[1]
	if first.ID != 1 || first.AuthorID != 0 || first.CreatedAt.Year() == 2001 || first.UpdatedAt.Year() == 2001 {
		t.Errorf("created post = %+v: non-schema fields were written", first)
	}

	if second.ID != 2 || second.AuthorID != 3 || second.Title != "Other edited" {
		t.Errorf("updated post = %+v: want only the title changed", second)
	}
}

func TestSecurity_A09_AdminAuditOmitsSensitive(t *testing.T) {
	t.Parallel()

	app := newApp(t, options{})
	c := as(t, app, "admin")

	c.PostForm("/admin/accounts", url.Values{
		"email": {"a@example.com"}, "password": {"hunter2-hash-xyz"}, "api_token": {"tok_secret_123"},
	}).AssertRedirect("/admin/accounts/1")

	// Secrets are never displayed or echoed back into forms.
	c.Get("/admin/accounts").AssertOK().AssertDontSee("hunter2").AssertDontSee("tok_secret")
	c.Get("/admin/accounts/1").AssertOK().AssertDontSee("hunter2").AssertDontSee("tok_secret")
	c.Get("/admin/accounts/1/edit").AssertOK().AssertDontSee("hunter2").AssertDontSee("tok_secret").
		AssertSee(`type="password"`)

	// A blank secret on edit keeps the stored one.
	c.PostForm("/admin/accounts/1", url.Values{"email": {"b@example.com"}, "password": {""}, "api_token": {"tok_new_456"}}).
		AssertRedirect("/admin/accounts/1")
	vukemtest.AssertDatabaseHas(
		t,
		app,
		"accounts",
		map[string]any{"email": "b@example.com", "password": "hunter2-hash-xyz"},
	)

	logs := auditLogs(t, app)
	if len(logs) != 2 {
		t.Fatalf("audit entries = %d, want 2", len(logs))
	}

	for _, l := range logs {
		raw, err := l.Changes.Value()
		if err != nil {
			t.Fatal(err)
		}

		text, _ := raw.(string)
		if b, ok := raw.([]byte); ok {
			text = string(b)
		}

		for _, secret := range []string{"hunter2", "tok_secret", "tok_new"} {
			if strings.Contains(text, secret) {
				t.Errorf("audit %s entry leaks %q: %s", l.Action, secret, text)
			}
		}
	}

	if ch := logs[1].Changes.V["api_token"]; !ch.Redacted || ch.Before != nil || ch.After != nil {
		t.Errorf("api_token change = %+v, want redacted without values", ch)
	}

	if _, ok := logs[1].Changes.V["password"]; ok {
		t.Error("unchanged password recorded in the audit log")
	}

	c.Get("/admin/_audit").AssertOK().AssertSee("redacted").AssertDontSee("tok_")
}

func TestSecurity_A05_AdminSortAndSearchAllowlisted(t *testing.T) {
	t.Parallel()

	app := newApp(t, options{})
	c := as(t, app, "admin")

	createPost(t, app, Post{Title: "Alpha", Body: "zzz", Status: "live"})
	createPost(t, app, Post{Title: "Bravo", Body: "aaa", Status: "live"})

	for _, sort := range []string{
		"title; DROP TABLE posts", "body", "status", "published) --", "(SELECT 1)", "password",
	} {
		c.Get("/admin/posts?" + url.Values{"sort": {sort}, "dir": {"asc; DROP TABLE posts"}}.Encode()).
			AssertOK().
			AssertSee("Bravo") // ignored: default order (newest first)
	}

	c.Get("/admin/posts?" + url.Values{"q": {"' OR '1'='1"}}.Encode()).AssertOK().AssertSee("0 records")
	c.Get("/admin/posts?" + url.Values{"filter[body]": {"zzz"}, "filter[status]": {"x' OR 1=1 --"}}.Encode()).
		AssertOK().AssertSee("2 records") // unknown filters and options are ignored

	vukemtest.AssertDatabaseCount(t, app, "posts", 2)
}

func TestSecurity_A02_AdminStrictHeaders(t *testing.T) {
	t.Parallel()

	app := newApp(t, options{})
	res := as(t, app, "admin").Get("/admin").AssertOK()

	if cc := res.Header("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}

	csp := res.Header("Content-Security-Policy")
	if csp == "" || strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "unsafe-eval") {
		t.Errorf("Content-Security-Policy = %q, want a strict policy", csp)
	}
}
