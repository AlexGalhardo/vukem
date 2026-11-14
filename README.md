
<div align="center">

<a href="https://vukem.dev/" target="_blank" rel="noopener noreferrer">
  <img src="site/src/assets/logo.svg" alt="Vukem gopher" width="400" height="250">
</a>

<br/>

<a href="https://github.com/AlexGalhardo/vukem/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/AlexGalhardo/vukem/ci.yml?branch=main&label=tests" alt="Tests"></a>
<a href="https://app.codecov.io/gh/AlexGalhardo/vukem"><img src="https://codecov.io/gh/AlexGalhardo/vukem/graph/badge.svg" alt="Codecov"></a>
<a href="https://github.com/AlexGalhardo/vukem/actions/workflows/release.yml"><img src="https://img.shields.io/github/actions/workflow/status/AlexGalhardo/vukem/release.yml?label=ci%2Fcd" alt="CI/CD"></a>
<a href="https://github.com/AlexGalhardo/vukem/releases"><img src="https://img.shields.io/github/v/release/AlexGalhardo/vukem?include_prereleases&label=version" alt="Version"></a>
<a href="go.mod"><img src="https://img.shields.io/badge/go-1.27.4-00ADD8?logo=go&logoColor=white" alt="Go 1.27.4"></a>
<a href="LICENSE"><img src="https://img.shields.io/github/license/AlexGalhardo/vukem" alt="License"></a>
<a href="https://scorecard.dev/viewer/?uri=github.com/AlexGalhardo/vukem"><img src="https://api.scorecard.dev/projects/github.com/AlexGalhardo/vukem/badge" alt="OpenSSF Scorecard"></a>

### Vukem is a complete Model-View-Controller (MVC) web framework for Go built on `net/http` and the standard library to build APIs and FullStack Web Applications.

<br/>

</div>

<br/>

