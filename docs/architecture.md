# Architecture

How Lumek is put together: modules, packages, application lifecycle, request lifecycle, and the rules that keep it idiomatic Go. Decisions referenced as `A1`, `D1`… come from [brainstorming.md](brainstorming.md).

## Principles

1. **`net/http` first.** Every handler and middleware interoperates with `http.Handler`.
2. **Explicit over magic.** No package-level mutable state, no `init()` registration, no reflection-based DI or discovery.
3. **Typed over stringly.** Config structs, generic jobs/events/policies, named routes checked at boot.
4. **Codegen over reflection.** Generators write plain code the user owns and edit `bootstrap/*.go` registries.
5. **Stdlib over dependency.** A third-party module is added only when no simple stdlib path exists and the module is well established (see [Dependency policy](#dependency-policy)).
6. **Small core, drivers as modules.** Importing the core never pulls a heavy driver.

## Repository layout

```text
golang-framework/                 module github.com/AlexGalhardo/golang-framework
├── app.go                        package lumek: App, Provider, builder
├── router/                       ServeMux wrapper, groups, names, resources, URLFor
├── web/                          *web.Ctx: request/response helpers, Parse/Bind[T], Render
├── middleware/                   built-in middleware (recover, logger, request id, timeout, cors…)
├── errs/                         HTTPError, ValidationError, central handler, RFC 9457
├── config/                       .env loader, Secret, typed config helpers
├── z/                            Zod-style typed schemas: parse input, check output, JSON Schema/TS export
├── session/                      session manager, cookie/database/memory stores, flash
├── view/                         View interface, html/template engine, fragments
├── htmx/                         htmx v4 request/response helpers
├── api/                          REST helpers: JSONOnly, resources, query conventions, pagination, OpenAPI (BETA)
├── orm/                          generic query builder, dialects (sqlite, postgres)
├── schema/                       migration schema builder + runner
├── database/                     connection management, SQLite defaults, transactions
├── auth/                         guards, user provider, hashers, tokens        (ALPHA)
├── authz/                        gates, Policy[T]                               (ALPHA)
├── social/                       OAuth2/OIDC social login (Ally-style)           (ALPHA)
├── mail/ cache/ storage/ event/                                                 (ALPHA)
├── queue/ schedule/ inertia/ vite/ i18n/ sse/ crypt/ ratelimit/                (BETA)
├── testing/                      fluent HTTP test client, DB helpers, fakes
├── cmd/lumek/                 global CLI
├── internal/
│   ├── stubs/                    embedded generator templates
│   ├── codemod/                  go/ast edits for bootstrap/*.go
│   └── tools/commitlint/         Conventional Commits validator
├── drivers/
│   ├── postgres/                 module …/drivers/postgres (pgx)
│   ├── redis/                    module …/drivers/redis (go-redis)
│   └── mongo/                    module …/drivers/mongo (mongo-driver v2)
├── starters/                     app skeletons embedded by `lumek new`
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

```go
// bootstrap/app.go
func New(getenv func(string) string) (*lumek.App, error) {
	return lumek.New(
		lumek.WithEnv(getenv),
		lumek.WithProviders(Providers()...),
		lumek.WithRoutes(routes.Web),
		lumek.WithMiddleware(func(m *middleware.Stack) {
			m.Server(middleware.RequestID(), middleware.Recover())
			m.Router(session.Middleware(), csrf.Middleware())
		}),
		lumek.WithErrors(func(h *errs.Handler) { /* custom renderers */ }),
	)
}
```

```go
// cmd/app/main.go
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args, os.Getenv, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
```

`run` is testable end to end: tests pass a fake `getenv` and a buffer.

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

## API evolution

- Pre-1.0: breaking changes allowed in minor releases, always listed under **Changed**/**Removed** in the changelog.
- Deprecated functions carry `// Deprecated:` plus `//go:fix inline` when a mechanical replacement exists, so users' `go fix ./...` migrates call sites.
- Post-1.0: no breaking changes without a new major (`/v2` module path).
