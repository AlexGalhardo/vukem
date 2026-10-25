# Tutorial: a JSON API with tokens

Build a JSON API where users exchange their password for a bearer token and manage private tasks, with filtering, sorting, pagination, rate limiting, an OpenAPI 3.1 document and a generated Go client. The finished code is [`examples/api`](../../examples/api); CI runs its tests.

The docs site publishes this tutorial as [Tutorial: a JSON API](https://vukem.dev/docs/rest-apis/tutorial) (`site/content/docs/rest-apis/tutorial.mdx`); keep both in step.

You need Go 1.27, `curl` and the CLI:

```sh
go install github.com/AlexGalhardo/vukem/cmd/vukem@latest
```

## 1. Create the app

```sh
vukem new api --api
cd api
vukem migrate
vukem serve
```

`--api` writes the JSON starter: no views, sessions or CSRF, `vukem.APIMiddleware`, problem+json errors, and what `vukem install:api` adds to other apps: `POST /api/v1/tokens`, `GET /api/v1/me`, `DELETE /api/v1/tokens/current`, CORS from `CORS_ALLOWED_ORIGINS` and rate limits ([api.md](../api.md)). It also ships a public `/api/v1/posts` resource to learn from.

## 2. A command to create users

The API has no sign-up page, so add a command:

```sh
vukem make:command CreateUser
```

`app/commands/create_user.go` registers `user:create`, hashes the password with the app's hasher and reads it from standard input, so it stays out of shell history:

```go
return vukem.CommandFunc{
	CommandName: "user:create",
	Desc:        "Create a user (password on stdin): vukem user:create -name Ada ada@example.com",
	DefineFlags: func(fs *flag.FlagSet) { name = fs.String("name", "API User", "display name") },
	Fn: func(ctx context.Context, app *vukem.App, args []string) error {
		// ...
		u := models.User{Name: *name, Email: strings.ToLower(args[0]), Password: hash}
		if err := orm.Create(ctx, acc.DB, &u); err != nil {
			return fmt.Errorf("user:create: %w", err)
		}
		// ...
	},
}
```

```sh
echo "correct horse battery" | vukem user:create -name Ada ada@example.com
# created user 1 <ada@example.com>
```

## 3. Tokens

```sh
curl -s -X POST localhost:3000/api/v1/tokens \
  -H 'Content-Type: application/json' -H 'Accept: application/json' \
  -d '{"email":"ada@example.com","password":"correct horse battery","device_name":"curl","abilities":["read","write"]}'
```

```json
{"abilities":["read","write"],"expires_at":"2026-11-16T09:29:28.3005936Z","token":"<id>.<secret>","type":"Bearer"}
```

A token carries only the abilities the request asks for (`read`, `write`; none when omitted), and `GET /api/v1/me` needs `read`. Only a verified email gets a token; `user:create` marks the address verified. Tokens are opaque, stored hashed, and expire after 30 days ([authentication.md](../authentication.md#token-guard-api)). Five token requests per minute per IP are allowed. Keep the token for the next steps:

```sh
TOKEN='<id>.<secret>'   # the token from the response
```

## 4. The Task model

```sh
vukem make:model Task -m -f
vukem make:resource Task
vukem migrate
```

Every task belongs to a user:

```go
return s.Create("tasks", func(t *schema.Table) {
	t.ID()
	t.ForeignID("user_id").Constrained("users").CascadeOnDelete()
	t.String("title")
	t.Enum("status", "todo", "doing", "done").Default("todo")
	t.Timestamps()
	t.Index("user_id", "status")
})
```

`app/resources/task.go` is the wire format, separate from the model so columns never leak by accident. `validate` tags on output types only shape the OpenAPI schema (required fields, enums):

```go
type TaskJSON struct {
	ID        int64     `json:"id"         validate:"required"`
	Title     string    `json:"title"      validate:"required"`
	Status    string    `json:"status"     validate:"required,oneof=todo doing done"`
	CreatedAt time.Time `json:"created_at" validate:"required"`
	UpdatedAt time.Time `json:"updated_at" validate:"required"`
}
```

## 5. Typed inputs

`app/requests/task.go` declares what each endpoint accepts. Query strings, JSON bodies and path parameters are decoded into these structs and validated before the handler runs (422 with field errors):

```go
type ListTasks struct {
	Status  string `json:"status"   validate:"oneof=todo doing done"                         doc:"Only tasks with this status"`
	Q       string `json:"q"        validate:"max=100"                                       doc:"Search in titles"`
	Sort    string `json:"sort"     validate:"oneof=title -title created_at -created_at -id" doc:"Sort column; - for descending"`
	Page    int    `json:"page"     validate:"min=1"`
	PerPage int    `json:"per_page" validate:"min=1,max=100"`
}

type UpdateTask struct {
	Task   int64  `json:"task"   validate:"required,positive"`
	Title  string `json:"title"  validate:"required,min=3,max=120"`
	Status string `json:"status" validate:"required,oneof=todo doing done"`
}
```

## 6. The controller

Typed handlers take the input struct and return the output value (`app/controllers/apiv1/tasks.go`). Every query starts from the authenticated user's rows, so another user's task is a 404:

```go
func (t *Tasks) owned(c *web.Ctx) (orm.Query[models.Task], error) {
	u, err := auth.User[models.User](c, "api")
	if err != nil {
		return orm.Query[models.Task]{}, err
	}

	return orm.QueryOf[models.Task](t.db).Where("user_id", u.ID), nil
}
```

`Index` filters, then lets `api.Query` apply the allowlisted sort and the capped page size:

```go
func (t *Tasks) Index(c *web.Ctx, in requests.ListTasks) (resources.TaskPageJSON, error) {
	q, err := t.owned(c)
	if err != nil {
		return resources.TaskPageJSON{}, err
	}

	if in.Status != "" {
		q = q.Where("status", in.Status)
	}

	if in.Q != "" {
		q = q.Where("title", "LIKE", "%"+in.Q+"%")
	}

	// api.Query allowlists the sort columns and caps per_page.
	req, err := api.Query(c, q, api.Sorts("title", "created_at", "-id"), api.PerPage(20, 100))
	if err != nil {
		return resources.TaskPageJSON{}, err
	}

	page, err := req.Paginate(c.Context())
	if err != nil {
		return resources.TaskPageJSON{}, err
	}

	return resources.TaskPage(page), nil
}
```

Values are always bound parameters; column names come only from the allowlist.

## 7. Routes, auth and rate limiting

`routes/tasks.go` (appended to `routes/registry.go`) registers the typed routes behind the token guard with 60 requests per minute per token, and serves the OpenAPI document outside production:

```go
limit := ratelimit.Middleware(ratelimit.NewMemory(60, time.Minute), ratelimit.ByToken)
bearer := []string{"bearer"}
tags := []string{"tasks"}

r.Group("/api/v1", func(g *router.Group) {
	g.Use(middleware.JSONOnly(), limit, auth.Required(acc.API))

	api.Register(g, "GET /tasks", api.Op{
		Name: "tasks.index", Summary: "List your tasks", Tags: tags, Security: bearer,
	}, tasks.Index)
	api.Register(g, "POST /tasks", api.Op{
		Name: "tasks.store", Summary: "Create a task", Tags: tags, Security: bearer,
	}, tasks.Store)
	// GET, PUT and DELETE /tasks/{task} the same way
}).Name("api.v1.")

// Not registered in production unless OPENAPI_IN_PRODUCTION=true.
vukem.ServeOpenAPI(app, r, "/openapi.json")
```

`ratelimit.NewMemory` keeps counters in the process; use a shared store when you run several instances.

## 8. Try it with curl

```bash
H=(-H "Authorization: Bearer $TOKEN" -H 'Accept: application/json' -H 'Content-Type: application/json')

curl -s "${H[@]}" -X POST localhost:3000/api/v1/tasks -d '{"title":"Write the tutorial"}'
# {"id":1,"title":"Write the tutorial","status":"todo","created_at":"...","updated_at":"..."}

curl -s "${H[@]}" -X POST localhost:3000/api/v1/tasks -d '{"title":"Ship it","status":"doing"}'

curl -s -i "${H[@]}" 'localhost:3000/api/v1/tasks?status=doing&sort=-created_at&per_page=10'
# Ratelimit-Limit: 60
# Ratelimit-Remaining: 57
# {"data":[{"id":2,"title":"Ship it","status":"doing",...}],"meta":{"page":1,"per_page":10,"total":1,"last_page":1}}

curl -s "${H[@]}" -X PUT localhost:3000/api/v1/tasks/1 -d '{"title":"Write the tutorial","status":"done"}'

curl -s "${H[@]}" -X POST localhost:3000/api/v1/tasks -d '{"title":"x","status":"later"}'
# {"type":"about:blank","title":"Validation failed","status":422,...,
#  "errors":{"status":["The selected status is invalid."],"title":["The title field must be at least 3 characters."]}}

curl -s -o /dev/null -w '%{http_code}\n' "${H[@]}" -X DELETE localhost:3000/api/v1/tasks/1
# 204

curl -s -H 'Accept: application/json' localhost:3000/api/v1/tasks
# {"type":"about:blank","title":"Unauthorized","status":401,"detail":"Unauthenticated.",...}
```

After 60 requests in a minute the API answers 429 with `Retry-After`. Revoke the token with `curl -X DELETE localhost:3000/api/v1/tokens/current -H "Authorization: Bearer $TOKEN"`.

## 9. OpenAPI and a typed client

Typed routes describe themselves; no annotations are needed:

```sh
vukem openapi:export > openapi.json
curl -s localhost:3000/openapi.json        # same document, development only
```

Generate a client from the same document:

```sh
vukem codegen:client -lang go     # internal/apiclient/client.go
vukem codegen:client              # TypeScript: resources/js/api.ts
```

The Go client has one method per operation, with typed path parameters, query and body:

```go
c := apiclient.New("http://localhost:3000")
c.Header = func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }

created, err := c.TasksStore(ctx, apiclient.TasksStoreBody{Title: "From the Go client"})
page, err := c.TasksIndex(ctx, apiclient.TasksIndexQuery{})

_, err = c.TasksStore(ctx, apiclient.TasksStoreBody{Title: "x"})
if apiErr, ok := errors.AsType[*apiclient.Error](err); ok && apiErr.Status == http.StatusUnprocessableEntity {
	// apiErr.Problem.Errors["title"]
}
```

## 10. Tests

`tests/tasks_test.go` drives the API with the fluent client and checks each response against the OpenAPI document:

```go
ada := app.Client(t).WithToken(apiUser(t, app, "ada@example.com"))

ada.GetJSON("/api/v1/tasks?sort=title&per_page=2").AssertOK().
	AssertMatchesOpenAPI().
	AssertJSONCount("data", 2).
	AssertJSONPath("meta.total", 3)

bob := app.Client(t).WithToken(apiUser(t, app, "bob@example.com"))
bob.GetJSON("/api/v1/tasks/1").AssertProblem(404)
```

`tests/client_test.go` fails when `internal/apiclient` is stale (rerun `vukem codegen:client -lang go`) and runs the generated client against the app over `httptest`.

```sh
go test ./...
```

## Next

- [Blog with HTMX](blog-htmx.md): server-rendered views and htmx fragments.
- [SaaS with Inertia](saas-inertia.md): React pages, two-factor auth and Stripe billing.