| Area | What you get |
| --- | --- |
| **World-Class DX Docs Website** | <ul><li><a href="https://vukem.dev">https://vukem.dev</a></li></ul> |
| **HTTP** | <ul><li>`net/http`-native router with named routes, resources, groups and host-only groups (`Group.Only`)</li><li>Middleware is `func(http.Handler) http.Handler` and its default order is fixed by a test</li><li>Request-log attributes (`web.AddLogAttrs`, matched route)</li><li>`route:list` with method, path and name filters</li><li>HTTP/3 (`contrib/http3`)</li></ul> |
| **Validation** | <ul><li>Zod-style typed schemas (`z`) for every input, including files</li><li>Generic `Slice`/`Map`</li><li>Unions and discriminated unions</li><li>Literals and tuples</li><li>Recursive schemas (`Lazy`)</li><li>`Pick`/`Omit`/`Partial`/`Extend`</li><li>String formats, codecs and brands</li><li>Cross-field rules and issues with stable codes</li><li>Exportable as JSON Schema, TypeScript and Zod</li></ul> |
| **Database** | <ul><li>SQLite built in</li><li>PostgreSQL and MySQL/MariaDB driver modules</li><li>MongoDB</li><li>Experimental Turso driver (a local file synced with Turso Cloud)</li><li>SQL-first ORM with a generic query builder</li><li>Relations, joins, aggregates and subqueries</li><li>Upsert and typed raw SQL</li><li>`UpdateOnly` and `OrWhere*`</li><li>Dialect capabilities and conflict errors (409)</li><li>Typed column references (`codegen:models`)</li><li>Savepoints and optimistic locking (`orm:"version"`)</li><li>Encrypted columns, factories and seeders</li><li>Migrations with SQLite table rebuilds</li><li>`Column.Change`</li><li>Schema inspection (`schema.Inspect`)</li><li>`migrate:check`, `migrate:diff` and `db:pull`</li></ul> |
| **Auth** | <ul><li>Sessions</li><li>API tokens</li><li>JWT with JWKS and refresh tokens</li><li>TOTP two-factor (`install:auth --2fa`)</li><li>WebAuthn passkeys</li><li>Social login (Google, GitHub, Apple, LinkedIn, X and any OIDC, with HTTPS-only discovery)</li><li>Email verification</li><li>Password reset</li><li>Gates and policies</li></ul> |
| **APIs** | <ul><li>JSON resources and pagination</li><li>RFC 9457 errors (`ProblemDecorator`, `WithType`, `WithTitle`)</li><li>JSON-RPC 2.0 with OpenRPC</li><li>Rate limiting</li><li>Idempotency keys</li><li>Typed handlers</li><li>Errors mapped by type (`web.MapError`)</li><li>Default problem `type` from `APP_PROBLEM_BASE_URL`</li><li>OpenAPI 3.1 with constraints from the same `z` schemas that validate input</li><li>Scalar API reference</li><li>Generated Go clients</li><li>Generated TypeScript clients with methods resolving to `{ data, error, status }`</li><li>Errors typed by status</li><li>`-style throw` and `-tanstack` helpers as options</li></ul> |
| **GraphQL and gRPC** | <ul><li>Stdlib GraphQL server (`contrib/graphql`)</li><li>GraphQL subscriptions</li><li>Complexity limits</li><li>Masked internal errors</li><li>gRPC (`contrib/grpc`)</li><li>gRPC-Web</li><li>Connect on connect-go</li></ul> |
| **Real time** | <ul><li>Server-Sent Events (SSE)</li><li>WebSockets</li><li>Channel broadcasting</li><li>Private and presence channels</li><li>Shared Redis presence store</li><li>Conformance suites for brokers and presence stores</li></ul> |
| **Background work** | <ul><li>Typed queue jobs (sync, memory, database, Redis)</li><li>Queued mail</li><li>Queued listeners</li><li>Cron scheduler</li></ul> |
| **Frontend** | <ul><li>HTMX, templ or Inertia (React, Vue, Svelte) starters</li><li>Vite and Tailwind CSS v4</li><li>Server-side rendering (SSR)</li><li>Typed Inertia pages (`inertia.Define`)</li><li>`codegen:pages`</li><li>Typed views checked by `view:check` (fields, named routes, `hx-*` targets)</li><li>Youch-style error page showing what production clients receive</li></ul> |
| **CLI** | <ul><li>`vukem new`</li><li>Generators</li><li>`upgrade`</li><li>`stub:publish`</li><li>App `db` console</li><li>`doctor`</li><li>`codegen` regenerates routes, API clients and page types</li><li>`vukem serve` runs codegen on every rebuild</li><li>`-check` fails in CI when generated code is stale</li><li>Generated configuration reference</li><li>"Did you mean" suggestions</li><li>`vukem bench` for URL or Go scenario load tests</li><li>Benchmark thresholds and baselines</li><li>Live read-only benchmark dashboard</li></ul> |
| **Admin and devtools** | <ul><li>HTMX admin panel generated from models</li><li>Audit log</li><li>Opt-in hardening (`DenyStatus`, `TwoFactorRedirect`, `TrackIdle`)</li><li>`/_vukem` dashboard for requests, queries, jobs, mail and logs</li><li>N+1 query detection</li><li>`vukem studio` loopback-only browser for SQL, MongoDB and Redis data</li><li>Read-only console</li><li>Opt-in row editing (`--write`)</li></ul> |
| **Billing and uploads** | <ul><li>Stripe subscriptions (`install:billing`)</li><li>Streaming uploads</li><li>Resumable uploads (tus)</li><li>Direct-to-S3 uploads</li><li>Virus scanning</li></ul> |
| **Bot protection** | <ul><li>Cloudflare Turnstile on form routes</li><li>Step-up challenge with a signed clearance cookie (`challenge`)</li><li>Cloudflare helpers (`contrib/cloudflare`)</li><li>Real client IP</li><li>Origin lock</li><li>Cloudflare Access</li><li>Cache purge</li></ul> |
| **SEO and analytics** | <ul><li>Sitemaps</li><li>`robots.txt` (`robots`)</li><li>`NOINDEX` switch</li><li>`llms.txt` and `llms-full.txt` (`llmstxt`)</li><li>Static site generation with an atomic output swap (`ssg`)</li><li>Umami analytics</li><li>GA4 with Consent Mode v2</li><li>Search Console and Bing verification (`analytics`)</li><li>Bing Webmaster API (`bingwmt`)</li><li>IndexNow (`indexnow`)</li></ul> |
| **More** | <ul><li>Mail (SMTP, Resend, `MAIL_REPLY_TO`)</li><li>Cache with batched counters (`cache.Buffer`)</li><li>Feature flags</li><li>Storage (local, S3)</li><li>Events</li><li>Encryption</li><li>Internationalization (i18n)</li><li>OpenTelemetry</li><li>Telegram logging</li><li>CSV export</li><li>NDJSON export (`export.EncodeNDJSON`)</li><li>Image thumbnails</li><li>QR codes</li><li>Server-side request forgery (SSRF)-safe HTTP client</li><li>Throttle, retry and circuit-breaker transports</li><li>Signed URLs and tokens</li><li>Webhooks</li></ul> |

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

Thank you for considering contributing to Vukem! Read the [contribution guide](.github/CONTRIBUTING.md) to get started, and follow the [issue standard](docs/maintenance.md#issues) when you report a bug or propose a feature.

## Code of conduct

To keep the community welcoming to everyone, please review and follow the [Code of Conduct](.github/CODE_OF_CONDUCT.md).

## Security vulnerabilities

If you discover a security vulnerability, please follow the [security policy](.github/SECURITY.md) instead of opening a public issue.

## License

Vukem is open-source software licensed under the [MIT license](LICENSE).
