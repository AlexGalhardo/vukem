# Tutorial: a SaaS with Inertia

Build a small SaaS: React pages rendered through Inertia, sign-up with two-factor authentication, Stripe subscriptions, and a `Project` resource that is private to its owner and limited to three projects on the free tier. The finished code is [`examples/saas`](../../examples/saas); CI runs its Go tests (no Node needed) and type-checks and builds its front end.

You need Go 1.27, Node 20+ for Vite, and the CLI:

```sh
go install github.com/AlexGalhardo/vukem/cmd/vukem@latest
```

## 1. Create the app

```sh
vukem new saas --inertia react
cd saas
vukem install:auth --2fa
vukem install:billing
vukem migrate
vukem serve
```

- `--inertia react` swaps the HTMX views for React pages in `resources/js/pages` with Vite, Tailwind and TypeScript, and runs `npm install` (skip it with `--no-install`). `vue` and `svelte` work too; see [frontend.md](../frontend.md#inertia-v3-beta).
- `install:auth --2fa` adds registration, login, password reset, email verification and TOTP two-factor authentication with recovery codes ([authentication.md](../authentication.md)).
- `install:billing` adds Stripe Checkout, the Billing Portal, webhooks and the `Subscribed()` / `OnPlan()` middleware ([billing.md](../billing.md)).
- `vukem serve` runs the Go app and the Vite dev server together.

To sell plans, create a Stripe sandbox and fill `.env`:

```sh
STRIPE_KEY=rk_test_...
STRIPE_PRICE_BASIC=price_...
STRIPE_PRICE_PRO=price_...
```

```sh
stripe listen --forward-to localhost:3000/stripe/webhook   # prints STRIPE_WEBHOOK_SECRET
```

## 2. The Project model

The starter ships a `posts` CRUD to learn from. The example deletes it (controller, model, request, factory, seeder, migration, pages and test) and generates projects instead:

```sh
vukem make:model Project -m -f
vukem make:policy Project
vukem make:controller Project --resource
```

Fill the migration and the model:

```go
return s.Create("projects", func(t *schema.Table) {
	t.ID()
	t.ForeignID("user_id").Constrained("users").CascadeOnDelete()
	t.String("name")
	t.Text("description")
	t.Timestamps()
	t.Index("user_id")
})
```

```go
type Project struct {
	orm.Model

	UserID      int64  `json:"-"`
	Name        string `json:"name"`
	Description string `json:"description"`
}
```

`UserID` has `json:"-"`: page props are JSON, and the owner's id has no business in the browser.

## 3. The policy

`app/policies/project_policy.go` keeps projects private and counts them for the free tier. The policy is a struct, so it can hold the database and the billing service; a failed query denies (fail closed):

```go
func (p ProjectPolicy) Create(ctx context.Context, u *models.User) authz.Result {
	n, err := orm.QueryOf[models.Project](p.DB).Where("user_id", u.ID).Count(ctx)
	if err != nil {
		return authz.Deny()
	}

	if n < FreeProjects {
		return authz.Allow()
	}

	sub, ok, err := p.Billing.Current(ctx, u.ID)
	if err == nil && ok && sub.Valid() {
		return authz.Allow()
	}

	return authz.Deny("Free accounts have up to 3 projects. Choose a plan to add more.")
}

func (ProjectPolicy) View(_ context.Context, u *models.User, p *models.Project) authz.Result {
	if p.UserID == u.ID {
		return authz.Allow()
	}

	return authz.DenyAsNotFound()
}
```

`DenyAsNotFound` answers 404, so other users cannot even learn that a project id exists. `Update` and `Delete` reuse `View`.

## 4. Routes and the gate

`routes/projects.go` builds the gate with the user of the `web` guard and mounts the resource behind `auth.Required`. Append `Projects` to `routes/registry.go`:

```go
gate := authz.New(func(ctx context.Context) (*models.User, error) {
	u, err := auth.UserFrom[models.User](ctx, "web")
	return &u, err
})
if err := gate.Register(policies.All(acc.DB, b)...); err != nil {
	return err
}

r.Services().Authorizer = gate

r.Resource("projects", controllers.NewProjectController(acc.DB)).Use(auth.Required(acc.Web))
```

## 5. The controller

Each action renders an Inertia page with props; there are no Go templates for these pages. Lists only ever query the current user's rows:

```go
func (pc *ProjectController) Index(c *web.Ctx) error {
	u, err := auth.User[models.User](c, "web")
	if err != nil {
		return err
	}

	q := orm.QueryOf[models.Project](pc.db).Where("user_id", u.ID).OrderBy("id", orm.Desc)
	if search := c.Query("q"); search != "" {
		q = q.Where("name", "LIKE", "%"+search+"%")
	}

	projects, err := q.Get(c.Context())
	if err != nil {
		return err
	}

	return inertia.Render(c, "Projects/Index", inertia.Props{
		"projects":  projects,
		"filters":   map[string]string{"q": c.Query("q")},
		"freeLimit": policies.FreeProjects,
	})
}
```

Single-project actions load the row and ask the policy:

```go
project, err := orm.QueryOf[models.Project](pc.db).Find(c.Context(), id)
if err != nil {
	return models.Project{}, err
}

return project, c.Authorize(ability, &project)
```

When the free tier is used up, pages send the user to `/billing` with a flash message, while JSON clients get the 403 problem:

```go
func denyCreate(c *web.Ctx, err error) error {
	if errs.Status(err) != http.StatusForbidden || c.WantsJSON() {
		return err
	}

	_ = c.Flash("status", "Free accounts have up to 3 projects. Choose a plan to add more.")

	return c.Redirect("/billing")
}
```

Validation errors need no code: `web.Parse` fails, Inertia redirects back, and the page receives `props.errors`.

## 6. The pages

`resources/js/pages/Projects/{Index,Create,Edit,Show}.tsx` and `components/ProjectForm.tsx` are plain React. The form posts with Inertia's `useForm`, which shows the server's validation messages:

```tsx
const form = useForm({ name: project?.name ?? "", description: project?.description ?? "" });

const submit = (e: FormEvent) => {
  e.preventDefault();

  if (project) {
    form.put(`/projects/${project.id}`);
  } else {
    form.post("/projects");
  }
};
```

The prop shapes live in `resources/js/types.ts`; `npm run typecheck` checks them.

## 7. Tests without Node

Inertia responses are page objects, so the Go tests assert components and props directly. No Vite build is needed:

```go
c := app.Client(t).ActingAs("web", u).Inertia("")

c.PostJSON("/projects", map[string]any{"name": "Launch", "description": "Q4 launch plan"}).
	AssertRedirect("/projects/1")
c.Get("/projects").AssertOK().
	AssertInertia("Projects/Index").
	HasProp("projects", 1).
	PropEquals("freeLimit", 3)
```

`TestProjectsFreeLimit` reuses the fake Stripe server that `install:billing` generated in `tests/billing_test.go`: the fourth project redirects to `/billing` until a signed `customer.subscription.created` webhook marks the subscription active.

```go
c.PostJSON("/projects", map[string]any{"name": "Fourth"}).AssertRedirect("/billing")

setCustomer(t, app, u, "cus_limit")
f.setSubscription("sub_limit", "cus_limit", "active", "price_basic")
postWebhook(t, app, "evt_limit", "customer.subscription.created", map[string]any{"id": "sub_limit"}).AssertOK()

c.PostJSON("/projects", map[string]any{"name": "Fourth"}).AssertRedirect("/projects/4")
```

```sh
go test ./...
npm run typecheck
npm run build
```

## Next

- [Blog with HTMX](blog-htmx.md): server-rendered views and htmx fragments.
- [JSON API with tokens](json-api.md): typed routes, OpenAPI and a generated client.
