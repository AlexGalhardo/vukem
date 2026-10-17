package tests

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/AlexGalhardo/vukem/examples/saas/app/models"
	"github.com/AlexGalhardo/vukem/examples/saas/bootstrap"
	"github.com/AlexGalhardo/vukem/examples/saas/database/factories"
	"github.com/AlexGalhardo/vukem/vukemtest"
)

func TestProjectsCRUD(t *testing.T) {
	t.Parallel()

	app := vukemtest.New(t, bootstrap.New)
	u := newUser(t, app, "owner@example.com")
	c := app.Client(t).ActingAs("web", u).Inertia("")

	c.PostJSON("/projects", map[string]any{"name": "Launch", "description": "Q4 launch plan"}).
		AssertRedirect("/projects/1")
	vukemtest.AssertDatabaseHas(t, app, "projects", map[string]any{"name": "Launch", "user_id": u.ID})

	c.Get("/projects/1").AssertOK().
		AssertInertia("Projects/Show").
		PropEquals("project.name", "Launch")
	c.Get("/projects").AssertOK().
		AssertInertia("Projects/Index").
		HasProp("projects", 1).
		PropEquals("freeLimit", 3)
	c.Get("/projects?q=nothing").AssertOK().
		AssertInertia("Projects/Index").
		HasProp("projects", 0).
		PropEquals("filters.q", "nothing")
	c.Get("/projects/1/edit").AssertOK().AssertInertia("Projects/Edit").PropEquals("project.id", 1)

	c.PutJSON("/projects/1", map[string]any{"name": "Launch v2"}).AssertRedirect("/projects/1")
	vukemtest.AssertDatabaseHas(t, app, "projects", map[string]any{"name": "Launch v2"})

	c.Delete("/projects/1").AssertRedirect("/projects")
	vukemtest.AssertDatabaseCount(t, app, "projects", 0)
}

func TestProjectsValidation(t *testing.T) {
	t.Parallel()

	app := vukemtest.New(t, bootstrap.New)
	u := newUser(t, app, "forms@example.com")
	c := app.Client(t).ActingAs("web", u).Inertia("").WithHeader("Referer", "/projects/create")

	c.PostJSON("/projects", map[string]any{"name": "x"}).AssertRedirect("/projects/create")
	c.Get("/projects/create").AssertInertia("Projects/Create").HasProp("errors.name")

	vukemtest.AssertDatabaseCount(t, app, "projects", 0)
}

func TestProjectsArePrivate(t *testing.T) {
	t.Parallel()

	app := vukemtest.New(t, bootstrap.New)
	owner := newUser(t, app, "ana@example.com")
	other := newUser(t, app, "bruno@example.com")

	db, err := app.DB()
	if err != nil {
		t.Fatal(err)
	}

	p, err := factories.Project.State(func(p *models.Project) { p.UserID = owner.ID }).CreateOne(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}

	path := "/projects/" + strconv.FormatInt(p.ID, 10)
	c := app.Client(t).ActingAs("web", other).Inertia("")

	c.Get("/projects").AssertInertia("Projects/Index").HasProp("projects", 0)
	c.Get(path).AssertNotFound()
	c.Get(path + "/edit").AssertNotFound()
	c.PutJSON(path, map[string]any{"name": "Hijacked"}).AssertNotFound()
	c.Delete(path).AssertNotFound()
	vukemtest.AssertDatabaseHas(t, app, "projects", map[string]any{"id": p.ID, "user_id": owner.ID})

	app.Client(t).Get("/projects").AssertRedirect("/login")
}

func TestProjectsFreeLimit(t *testing.T) {
	t.Parallel()

	f := newFakeStripe(t)
	app := billingApp(t, f)
	u := newUser(t, app, "limit@example.com")
	c := app.Client(t).ActingAs("web", u).Inertia("")

	db, err := app.DB()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := factories.Project.State(func(p *models.Project) { p.UserID = u.ID }).
		Count(3).Create(t.Context(), db); err != nil {
		t.Fatal(err)
	}

	// The fourth project needs a plan: pages go to /billing, JSON gets 403.
	c.Get("/projects/create").AssertRedirect("/billing")
	c.PostJSON("/projects", map[string]any{"name": "Fourth"}).AssertRedirect("/billing")
	app.Client(t).ActingAs("web", u).AcceptJSON().
		PostJSON("/projects", map[string]any{"name": "Fourth"}).
		AssertStatus(http.StatusForbidden)
	vukemtest.AssertDatabaseCount(t, app, "projects", 3)

	setCustomer(t, app, u, "cus_limit")
	f.setSubscription("sub_limit", "cus_limit", "active", "price_basic")
	postWebhook(t, app, "evt_limit", "customer.subscription.created", map[string]any{"id": "sub_limit"}).AssertOK()

	c.PostJSON("/projects", map[string]any{"name": "Fourth"}).AssertRedirect("/projects/4")
	vukemtest.AssertDatabaseCount(t, app, "projects", 4)
}
