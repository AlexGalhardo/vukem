# Lumek

**Laravel and AdonisJS productivity, written in idiomatic Go.**

Lumek is a web framework built on `net/http` and the standard library for **REST APIs and full-stack apps**: routing, validation, an ORM with migrations, authentication, JSON resources with problem+json errors, HTMX-first views and an optional Inertia + React frontend, all driven by one CLI.

> [!WARNING]
> Lumek is in the **prototype** phase. Nothing is released yet and APIs will change. Follow progress in [PLAN.md](PLAN.md).

## Why Lumek

- **Standard library first.** Handlers and middleware are plain `net/http`. Any `http.Handler` works.
- **No magic.** No globals, no `init()` registration, no reflection-based dependency injection. Generators write code you own.
- **Typed end to end.** Zod-style schemas (`z`) parse every input into Go types and check outgoing data; generic query builder, typed jobs and events, named routes checked at boot.
- **Batteries included, opt-in.** SQLite out of the box; PostgreSQL, Redis and MongoDB as separate driver modules, so you only download what you use.
- **APIs first-class.** `lumek new shop --api` gives a JSON-only app with token auth, pagination, CORS, rate limiting and RFC 9457 errors.
- **Secure by default.** CSRF, security headers, argon2id, safe SQL building, fail-closed authorization, mapped to the [OWASP Top 10:2025](docs/security.md).
- **No Node required** for the default HTMX starter.

## A taste

The API below is the design target ([docs/http.md](docs/http.md)), not released code yet.

```go
func Web(r *router.Router, app *lumek.App) {
	posts := controllers.NewPost(app)

	r.Get("/", home.Index).Name("home")
	r.Resource("posts", posts) // posts.index, posts.store, posts.show…
}

func (p *PostController) Store(c *web.Ctx) error {
	in, err := web.Bind[requests.StorePost](c) // validates, 422 on failure
	if err != nil {
		return err
	}

	post := models.Post{Title: in.Title, Body: in.Body}
	if err := orm.Create(c.Context(), p.db, &post); err != nil {
		return err
	}

	c.Flash("success", "Post created.")
	return c.RedirectRoute("posts.show", "id", post.ID)
}
```

## Quick start (planned for v0.1.0)

```sh
go install github.com/AlexGalhardo/golang-framework/cmd/lumek@latest

lumek new blog
cd blog
lumek serve          # http://localhost:3000
```

Requires Go 1.27 or newer. Works on **Windows 11/10**, Linux and macOS — no C compiler needed.

## Documentation

| Topic | Read |
| --- | --- |
| Roadmap and current task | [PLAN.md](PLAN.md) |
| Architecture | [docs/architecture.md](docs/architecture.md) |
| Configuration | [docs/configuration.md](docs/configuration.md) |
| Routing, validation, sessions, errors | [docs/http.md](docs/http.md) |
| Typed schemas (Zod-style validation) | [docs/validation.md](docs/validation.md) |
| REST APIs | [docs/api.md](docs/api.md) |
| Security defaults (OWASP Top 10:2025) | [docs/security.md](docs/security.md) |
| Databases, ORM, migrations | [docs/database.md](docs/database.md) |
| Authentication and authorization | [docs/authentication.md](docs/authentication.md) |
| Views, HTMX, Inertia, Vite | [docs/frontend.md](docs/frontend.md) |
| CLI | [docs/cli.md](docs/cli.md) |
| Mail, cache, queue and more | [docs/subsystems.md](docs/subsystems.md) |
| Testing | [docs/testing.md](docs/testing.md) |
| Tooling and releases | [docs/tooling.md](docs/tooling.md) |
| Design decisions | [docs/brainstorming.md](docs/brainstorming.md) |
| Research behind the design | [docs/golang-web-search.md](docs/golang-web-search.md) |

## Contributing

Contributions are welcome. Read [CONTRIBUTING.md](CONTRIBUTING.md) first; security issues go through [SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE) © 2026 Alex Galhardo
