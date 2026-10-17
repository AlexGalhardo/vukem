# Tutorial: a blog with HTMX

Build a multi-author blog with server-rendered `html/template` views, HTMX for live search and inline deletes, sign-in, authorization policies and comments. The finished code is [`examples/blog`](../../examples/blog); CI runs its tests on SQLite and Postgres.

You need Go 1.27 and the CLI:

```sh
go install github.com/AlexGalhardo/vukem/cmd/vukem@latest
```

## 1. Create the app

```sh
vukem new blog
cd blog
vukem install:auth
vukem migrate
vukem serve
```

`vukem new` writes the HTMX + SQLite starter: a vendored htmx, Tailwind, a `posts` CRUD to start from and a test suite. `install:auth` adds registration, login, password reset and email verification (see [authentication.md](../authentication.md)). Open <http://localhost:3000>.

## 2. Posts belong to authors, comments to posts

The starter's `Post` has a title and a body. Give it an author, then add comments:

```sh
vukem make:model Comment -m
```

Nothing is deployed yet, so the example adds `t.ForeignID("user_id").Constrained("users").CascadeOnDelete()` to the posts migration itself; on a live app write a new migration with `vukem make:migration`. The comments migration references both tables:

```go
return s.Create("comments", func(t *schema.Table) {
	t.ID()
	t.ForeignID("post_id").Constrained("posts").CascadeOnDelete()
	t.ForeignID("user_id").Constrained("users").CascadeOnDelete()
	t.Text("body")
	t.Timestamps()
})
```

Relations are typed fields; nothing is loaded until you ask with `With` ([database.md](../database.md)):

```go
type Post struct {
	orm.Model

	UserID int64  `json:"user_id"`
	Title  string `json:"title"`
	Body   string `json:"body"`

	User     orm.BelongsTo[User]  `json:"author,omitzero"`
	Comments orm.HasMany[Comment] `json:"comments,omitzero"`
}
```

Inputs are validated with `z` schemas in `app/requests/post.go`:

```go
var CommentSchema = z.Struct[Comment](z.Shape{"body": z.String().Trim().Min(2).Max(2000)})
```

Run `vukem migrate` again.

## 3. The controller

`app/controllers/post_controller.go` keeps the resource actions of the starter. `Index` eager-loads the authors in one extra query and, for HTMX requests, renders only the `post-list` block:

```go
func (pc *PostController) Index(c *web.Ctx) error {
	q := orm.QueryOf[models.Post](pc.db).OrderBy("id", orm.Desc).With("User")
	if search := c.Query("q"); search != "" {
		q = q.Where("title", "LIKE", "%"+search+"%")
	}

	posts, err := q.Get(c.Context())
	if err != nil {
		return err
	}

	if c.WantsJSON() {
		return c.JSON(http.StatusOK, map[string]any{"data": posts})
	}

	return c.Render("posts/index", map[string]any{"Posts": posts, "Search": c.Query("q")}, web.Block("post-list"))
}
```

`Store` takes the author from the session guard, and `Destroy` answers an empty 200 to HTMX so the row can be swapped out:

```go
switch {
case c.WantsJSON():
	return c.NoContent()
case c.IsHTMX():
	return c.String(http.StatusOK, "")
}
```

## 4. HTMX in the views

`resources/views/posts/index.html` searches as you type and deletes in place. `can` hides the button from users the policy would refuse:

```html
<input type="search" name="q" value="{{.Search}}" placeholder="Search posts" aria-label="Search posts"
  hx-get="{{route "posts.index"}}" hx-trigger="input changed delay:300ms, search" hx-target="#post-list">

{{block "post-list" .}}
<ul id="post-list" class="list">
  {{range .Posts}}
  <li>
    <a href="{{route "posts.show" "post" .ID}}">{{.Title}}</a>
    {{with .User.Get}}<span class="empty">{{.Name}}</span>{{end}}
    {{if can "delete" .}}<button type="button" class="link danger" hx-delete="{{route "posts.destroy" "post" .ID}}"
      hx-target="closest li" hx-swap="outerHTML" hx-confirm="Delete this post?">Delete</button>{{end}}
  </li>
  {{else}}
  <li class="empty">No posts yet.</li>
  {{end}}
</ul>
{{end}}
```

htmx requests need nothing extra for CSRF (`CrossOriginProtection` checks `Sec-Fetch-Site`); plain forms include `{{csrfField}}`. See [frontend.md](../frontend.md).

## 5. Authorization

```sh
vukem make:policy Post
```

Anyone signed in may write; only the author may change a post (`app/policies/post_policy.go`):

```go
func (PostPolicy) Update(_ context.Context, u *models.User, p *models.Post) authz.Result {
	if p.UserID == u.ID {
		return authz.Allow()
	}

	return authz.Deny("Only the author can change this post.")
}
```

Controllers call `c.Authorize("update", &post)`, which answers 403 on denial. The gate is built in `routes/web.go` with the user of the `web` guard:

```go
gate := authz.New(func(ctx context.Context) (*models.User, error) {
	u, err := auth.UserFrom[models.User](ctx, "web")
	return &u, err
})
if err := gate.Register(policies.All()...); err != nil {
	return err
}

r.Services().Authorizer = gate

posts := controllers.NewPostController(acc.DB)
optional, required := auth.Optional(acc.Web), auth.Required(acc.Web)

r.Get("/", controllers.Home).Name("home")
r.Resource("posts", posts).Only("index", "show").Use(optional)
r.Resource("posts", posts).Except("index", "show").Use(required)
r.Post("/posts/{post}/comments", posts.Comment).Name("posts.comments.store").Use(required)
```

`auth.Optional` lets guests read while templates still know who is signed in.

## 6. Demo data

```sh
vukem make:seeder Posts
vukem db:seed
```

The seeder creates two authors with five posts each from the factories in `database/factories`:

```go
for _, a := range authors {
	if _, err := factories.Post.Count(5).
		State(func(p *models.Post) { p.UserID = a.ID }).
		Create(ctx, db); err != nil {
		return err
	}
}
```

## 7. Tests

Tests boot the app on a private in-memory SQLite database and drive it with the fluent client ([testing.md](../testing.md)):

```go
intruder := app.Client(t).ActingAs("web", bob)

intruder.Get(path + "/edit").AssertForbidden()
intruder.HTMX().Delete(path).AssertForbidden()
vukemtest.AssertDatabaseHas(t, app, "posts", map[string]any{"title": "Ana's post"})

app.Client(t).ActingAs("web", ana).HTMX().Delete(path).AssertOK()
```

```sh
go test ./...
```

## 8. Postgres

Add the driver module and register it in `bootstrap/app.go`:

```sh
go get github.com/AlexGalhardo/vukem/drivers/postgres
```

```go
vukem.WithDatabaseDrivers(postgres.Driver()), // DATABASE_URL=postgres://... also works
```

`tests/helpers_test.go` switches to `TEST_DATABASE_URL` when it is set, which is how CI runs the same suite on Postgres:

```sh
TEST_DATABASE_URL=postgres://vukem:vukem@localhost:55432/vukem_test go test ./tests/
```

## Next

- [SaaS with Inertia](saas-inertia.md): React pages, two-factor auth and Stripe billing.
- [JSON API with tokens](json-api.md): typed routes, OpenAPI and a generated client.
