<p align="center">
  <a href="https://vukem.vercel.app"><img src=".github/logo-wordmark.svg" width="320" alt="Vukem"></a>
</p>

<p align="center">
  <a href="https://github.com/AlexGalhardo/vukem/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/AlexGalhardo/vukem/ci.yml?branch=main&label=tests" alt="Tests"></a>
  <a href="https://github.com/AlexGalhardo/vukem/actions/workflows/release.yml"><img src="https://img.shields.io/github/actions/workflow/status/AlexGalhardo/vukem/release.yml?label=ci%2Fcd" alt="CI/CD"></a>
  <a href="https://github.com/AlexGalhardo/vukem/releases"><img src="https://img.shields.io/github/v/release/AlexGalhardo/vukem?include_prereleases&label=version" alt="Version"></a>
  <a href="https://pkg.go.dev/github.com/AlexGalhardo/vukem"><img src="https://pkg.go.dev/badge/github.com/AlexGalhardo/vukem.svg" alt="Go Reference"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/AlexGalhardo/vukem" alt="License"></a>
</p>

## Introduction

Vukem is a web framework for Go with the productivity of Laravel and AdonisJS, built on `net/http` and the standard library. It gives you routing, validation, an ORM with migrations, authentication, queues, a scheduler, mail, OpenAPI documents and one CLI to generate and run it all, for **REST APIs and full-stack apps** alike.

> [!WARNING]
> Vukem is in active development (`v0.x`, heading to the 1.0 beta): it works end to end, but APIs may still change. Follow progress in [PLAN.md](PLAN.md).

## Summary

- **HTTP**: `net/http`-native router with named routes, resources and groups; middleware is `func(http.Handler) http.Handler`.
- **Validation**: Zod-style typed schemas (`z`) for every input, exportable as JSON Schema, TypeScript and Zod.
- **Database**: SQLite built in, PostgreSQL driver module, generic query builder, relations, migrations, factories and seeders.
- **Auth**: sessions, API tokens, social login (OAuth2/OIDC), email verification, password reset, gates and policies.
- **APIs**: JSON resources, pagination, RFC 9457 errors, rate limiting, idempotency keys, typed handlers, OpenAPI 3.1 and a Scalar API reference.
- **Background work**: typed queue jobs (sync, memory, database), queued mail and listeners, cron scheduler.
- **Frontend**: HTMX-first server-rendered views, with Inertia + React on the way.
- **More**: mail (SMTP, Resend), cache, storage, events, Telegram logging, SSRF-safe HTTP client, signed URLs and webhooks.

Full documentation: **[vukem.vercel.app](https://vukem.vercel.app/docs/introduction)** · design docs in [docs/](docs/README.md).

## Why Vukem

- **Standard library first.** Handlers are plain `net/http`; any `http.Handler` works.
- **No magic.** No globals, no `init()` registration, no reflection-based dependency injection. Generators write code you own.
- **Typed end to end.** Inputs parse into Go types, outputs are checked, and the same types produce your OpenAPI and TypeScript.
- **Opt-in batteries.** Heavy drivers (PostgreSQL, Redis, MongoDB) are separate modules, so you only download what you use.
- **Secure by default.** CSRF, security headers, Argon2id, safe SQL, fail-closed authorization, mapped to the [OWASP Top 10:2025](docs/security.md).
- **Windows is first-class.** Pure Go, no C compiler, tested on Windows, Linux and macOS.

## A taste

```sh
go install github.com/AlexGalhardo/vukem/cmd/vukem@latest

vukem new blog && cd blog   # HTMX + SQLite starter (or: vukem new shop --api)
vukem migrate
vukem serve                 # http://localhost:3000, rebuilds on changes
```

```go
type StoreProduct struct {
	Name  string  `json:"name"  validate:"required,max=80"`
	Price float64 `json:"price" validate:"min=0"`
}

func Routes(r *router.Router, app *vukem.App) error {
	vukem.ServeOpenAPI(app, r, "/openapi.json")

	api.Register(r, "POST /products", api.Op{Name: "products.store", Tags: []string{"products"}},
		func(c *web.Ctx, in StoreProduct) (Product, error) {
			p := Product{Name: in.Name, Price: in.Price}
			return p, orm.Create(c.Context(), db, &p) // 201 JSON; invalid input is a 422 problem
		})

	return nil
}
```

## Agentic development

Vukem is built to be worked on with coding agents. [AGENTS.md](AGENTS.md) (and [CLAUDE.md](CLAUDE.md)) give the rules and where things live; [PLAN.md](PLAN.md) is the task list, one verifiable task at a time; `.claude/rules/` holds path-scoped rules and `.claude/skills/` the Go and workflow skills. Every change goes through tests first, `golangci-lint`, Conventional Commits and the GitHub Actions gate.

## Contributing

Thank you for considering contributing to Vukem! Read the [contribution guide](CONTRIBUTING.md) to get started.

## Code of conduct

To keep the community welcoming to everyone, please review and follow the [Code of Conduct](CODE_OF_CONDUCT.md).

## Security vulnerabilities

If you discover a security vulnerability, please follow the [security policy](SECURITY.md) instead of opening a public issue.

## License

Vukem is open-source software licensed under the [MIT license](LICENSE).
