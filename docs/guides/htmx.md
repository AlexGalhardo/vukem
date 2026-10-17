# HTMX starter

The default starter: server-rendered `html/template` views with htmx 4, Tailwind through Vite, and SQLite. Owning docs: [frontend.md](../frontend.md#htmx), [http.md](../http.md), [database.md](../database.md).

## Create

```sh
vukem new blog                     # or: vukem new blog --module github.com/you/blog
cd blog
vukem migrate
```

`vukem new` writes the starter, runs `go mod tidy`, generates `APP_KEY` in `.env`, runs `npm install` (skip with `--no-install`) and `git init`.

## Run

```sh
vukem serve        # http://localhost:3000
```

`vukem serve` builds and runs `cmd/app`, restarts it when a `.go` file or `.env` changes, and starts the Vite dev server (`--no-vite` to skip it). Template edits show up on refresh without a restart, because the views are read from disk in development. `--poll 1s` changes how often files are checked.

## Project layout

```text
cmd/app/main.go                 the app binary: HTTP server by default, or an app command
bootstrap/app.go                vukem.New with options (views, Vite, providers)
bootstrap/commands.go           app commands (make:command appends here)
bootstrap/providers.go          providers (make:provider appends here)
routes/web.go                   browser routes; routes/registry.go lists the route files
app/controllers/                handlers: home.go, post_controller.go (example CRUD), upload_controller.go
app/models/                     ORM models (post.go)
app/requests/                   z schemas for input (post.go)
app/jobs/, app/listeners/       queue jobs and event listeners registries
database/migrations/            migrations; registry.go lists them in order
database/factories/, seeders/   fake data for tests and vukem db:seed
resources/views/                layouts/, partials/, components/, posts/, home.html
resources/css, resources/js     Tailwind and the Vite entry
resources/public/               static files served at / (vendored htmx)
storage/                        SQLite database (storage/database.sqlite) and local files
tests/                          HTTP tests using vukemtest
```

## Add a page

A page defines blocks rendered inside `layouts/app.html`:

```html
{{define "title"}}About{{end}}
{{define "content"}}
<h1>About</h1>
<p>Built with Vukem.</p>
{{end}}
```

Save it as `resources/views/about.html`, add a handler and a named route:

```go
// app/controllers/about.go
func About(c *web.Ctx) error { return c.Render("about", nil) }

// routes/web.go, inside Web
r.Get("/about", controllers.About).Name("about")
```

Link to it with `{{route "about"}}`. For htmx requests, render only one block with `c.Render("posts/index", data, web.Block("post-list"))`, as the posts index does for its search box ([frontend.md](../frontend.md#rendering)). `vukem route:list` prints the route table.

## Add a model and a migration

```sh
vukem make:model Comment -m -f -c   # model, create_comments_table migration, factory, resource controller
vukem migrate
```

- The model lands in `app/models/comment.go`, the migration in `database/migrations/<timestamp>_create_comments_table.go` (appended to `registry.go`), the factory in `database/factories/` and the controller in `app/controllers/comment_controller.go`.
- Add columns in the migration's `Up` with the schema builder (`t.String("body")`, `t.ForeignID("post_id")`...), then `vukem migrate`. `vukem migrate:status`, `migrate:rollback` and `migrate:fresh --seed` manage the history ([database.md](../database.md#migrations)).
- `vukem make:migration add_published_at_to_posts_table` creates an alter-table migration; the name prefix picks the stub.
- Register the controller with `r.Resource("comments", controllers.NewCommentController())` and validate input with a schema from `vukem make:request Comment`.

## Authentication

```sh
vukem install:auth                       # login, registration, password reset, email verification
vukem install:auth --2fa                 # also TOTP two-factor authentication
vukem install:auth --social github,google
vukem migrate
```

`install:auth` copies user-owned code (`app/accounts`, `app/controllers/account`, `resources/views/auth`, `routes/auth.go`, `tests/auth_test.go`) and registers its migrations and routes. `--2fa` and `--social` also work on a project that already ran `install:auth`; social providers are `github`, `gitlab`, `discord`, `google`, `microsoft`, `linkedin`, `x` and `apple`, with credentials in `SOCIAL_<NAME>_CLIENT_ID` / `_SECRET`. Set `MAIL_MAILER` and `APP_URL` so verification and reset emails work. See [authentication.md](../authentication.md#installauth-scaffold).

Next steps on top of auth: `vukem install:admin` (admin panel at `/admin`, [admin.md](../admin.md)), `vukem install:api` (tokens and `/api/v1` beside the web routes, [api.md](../api.md#starter)) and `vukem install:billing` (Stripe subscriptions, [billing.md](../billing.md)).

## Tests

```sh
go test ./...
vukem make:test Comments            # tests/comments_test.go with the fluent client
```

Each test boots the app with an isolated in-memory database:

```go
app := vukemtest.New(t, bootstrap.New)

app.Client(t).PostForm("/posts", url.Values{"title": {"Hello"}, "body": {"First."}}).
	AssertRedirect("/posts/1").
	AssertSessionHas("success")
app.Client(t).HTMX().Get("/posts?q=Hello").AssertOK().AssertFragment()
vukemtest.AssertDatabaseHas(t, app, "posts", map[string]any{"title": "Hello"})
```

See [testing.md](../testing.md#toolkit-for-apps-testing-package-imported-as-vukemtest).

## Build and deploy

```sh
vukem build                        # vite build, then go build -trimpath -ldflags="-s -w" -o bin/app ./cmd/app
```

The binary embeds `resources/views` and `resources/public`, so a deployment is one file plus its environment:

```sh
APP_ENV=production APP_DEBUG=false APP_URL=https://example.com ./bin/app migrate
APP_ENV=production APP_DEBUG=false APP_URL=https://example.com ./bin/app
```

- Provide a unique `APP_KEY` (or `APP_KEY=@file:/run/secrets/app_key`); the app refuses to boot with `APP_DEBUG=true` in production.
- Point `DATABASE_URL` at the production database; PostgreSQL and MySQL need their driver modules ([database.md](../database.md#connections)).
- Terminate TLS at a reverse proxy and configure trusted proxies; `SIGTERM` drains in-flight requests within `SHUTDOWN_TIMEOUT`.
- `vukem down --with-secret` and `vukem up`, run in the app's working directory, toggle maintenance mode (`storage/framework/down`) during a release.

Full checklist: [configuration.md](../configuration.md) and [security.md](../security.md).
