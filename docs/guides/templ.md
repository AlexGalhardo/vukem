# templ starter

The [HTMX starter](htmx.md) with [templ](https://templ.guide) components instead of `html/template` views: the same pages, CRUD, htmx search fragment, flash messages, validation errors, CSRF field, CSP nonces and Vite tags, type-checked by the Go compiler. Owning docs: [frontend.md](../frontend.md#templ-starter).

## Create

```sh
vukem new blog --templ
cd blog
vukem migrate
```

Besides the HTMX starter steps, `--templ` pins templ as a Go tool (`go get -tool github.com/a-h/templ/cmd/templ@...`, a `tool` line in `go.mod`) and runs `go tool templ generate`. The generated `*_templ.go` files are committed, so `go build` and `go test` work without running templ.

## Run

```sh
vukem serve        # http://localhost:3000
```

`vukem serve` runs `go tool templ generate` before every build when the project has `.templ` files and restarts on `.go` and `.templ` changes; it also starts the Vite dev server (`--no-vite` to skip it). Regenerate by hand with `go tool templ generate`.

## Project layout

The layout is the HTMX starter's ([htmx.md](htmx.md#project-layout)) with these differences:

```text
resources/views/layout.templ    Layout(title): the page shell, Vite tags, flash messages
resources/views/home.templ      Home()
resources/views/posts.templ     PostsIndex, PostList (the htmx fragment), forms
resources/views/helpers.go      route, asset, viteTags, script, flash, old, fieldError, csrfField
resources/views/*_templ.go      generated, committed
routes/web.go                   also r.Use(app.Vite.Middleware()) so components can render Vite tags
```

`bootstrap/app.go` has no `vukem.WithViews`: handlers render components with `c.View`.

## Add a page

```templ
// resources/views/about.templ
package views

templ About() {
  @Layout("About") {
    <h1>About</h1>
    <p><a href={ route(ctx, "home") }>Home</a></p>
  }
}
```

```go
// app/controllers/about.go
func About(c *web.Ctx) error { return c.View(views.About()) }

// routes/web.go, inside Web
r.Get("/about", controllers.About).Name("about")
```

Components only get the request `ctx`, so use the helpers in `helpers.go` (`route(ctx, ...)`, `csrfField(ctx)`, `flash(ctx, ...)`) rather than template functions. Scripts go through `script`/`viteTags`, which add the CSP nonce; do not write `<script>` tags in components. For an htmx fragment, pass the partial component: `c.View(views.PostsIndex(posts, q), web.Partial(views.PostList(posts)))`.

## Add a model and a migration

Identical to the HTMX starter:

```sh
vukem make:model Comment -m -f -c
vukem migrate
```

See [htmx.md](htmx.md#add-a-model-and-a-migration) and [database.md](../database.md#migrations). The generated controller is plain Go; render templ components from its actions.

## Authentication

`vukem install:auth` (including `--2fa` and `--social`) writes `html/template` pages and **refuses templ projects** for now. Until templ auth pages ship, either start from the [HTMX starter](htmx.md#authentication) or [Inertia](inertia.md#authentication), or wire the auth packages yourself: guards and middleware from `auth` ([authentication.md](../authentication.md#wiring)), TOTP from `auth/totp`, social sign-in from `social`.

## Tests

```sh
go test ./...
```

`tests/views_test.go` checks that the components keep the starter's guarantees (CSP nonces on scripts, a CSRF field in forms, validation errors and old input), next to the CRUD tests:

```go
app := vukemtest.New(t, bootstrap.New)

app.Client(t).Get("/posts/create").AssertOK().
	AssertSee(`<input type="hidden" name="` + csrf.FieldName + `" value="`)
```

Component output can also be tested without HTTP by rendering it to a buffer with `views.About().Render(ctx, &buf)`. See [testing.md](../testing.md).

## Build and deploy

```sh
vukem build        # go tool templ generate, vite build, then go build into bin/app
```

Deploy `bin/app` as described in the [HTMX guide](htmx.md#build-and-deploy): `APP_ENV=production`, `APP_DEBUG=false`, a unique `APP_KEY`, `APP_URL`, `./bin/app migrate`, then `./bin/app`.
