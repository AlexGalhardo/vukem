# Architecture

How Vukem is put together: modules, packages, application lifecycle, request lifecycle, and the rules that keep it idiomatic Go. Decisions referenced as `A1`, `D1`… come from [brainstorming.md](brainstorming.md).

## Principles

1. **`net/http` first.** Every handler and middleware interoperates with `http.Handler`.
2. **Explicit over magic.** No package-level mutable state, no `init()` registration, no reflection-based DI or discovery.
3. **Typed over stringly.** Config structs, generic jobs/events/policies, named routes checked at boot.
4. **Codegen over reflection.** Generators write plain code the user owns and edit `bootstrap/*.go` registries.
5. **Stdlib over dependency.** A third-party module is added only when no simple stdlib path exists and the module is well established (see [Dependency policy](#dependency-policy)).
6. **Small core, drivers as modules.** Importing the core never pulls a heavy driver.

## Repository layout

```text
vukem/                            module github.com/AlexGalhardo/vukem
├── app.go                        package vukem: App, Provider, builder
├── router/                       ServeMux wrapper, groups, names, resources, URLFor
├── web/                          *web.Ctx: request/response helpers, Parse/Bind[T], Render
├── middleware/                   built-in middleware (recover, logger, request id, timeout, cors…)
├── errs/                         HTTPError, ValidationError, central handler, RFC 9457
├── config/                       .env loader, Secret, typed config helpers
├── z/                            Zod-style typed schemas: parse input, check output, JSON Schema/TS export
├── session/                      session manager, cookie/database/memory stores, flash
├── view/                         View interface, html/template engine, fragments
├── htmx/                         htmx v4 request/response helpers
├── api/                          REST helpers: resources, query conventions, pagination, typed handlers, idempotency
├── orm/                          generic query builder, dialects (sqlite, postgres)
├── schema/                       migration schema builder + runner
├── database/                     connection management, SQLite defaults, transactions
├── auth/                         guards, user provider, hashers, tokens        (ALPHA)
├── authz/                        gates, Policy[T]                               (ALPHA)
├── social/                       OAuth2/OIDC social login (Ally-style)           (ALPHA)
├── mail/ cache/ storage/ event/                                                 (ALPHA)
├── openapi/ apidocs/             OpenAPI 3.1 generator, TS/Zod codegen, Scalar API reference
├── queue/ schedule/ ratelimit/   typed jobs (sync/memory/database drivers), cron scheduler, GCRA limiter
├── vite/                         Vite tags: hot file (dev), manifest, CSP nonce, SRI
├── inertia/                      Inertia.js v3 adapter: page object, version check, redirects, shared props
├── sse/                          server-sent events: streams, heartbeat, hub (memory or Redis)
├── crypt/                        AES-256-GCM encryption, key rotation, encrypted cookies
├── i18n/                         JSON catalogs, CLDR plurals, locale middleware, translated validation
├── testing/                      fluent HTTP test client, DB helpers, fakes
├── cmd/vukem/                 global CLI
├── internal/
│   ├── stubs/                    embedded generator templates
│   ├── codemod/                  go/ast edits for bootstrap/*.go
│   └── tools/commitlint/         Conventional Commits validator
├── drivers/
│   ├── postgres/                 module …/drivers/postgres (pgx)
│   ├── redis/                    module …/drivers/redis (go-redis)
│   ├── mongo/                    module …/drivers/mongo (mongo-driver v2)
│   ├── otel/                     module …/drivers/otel (OpenTelemetry tracing)
│   ├── s3/                       module …/drivers/s3 (stdlib SigV4 client, no AWS SDK)
│   └── websocket/                module …/drivers/websocket (coder/websocket)
├── contrib/
│   └── stripe/                   module …/contrib/stripe (Stripe API + webhooks, stdlib)
├── starters/                     app skeletons embedded by `vukem new`
├── docs/
└── go.work                       local development only (CI uses GOWORK=off)
```

Package names are short, lowercase, singular and never `util`/`common`/`helpers`. The request context package is named `web` (not `ctx`) so it never shadows the conventional `ctx context.Context` variable.

## Module boundaries

| Rule | Enforced by |
| --- | --- |
| Core never imports `drivers/*` | `depguard` linter + `drivers.md` agent rule |
| Drivers import core, never each other | `depguard` |
| Core third-party deps limited to the allowlist | `depguard` + review |
| Published `go.mod` files contain no `replace` | CI check |

### Dependency policy

| Module | Where | Why |
| --- | --- | --- |
| `modernc.org/sqlite` | core | Pure-Go SQLite default (no cgo) |
| `golang.org/x/crypto` | core | argon2id, bcrypt |
| `github.com/google/go-cmp` | tests only | Diffs in assertions |
| `github.com/jackc/pgx/v5` | `drivers/postgres` | Postgres |
| `github.com/redis/go-redis/v9` | `drivers/redis` | Redis |
| `go.mongodb.org/mongo-driver/v2` | `drivers/mongo` | MongoDB |
| `go.opentelemetry.io/otel` (+ `sdk`, `exporters/otlp/otlptrace/otlptracehttp`) | `drivers/otel` | Tracing |
| `github.com/coder/websocket` | `drivers/websocket` | WebSockets |

`drivers/s3` adds no dependency: it signs requests with its own Signature Version 4 implementation, checked against the AWS test suite.

Anything else requires a brainstorming entry explaining why the stdlib is insufficient.

## The `App`

```go
type App struct {
	Config  *config.App
	Log     *slog.Logger
	Router  *router.Router
	DB      *database.Manager
	Session *session.Manager
	Views   view.Engine
	// ALPHA+: Auth, Gate, Mail, Cache, Storage, Events; BETA+: Queue, Scheduler…
}
```

- Fields are concrete, typed services. There is no `Make("cache")`.
- Optional services are `nil` until their provider registers them; accessing one without its provider fails at **boot**, not at request time.
- Controllers receive the `*App` (or narrower interfaces) through their constructors.

### Providers and lifecycle

```go
type Provider interface {
	Register(app *App) error            // bind services, no I/O
}

// Optional hooks, detected by interface assertion:
type Booter interface   { Boot(ctx context.Context, app *App) error }     // may use other services
type Starter interface  { Start(ctx context.Context, app *App) error }    // long-running runners
type Shutdowner interface { Shutdown(ctx context.Context, app *App) error } // reverse order
```

Lifecycle (inspired by AdonisJS v7 and Goravel runners):

1. **Load config** — `.env` files → typed structs → validate. Abort on error.
2. **Register** — every provider, in declared order.
3. **Boot** — every provider, in declared order.
4. **Start** — runners (HTTP server, queue worker, scheduler) started under one `errgroup` bound to a `signal.NotifyContext`.
5. **Shutdown** — on signal: `http.Server.Shutdown`, drain runners, then providers' `Shutdown` in **reverse** order, each under a timeout.

Each provider can be limited to environments: `web`, `console`, `test`.

### Bootstrap (user app)

Generated by `vukem new`; everything the app uses is listed in code:

```go
// bootstrap/app.go
func New(opts ...vukem.Option) (*vukem.App, error) {
	app, err := vukem.New(append([]vukem.Option{
		vukem.WithProviders(Providers()...),      // vukem make:provider appends here
		vukem.WithMigrations(migrations.All()),   // vukem make:migration appends here
		vukem.WithViews(resources.Views()),
		vukem.WithRoutes(routes.Web),             // func(r *router.Router, app *vukem.App) error
	}, opts...)...)
	if err != nil {
		return nil, err
	}

	app.AddCommand(Commands()...)                 // vukem make:command appends here

	return app, nil
}
```

```go
// cmd/app/main.go
func main() { os.Exit(vukem.Main(bootstrap.New)) }
```

`vukem.Main` loads `.env` and the process environment, handles signals and runs `serve` or a command. Its testable body is `vukem.RunMain(ctx, args, getenv, build, opts...)`; tests use `vukemtest.New(t, bootstrap.New)`.

## Request lifecycle

```text
net/http server
  └─ server middleware stack      (always: recover, request id, logger, static, cors)
      └─ ServeMux match
          ├─ no match → 404 via error handler
          └─ router middleware stack  (matched routes: session, csrf, auth init)
              └─ group / route middleware (named, with options)
                  └─ handler func(c *Ctx) error
                      └─ error? → errs.Handler → HTML | redirect+flash | 422 fragment | problem+json
```

Three middleware stacks (AdonisJS model): **server**, **router**, **named**. All have the signature `func(http.Handler) http.Handler`.

## Concurrency and context

- `context.Context` is the first parameter of every I/O function; never stored in structs.
- Request-scoped values (user, session, request id) live in the request context under unexported keys and are exposed through typed accessors on `*web.Ctx`.
- Work that must outlive the request uses `context.WithoutCancel` and is tracked by the app so shutdown can drain it.
- The `http.Server` always sets `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, `IdleTimeout`.

## Error model

See [http.md](http.md#errors). Summary: handlers return `error`; sentinel errors for conditions, typed errors for data; `errors.Is`/`errors.AsType`; the central handler negotiates the response format.

## Platform support

Windows is a first-class platform (maintainer requirement, 2026-10-16), with **Windows 11** as the primary Windows target.

| OS | Support | Verified by |
| --- | --- | --- |
| Windows 11 (amd64, arm64) | first-class | local development machine + `windows-latest` CI job + e2e (`vukem new` → build → serve) |
| Windows 10 22H2 | supported while Microsoft/Go support it | same suite |
| Linux (amd64, arm64) | first-class | Docker `-race` job (`task test:linux`), CI |
| macOS (arm64, amd64) | first-class | CI |

Rules that keep Windows working:

- Paths: `path/filepath` for OS paths, `path` only for URLs and `fs.FS` names; never hard-code `/` or `\`; generated projects use `filepath.Join`.
- Files: close handles before deleting (Windows refuses to remove open files — `os.Root` and SQLite connections must be closed on shutdown and in tests via `t.Cleanup`); no reliance on Unix permissions.
- Line endings: generators and stubs write LF; parsers (`.env`, commit messages, templates) accept CRLF.
- Processes and signals: shutdown listens for `os.Interrupt` (Ctrl+C) and `SIGTERM`; the CLI never shells out through `sh`/`bash` — it runs binaries with `os/exec` and the `go` toolchain.
- No cgo by default (`modernc.org/sqlite`), so `go install` works on Windows without a C compiler.
- Tooling works in PowerShell and Git Bash: Taskfile (mvdan/sh), lefthook, winget-installable tools.

## API evolution

- Pre-1.0: breaking changes allowed in minor releases, always listed under **Changed**/**Removed** in the changelog.
- Deprecated functions carry `// Deprecated:` plus `//go:fix inline` when a mechanical replacement exists, so users' `go fix ./...` migrates call sites.
- Post-1.0: no breaking changes without a new major (`/v2` module path).
