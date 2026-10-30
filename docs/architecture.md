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
├── app.go                        package vukem: Config, App, Option, New
├── options_*.go                  With* options by subsystem (routes, views, runtime); the rest sit beside their subsystem
├── validate_config.go            boot-time config checks, key derivation, logger setup
├── boot.go / serve.go            Boot and route registration; Run, Shutdown, HTTPServer
├── providers.go / instrument.go  Provider and Runner contracts; query, job and mail instrumentation
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
├── orm/                          generic query builder, dialects (sqlite, postgres, mysql)
├── schema/                       migration schema builder + runner
├── database/                     connection management, SQLite defaults, transactions
├── auth/                         guards, user provider, hashers, tokens        (ALPHA)
├── authz/                        gates, Policy[T]                               (ALPHA)
├── admin/                        admin panel from ORM models + z schemas, audit log (BETA)
├── social/                       OAuth2/OIDC social login (Ally-style)           (ALPHA)
├── mail/ cache/ storage/ event/                                                 (ALPHA)
├── openapi/ apidocs/             OpenAPI 3.1 generator, TS/Zod codegen, Scalar API reference
├── queue/ schedule/ ratelimit/   typed jobs (sync/memory/database drivers), cron scheduler, GCRA limiter
├── vite/                         Vite tags: hot file (dev), manifest, CSP nonce, SRI
├── inertia/                      Inertia.js v3 adapter: page object, version check, redirects, shared props
├── sse/                          server-sent events: streams, heartbeat, hub (memory or Redis)
├── export/                       streaming CSV/JSON/NDJSON downloads, safe file names, CSV formula guard
├── broadcast/                    channels (public/private/presence), authorization rules, SSE transport
├── crypt/                        AES-256-GCM encryption, key rotation, encrypted cookies
├── i18n/                         JSON catalogs, CLDR plurals, locale middleware, translated validation
├── devtools/                     /_vukem dev dashboard: requests, SQL (N+1), jobs, events, mail, logs
├── testing/                      fluent HTTP test client, DB helpers, fakes
├── cmd/vukem/                 global CLI
├── internal/
│   ├── stubs/                    embedded generator templates
│   ├── codemod/                  go/ast edits for bootstrap/*.go
│   └── tools/commitlint/         Conventional Commits validator
├── drivers/
│   ├── postgres/                 module …/drivers/postgres (pgx)
│   ├── mysql/                    module …/drivers/mysql (go-sql-driver/mysql; MySQL 8, MariaDB 11)
│   ├── redis/                    module …/drivers/redis (go-redis)
│   ├── mongo/                    module …/drivers/mongo (mongo-driver v2)
│   ├── otel/                     module …/drivers/otel (OpenTelemetry tracing)
│   ├── s3/                       module …/drivers/s3 (stdlib SigV4 client, no AWS SDK)
│   └── websocket/                module …/drivers/websocket (coder/websocket, broadcast protocol)
├── contrib/
│   ├── cloudflare/               module …/contrib/cloudflare (client IP ranges, origin lock, stdlib)
│   ├── graphql/                  module …/contrib/graphql (stdlib GraphQL server)
│   ├── grpc/                     module …/contrib/grpc (gRPC, gRPC-Web, Connect on connect-go)
│   ├── http3/                    module …/contrib/http3 (HTTP/3 over QUIC on quic-go)
│   └── stripe/                   module …/contrib/stripe (Stripe API + webhooks, stdlib)
├── starters/                     app skeletons embedded by `vukem new`
├── docs/
└── go.work                       local development only (CI uses GOWORK=off)
```

Package names are short, lowercase, singular and never `util`/`common`/`helpers`. The request context package is named `web` (not `ctx`) so it never shadows the conventional `ctx context.Context` variable.

## Module boundaries

| Rule | Enforced by |
| --- | --- |
| Core never imports `drivers/*` or `contrib/*` | `depguard` linter + `drivers.md` agent rule |
| `contrib/*` modules are optional features in their own module (like drivers), importing core only | `depguard` + review |
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
| `github.com/go-sql-driver/mysql` | `drivers/mysql` | MySQL 8 / MariaDB 11 |
| `github.com/redis/go-redis/v9` | `drivers/redis` | Redis |
| `go.mongodb.org/mongo-driver/v2` | `drivers/mongo` | MongoDB |
| `go.opentelemetry.io/otel` (+ `sdk`, `exporters/otlp/otlptrace/otlptracehttp`) | `drivers/otel` | Tracing |
| `github.com/coder/websocket` | `drivers/websocket` | WebSockets |
| none (stdlib only) | `contrib/cloudflare` | Cloudflare client IP ranges and origin lock |
| none (stdlib only) | `contrib/graphql` | GraphQL server; gqlgen/graphql-go are not used |
| `connectrpc.com/connect` (+ `grpchealth`, `grpcreflect`), `google.golang.org/protobuf`, `google.golang.org/genproto/googleapis/rpc` | `contrib/grpc` | gRPC, gRPC-Web and Connect on `net/http`; health, reflection, `google.rpc.BadRequest` details |
| `github.com/quic-go/quic-go` `v0.64.1` | `contrib/http3` | HTTP/3 and QUIC: the stdlib has no HTTP/3 server and `golang.org/x/net/quic` is not yet covered by the Go security policy (checked 2026-10-29); owner approval in VF-057 |
| `golang.org/x/image` | `contrib/imaging` | WebP decoding and high-quality scaling (`draw.CatmullRom`) for thumbnails; the stdlib has neither. Neither has a WebP encoder, so thumbnails are JPEG or PNG (byte-identical for one Go toolchain, `TestThumbnailIsDeterministic`) |
| `github.com/go-chi/chi/v5`, `github.com/labstack/echo/v5` | `internal/bench` (bench only) | Comparative benchmarks ([performance.md](performance.md)); never imported by core or drivers |

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
- `web.Services`, the bag that travels in the request context, is not a service locator in the making: a field enters `Services` only if it is read by more than one package. Every other dependency goes through the constructor of the package that needs it.

### Values and pointers

- Descriptions are immutable values: schemas (`z`), queries (`orm.Query[T]`), factories (`factory.Factory[T]`) and configs. Every method returns a copy, so a shared description is never mutated by a caller.
- Registrars and resources are pointers: the router and its groups, the `App`, stores, clients and pools. They carry mutable state or own a resource, so copying them would be a bug.
- `Transform`, `Refine` and `Preprocess` callbacks are pure functions and do no I/O; a `Refine` that queries the database is the documented exception.

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
2. **Register** — every provider, in declared order; then the `WithRoutes` functions (still inside `New`).
3. **Boot** — every provider, in declared order; then the `WithRoutesAfterBoot` functions, for routes that need clients opened in Boot. Warmup (`codegen:*`) never boots and runs them at the end of `New`.
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
                      └─ error? → web.DefaultErrorHandler → HTML | redirect+flash | 422 fragment | problem+json
```

Three middleware stacks (AdonisJS model): **server**, **router**, **named**. All have the signature `func(http.Handler) http.Handler`.

## Concurrency and context

- `context.Context` is the first parameter of every I/O function; never stored in structs.
- Request-scoped values (user, session, request id) live in the request context under unexported keys and are exposed through typed accessors on `*web.Ctx`.
- Work that must outlive the request uses `context.WithoutCancel` and is tracked by the app so shutdown can drain it.
- The `http.Server` always sets `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, `IdleTimeout`.

### Sizing work to the container's CPU quota

Go 1.25 and later derive `GOMAXPROCS` from the cgroup CPU quota, so a Go worker pool scaled from `runtime.GOMAXPROCS(0)` is already right in a container. It is the **wrong** number for a pool outside the Go runtime — ffmpeg's `-threads`, SVT-AV1's `lp`, `VIPS_CONCURRENCY` — because it rounds the quota up and never drops below 2. Measured on Go 1.27.2 under Docker on a 16-core host:

| `--cpus` | `GOMAXPROCS` | `cpu.Limit()` | `cpu.Count()` |
| --- | --- | --- | --- |
| 0.5 | 2 | 0.5 | 1 |
| 1.5 | 2 | 1.5 | 1 |
| 2 | 2 | 2 | 2 |
| 4.5 | 5 | 4.5 | 4 |

`cpu.Count()` is the quota rounded **down**, at least 1, or `runtime.NumCPU()` when there is no quota; `cpu.Limit()` gives the fraction. Rounding down is deliberate: a thread past the quota is throttled rather than faster, and it still counts against `RLIMIT_NPROC` — an encoder that cannot start its threads can exit having written nothing, with a message naming no cause. Only Linux has cgroups; elsewhere `Limit` reports no quota and `Count` falls back to the machine's CPUs.

## Error model

See [http.md](http.md#errors). Summary: handlers return `error`; sentinel errors for conditions, typed errors for data; `errors.Is`/`errors.AsType`; the central handler negotiates the response format.

### When to panic

A panic is a programming error found at boot, never a way to report a bad request.

| Where | Panic? | Examples |
| --- | --- | --- |
| Construction and registration during boot (schemas, schedules, keys) | Yes, with a message naming the fix, so the mistake stops the process before it serves | `z` unknown tag rules, `signed.New` / `social.New` and their `WithPreviousKeys` with a short key, a bad `schedule` cron expression |
| `Must*` helpers for constants | Yes, documented as "for constants" | `uuid.MustParse` |
| A request path (handlers, middleware, anything fed by request data) | No: return an error (`errs.*`, a sentinel, a `*errs.ValidationError`) | `crypt.Open` returns `ErrNoKey` |
| A method with no error to return on a missing boot dependency | Only when boot already refuses the misconfiguration in production; document it and check for nil where the dependency may be absent | `crypt.Seal` / `Encrypt` on a nil `Encrypter` (no `APP_KEY` outside production) |

`middleware.Recover` turns an unexpected panic into a 500, but that is a safety net, not an error path.

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
