<p align="center">
  <a href="https://vukem.vercel.app"><img src=".github/logo-wordmark.svg" width="320" alt="Vukem"></a>
</p>

<p align="center">
  <a href="https://github.com/AlexGalhardo/vukem/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/AlexGalhardo/vukem/ci.yml?branch=main&label=tests" alt="Tests"></a>
  <a href="https://github.com/AlexGalhardo/vukem/actions/workflows/release.yml"><img src="https://img.shields.io/github/actions/workflow/status/AlexGalhardo/vukem/release.yml?label=ci%2Fcd" alt="CI/CD"></a>
  <a href="https://github.com/AlexGalhardo/vukem/releases"><img src="https://img.shields.io/github/v/release/AlexGalhardo/vukem?include_prereleases&label=version" alt="Version"></a>
  <a href="https://pkg.go.dev/github.com/AlexGalhardo/vukem"><img src="https://pkg.go.dev/badge/github.com/AlexGalhardo/vukem.svg" alt="Go Reference"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/AlexGalhardo/vukem" alt="License"></a>
  <a href="https://scorecard.dev/viewer/?uri=github.com/AlexGalhardo/vukem"><img src="https://api.scorecard.dev/projects/github.com/AlexGalhardo/vukem/badge" alt="OpenSSF Scorecard"></a>
</p>

## Introduction

Vukem is a complete Model-View-Controller (MVC) web framework for Go built on `net/http` and the `standard library` to build APIs and FullStack Web Applications.

## Summary

- **HTTP**: `net/http`-native router with named routes, resources and groups; middleware is `func(http.Handler) http.Handler`.
- **Validation**: Zod-style typed schemas (`z`) for every input, including files, exportable as JSON Schema, TypeScript and Zod.
- **Database**: SQLite built in, PostgreSQL and MySQL/MariaDB driver modules, MongoDB, generic query builder, relations, encrypted columns, migrations, factories and seeders.
- **Auth**: sessions, API tokens, JWT with JWKS and refresh tokens, TOTP two-factor (`install:auth --2fa`), social login (Google, GitHub, Apple, LinkedIn, X and any OIDC), email verification, password reset, gates and policies.
- **APIs**: JSON resources, pagination, RFC 9457 errors, rate limiting, idempotency keys, typed handlers, OpenAPI 3.1, a Scalar API reference and generated TypeScript and Go clients.
- **GraphQL and gRPC**: a stdlib GraphQL server with subscriptions (`contrib/graphql`) and gRPC, gRPC-Web and Connect on connect-go (`contrib/grpc`).
- **Real time**: Server-Sent Events, WebSockets and channel broadcasting with private and presence channels.
- **Background work**: typed queue jobs (sync, memory, database, Redis), queued mail and listeners, cron scheduler.
- **Frontend**: HTMX, templ or Inertia (React, Vue, Svelte) starters with Vite and Tailwind CSS v4, SSR and a youch-style error page.
- **Admin and devtools**: an HTMX admin panel generated from your models, with an audit log, and a `/_vukem` dashboard for requests, queries (with N+1 detection), jobs, mail and logs.
- **Billing and uploads**: Stripe subscriptions (`install:billing`), streaming, resumable (tus) and direct-to-S3 uploads with virus scanning.
- **More**: mail (SMTP, Resend), cache, storage (local, S3), events, encryption, i18n, OpenTelemetry, Telegram logging, SSRF-safe HTTP client, signed URLs and webhooks.

## Why Vukem

- **Standard library first.** Handlers are plain `net/http`; any `http.Handler` works.
- **No magic.** No globals, no `init()` registration, no reflection-based dependency injection. Generators write code you own.
- **Typed end to end.** Inputs parse into Go types, outputs are checked, and the same types produce your OpenAPI and TypeScript.
- **Opt-in batteries.** Heavy drivers (PostgreSQL, MySQL, Redis, MongoDB, S3, WebSockets, OpenTelemetry) and integrations (GraphQL, gRPC, Stripe) are separate modules, so you only download what you use.
- **Secure by default.** CSRF, security headers, Argon2id, safe SQL, fail-closed authorization, mapped to the [OWASP Top 10:2025](docs/security.md).
- **Windows is first-class.** Pure Go, no C compiler, tested on Windows, Linux and macOS.

## Showcase

Open-source reference apps built with Vukem, live in production ([details](https://vukem.vercel.app/docs/showcase)):

- [vukem-golang-quiz-htmx](https://github.com/AlexGalhardo/vukem-golang-quiz-htmx) ([live](https://vukem-quiz.up.railway.app)): HTMX 4, embedded JSON, i18n, CSP nonces, Lighthouse 100.
- [vukem-rifa-vuejs](https://github.com/AlexGalhardo/vukem-rifa-vuejs) ([live](https://vukem-rifa.up.railway.app)): Inertia v3 + Vue, PostgreSQL, PIX with AbacatePay, a ledger, a REST API with Scalar, scheduler and queue inside `serve`.
- [vukem-micro-mercadolivre-reactjs](https://github.com/AlexGalhardo/vukem-micro-mercadolivre-reactjs) ([live](https://vukem-marketplace.up.railway.app)): Inertia v3 + React + Base UI, PostgreSQL, Redis and MongoDB, Stripe Connect and PIX, REST, GraphQL and gRPC, OpenTelemetry.

## Why MVC and not Hexagonal/Clean Architecture?

- This framework was inspired by the simplicity of well established frameworks like: Laravel, Ruby on Rails, AdonisJS, Django.
- Hexagonal and Clean Architecture are better developed without framework's attachment in Go ecosystem.

## Agentic development

Vukem's predictable structure and conventions (generators, typed schemas, named routes, no magic) make it a natural fit for AI coding agents like Claude Code, Cursor and GitHub Copilot. Install **Vukem Boost** in your project to supercharge your AI workflow:

```sh
vukem boost:install
```

Boost adds Vukem guidelines to `AGENTS.md`, framework skills to `.claude/skills/` and the `vukem-boost` MCP server to `.mcp.json` (plus `.cursor/` and `.vscode/` when present). The server gives your agent tools to inspect the running app: application info, routes, commands, the database schema, read-only SQL queries, absolute URLs for named routes and a documentation search.

## Stability

Vukem 1.x keeps the Go 1 compatibility promise: no breaking API changes until v2, deprecations kept for at least two minors, the two latest Go releases supported. Read the [stability policy](docs/stability.md) and the [upgrade guides](docs/upgrade/README.md).

## Contributing

Thank you for considering contributing to Vukem! Read the [contribution guide](CONTRIBUTING.md) to get started.

## Code of conduct

To keep the community welcoming to everyone, please review and follow the [Code of Conduct](CODE_OF_CONDUCT.md).

## Security vulnerabilities

If you discover a security vulnerability, please follow the [security policy](SECURITY.md) instead of opening a public issue.

## License

Vukem is open-source software licensed under the [MIT license](LICENSE).
