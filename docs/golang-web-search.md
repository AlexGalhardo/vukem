# Research: web frameworks and the Go ecosystem (Oct 2026)

Consolidation of the research that underpins Vukem's design. Each section ends with a **→ Suggested decision** — what to copy, what to adapt, what to avoid. Sources inline.

## Contents

1. [Version overview](#1-version-overview)
2. [Laravel 13](#2-laravel-13)
3. [AdonisJS v7](#3-adonisjs-v7)
4. [Goravel](#4-goravel)
5. [Other Go frameworks](#5-other-go-frameworks)
6. [Go community opinion](#6-go-community-opinion)
7. [Language and stdlib: what matters for a framework](#7-language-and-stdlib-what-matters-for-a-framework)
8. [Idiomatic patterns](#8-idiomatic-patterns)
9. [Database](#9-database)
10. [Testing](#10-testing)
11. [Security and authentication](#11-security-and-authentication)
12. [Frontend: HTMX v4, Inertia v3, Vite, templates](#12-frontend-htmx-v4-inertia-v3-vite-templates)
13. [Mongo, Redis, queues](#13-mongo-redis-queues)
14. [Tooling: lint, format, hooks, release](#14-tooling-lint-format-hooks-release)
15. [Repository organization](#15-repository-organization)
16. [Skills and rules for agents](#16-skills-and-rules-for-agents)
17. [Open source hygiene](#17-open-source-hygiene)
18. [Synthesis: design principles](#18-synthesis-design-principles)

---

## 1. Version overview

| Item | Version | Note |
| --- | --- | --- |
| Go | **1.27.3** (1.27.0 on 2026-08-19) | 1.26 still supported; 1.25 out of support |
| Laravel | 13 (2026-03-17) | PHP 8.3+, one major/year |
| AdonisJS | 7 (2026-02-25) | Node 24+, focus on type-safety via codegen |
| Goravel | v1.18 (2026-07) | ~4.9k★ on the skeleton |
| Inertia.js | 3.x (stable since ~Apr 2026) | no Axios, ESM-only, React 19+ |
| htmx | **4.0.0** (2026-08-28) | npm tag `next` until early 2027 — pin the version |
| golangci-lint | v2.14 | config `version: "2"` |
| Keep a Changelog | 2.0.0 published in 2026-06 | original request: 1.1.0 (decide) |

**→ Suggested decision:** final decision: Go 1.27 as the minimum (see `brainstorming.md` P1), since the project is new — so 1.27-only features (generic methods, `json/v2` GA, `httptest.NewTestServer`) are available from the start. The alternative considered was `go 1.26.0` in `go.mod` (covering both supported versions) with CI on 1.26 and 1.27, adopting 1.27 features only once 1.28 ships.

---

## 2. Laravel 13

Sources: <https://laravel.com/docs/13.x>, <https://github.com/laravel/laravel>.

### Skeleton (slimmed down since L11)

```text
app/Http/Controllers  app/Models  app/Providers/AppServiceProvider.php
bootstrap/app.php     # single fluent configuration (replaced Kernels and Handler)
bootstrap/providers.php
config/               # app, auth, cache, database, mail, queue, session...
database/{migrations,factories,seeders}  database/database.sqlite (SQLite is the default)
public/index.php  resources/{views,css,js}  routes/{web.php,console.php}
storage/  tests/{Feature,Unit}
```

- "Lazy" directories: `Jobs/`, `Events/`, `Policies/` etc. only appear when a `make:*` creates them.
- Opt-in via `install:api` (Sanctum + `routes/api.php`) and `install:broadcasting`.
- Chained `bootstrap/app.php`: `Application::configure()->withRouting()->withMiddleware()->withExceptions()->create()`. Built-in `/up` health route.

### Lifecycle

1. Front controller → container (the `Application` itself).
2. Providers in two phases: `register()` (bindings only) → `boot()` (uses services).
3. Global middleware → router → group/route middleware → controller resolved by the container (type-hint injection) → return value converted into a `Response`.
4. `terminate()` and `defer()` after sending.

### Strengths to copy

- **Routes:** named (`route('posts.show', $post)`), groups (`prefix/middleware/name`), `Route::resource` (7 actions) / `apiResource` (5), route model binding.
- **Form Requests:** class with `authorize()` + `rules()`; failure → redirect with errors in the session (web) or 422 JSON.
- **Manager/driver everywhere:** cache, queue, mail, storage, session — one config, several drivers.
- **Eloquent:** conventions (plural table, `id`, timestamps), scopes, casts, factories, seeders, soft deletes, *strict mode* against N+1.
- **Auth:** separation of **guard** (how it authenticates) × **provider** (where the user comes from); headless Fortify with replaceable "actions"; Sanctum (opaque tokens hashed in the database); Gates and Policies.
- **Testing:** fluent in-process HTTP client (`$this->actingAs($u)->postJson(...)->assertCreated()`), `RefreshDatabase` (transaction per test), **fakes** (`Queue::fake()`, `Mail::fake()`...), time travel.
- **Artisan:** `namespace:action`, `make:*` with publishable stubs (`stub:publish`), `route:list`, `migrate:*`, `db:seed`, `queue:work`, `schedule:run`, `key:generate`, `about`, `down/up`.
- **Vite:** a `public/hot` file signals dev mode; `@vite()` reads `manifest.json` in production; manifest hash = Inertia version.

### What translates poorly to Go

| Laravel | Problem in Go | Idiomatic adaptation |
| --- | --- | --- |
| Facades (`Cache::get`) | global service locator, hidden dependency | inject `*App` or narrow interfaces |
| Container with reflection autowiring | slow, opaque, runtime errors | explicit constructors; typed registration with generics |
| Type-hint injection in methods | does not exist without reflection | `func(c *Ctx) error` or generic handlers `Handle[Req,Res]` |
| Eloquent magic properties, static `User::where` | no `__get`, no static inheritance | `Query[User](db)`, explicit `With("Posts")` |
| Auto-discovery (listeners, policies) | no runtime class scanning | explicit registration or `go generate` |
| String rules `'required\|email'` | errors only at runtime | tags validated at boot or a typed builder |
| `config('app.name')`, `env()` | untyped | typed config structs loaded once |
| Exceptions for control flow (`abort(404)`) | Go uses error values | typed errors + central handler (`withExceptions`) |
| `tinker` | no practical REPL | skip; offer `db` (SQL shell) |

**→ Suggested decision:** copy the **DX surface** (slim skeleton + `install:*`, fluent bootstrap, named routes/groups/resources, form requests, manager/driver, `make:*` with stubs, migrations/factories/seeders, test client + fakes, Vite/Inertia). Replace **the mechanics** (facades, reflection, magic) with explicit, typed Go.

---

## 3. AdonisJS v7

Sources: <https://adonisjs.com/blog/v7>, <https://docs.adonisjs.com/v6-to-v7>, <https://docs.adonisjs.com/guides>.

### Structure

```text
app/{controllers,models,middleware,validators,exceptions,policies,transformers,mails,jobs}
bin/{server.ts,console.ts,test.ts}   config/   providers/
database/{migrations,seeders,factories}  + schema.ts gerado (v7)
start/{env.ts,kernel.ts,routes.ts}   inertia/pages   resources/views
tests/{unit,functional,browser}     adonisrc.ts (manifesto do projeto)
.adonisjs/   # generated code (v7) — committed
```

### Highlights

- **Provider lifecycle with 5 hooks:** `register` → `boot` → `start` → `ready` → `shutdown` (v7: shutdown in reverse order). Providers and preloads filterable by **environment** (`web`, `console`, `test`, `repl`).
- **`warmup` mode:** boots without opening connections to inspect routes — used by `codegen`. Great for generating artifacts in Go.
- **"Services" instead of facades:** `import router from '@adonisjs/core/services/router'` — explicit module singletons.
- **Env validated at boot:** `Env.schema.number()`, `Env.schema.secret()` (logs `[redacted]`), boot aborts if invalid. Hierarchy `.env.[ENV].local` > `.env.local` > `.env.[ENV]` > `.env`.
- **Three middleware stacks:** *server* (every request), *router* (only if a route matches), *named* (per route, with typed options).
- **Auto-named routes** (`posts.index`) + typed `urlFor('posts.show', {id})` (v7).
- **VineJS:** compiled, fast validation; failure → redirect with errors (web) or 422.
- **Lucid v7:** generates `schema.ts` **from the migrations**; models extend the generated class. "Smart" migrations (`create_*` → `createTable`).
- **Transformers (v7):** serialization layer separate from the model (like Laravel's API Resources).
- **Auth:** guards `session`, `access_tokens` (opaque, hashed in the database), `basic`; `verifyCredentials` hashes even without a user (anti timing attack). **Bouncer** (abilities + policies), **Ally** (social), **Limiter** (`multi()` in v7).
- **Encryption rewritten (v7):** drivers `aes256gcm`, `aessiv` (deterministic, queryable), key rotation (first one encrypts, the others only decrypt).
- **Japa:** declared suites, `client.visit('posts.store')` by route name, `withGlobalTransaction()`, fakes (`mail.fake()`, `emitter.fake()`), **AI agent detection** changes the reporter.
- **Ace:** `make:*`, `list:routes`, `migration:*`, `db:seed`, `repl`, `generate:key`, `eject` (local stubs take precedence). **`node ace add pkg`** = install + `configure` via **codemods** (edits rc, env, kernel, config).
- **Vite:** in Node it runs in-process; in Go that is not possible → follow Laravel's *hot file* model.
- **Transmit** (SSE with channels and authorization), **Tuyau** (typed client generated from the routes).

**→ Suggested decision:** copy the **5-hook lifecycle with environment filtering**, **env validated at boot with a `Secret` type**, **three middleware stacks**, **auto-named routes + `urlFor`**, **codegen from the app in warmup mode** (routes → typed TypeScript), **`add`/`configure` via codemods** (`go/ast`), **ejectable stubs**, **transformers**, **encryption with key rotation**.

---

## 4. Goravel

Sources: <https://github.com/goravel/goravel>, <https://github.com/goravel/framework>, <https://www.goravel.dev>, <https://news.ycombinator.com/item?id=36612190>.

- Skeleton ~4.9k★; core ~531★. Near-literal port of Laravel. Maintained by a small team; two release lines in parallel.
- **Multi-repo:** core with `contracts/*`; HTTP drivers (`gin`, `fiber`), database (`postgres`, `sqlite`...), `redis` in separate repositories/modules. **Good idea.**
- Fluent bootstrap: `foundation.Setup().WithProviders(...).WithRouting(...).WithCommands(...).Create()`.
- **Generators register what they create** (`make:command` adds to `bootstrap/commands.go`) — discovery by codegen, not reflection. **Good idea.**
- **Runners:** providers return `[]Runner{ShouldRun, Run, Shutdown}` — HTTP, scheduler and workers with graceful shutdown. **Good idea.**
- Facades = generated functions in `app/facades` over a **global app** → main criticism.
- Config `map[string]any` with string keys (`GetString("app.host")`) → untyped.
- HTTP abstracts over gin/fiber → `net/http` middleware becomes a second-class citizen.
- ORM on top of **GORM** (double reflection, zero-value pitfalls).
- Jobs with `Handle(args ...any)` → untyped.
- CLI = `go run . artisan` (compiles the whole app on every command).
- Tests with testify + Docker containers per package (does not work on Windows).

**→ Suggested decision:** copy **drivers in separate modules**, **generators that edit a Go registry**, **Runners with shutdown**, **two release lines**. Avoid: global app, stringly-typed config/container, abstraction over gin/fiber, GORM, `any` in jobs, mandatory Docker in tests.

---

## 5. Other Go frameworks

| Project | Status | What to learn |
| --- | --- | --- |
| **Buffalo** (~8.4k★) | minimal maintenance | Died from coupling to Gorilla, its own JS bundler, `packr`, bus factor 1. **Don't own the bundler; use the stdlib.** |
| **Beego** (~32k★) | slow | Subsystems usable in isolation (good); reflection router and global config (bad) |
| **Revel** (13k★) | abandoned | Its own build harness broke with modules. **Never wrap `go build`.** |
| **Gin** (~85k★) | active | Loved bind/groups DX; `gin.Context` and non-`net/http` middleware criticized |
| **Echo** (~31k★) | active | `func(c) error` + central `HTTPErrorHandler` — **copy** |
| **Fiber** (~38k★) | active | fasthttp, incompatible with `net/http`, recycled context → races. **Avoid.** |
| **Chi** (~21k★) | active | 100% `net/http`, `func(http.Handler) http.Handler` — **copy the signature** |
| **Huma** (~4.5k★) | active | Typed handlers `func(ctx, *In) (*Out, error)` → OpenAPI 3.1; RFC 9457 errors |
| **Fuego** (~1.8k★) | active | On top of ServeMux 1.22, OpenAPI from generic handlers — **best stdlib-first reference** |
| **Encore** (12.5k★) | active | **Local dev dashboard** (routes, queries, traces) — copy the idea |
| **PocketBase** (~61k★) | very active | Single binary, **pure-Go SQLite** by default, hooks, embedded admin, clear pre-1.0 warning |
| **GoFr** (~21k★) | active | Observability (OTel) from day 1, health checks per data source |
| **Iris** (~25k★) | — | Governance anti-pattern (rewritten history, benchmarks) |
| **Ent** (~16k★) | active | Type-safe codegen instead of reflection |
| **templ** (~10k★) | active | Typed HTML components; de facto standard with htmx |
| **go-blueprint / Gowebly** | active | Popular scaffolders *because they leave no runtime* |
| **Pagoda** (~3k★) | active | Starter kit: Echo + Ent + SQLite (including queue) + HTMX |
| **Andurel** (~200★) | 2026 | Rails-like, templ+Datastar **or** Inertia+Vue — same niche |
| **Velocity** (~40★) | 2026 | Laravel-like "no magic": refuses to start without `APP_KEY`, CORS deny-by-default, `--force` in prod |

**→ Suggested decision:** the "Laravel for Go" niche is full of small projects; differentiation comes from **idiomatic design, quality and longevity**, not from a feature list. Main code references: **Chi** (middleware), **Echo** (central error), **Fuego/Huma** (typed handlers), **PocketBase** (SQLite/single binary), **Velocity** (secure defaults).

---

## 6. Go community opinion

Synthesis of HN, blogs and recurring positions on r/golang:

1. **"Use the stdlib."** Since 1.22 `ServeMux` has methods and wildcards; with `slog`, `embed`, generics, `CrossOriginProtection`, there is little reason left for a *router* framework.
2. **"Libraries, not frameworks."** But the pro-framework side acknowledges: session, auth, authorization, CSRF, validation, mail, queues and migrations are the real cost — **that is the niche**.
3. **Resentment of imported idioms:** facades, God objects, `any`, annotations, magic strings.

### Acceptance checklist

- [ ] `net/http`-native: handlers and middleware interoperate with `http.Handler`.
- [ ] No globals, singletons or registration `init()`.
- [ ] No magic reflection (DI, auto-discovery). Reflection acceptable only in bind/validation.
- [ ] No stringly-typed APIs (config, container, jobs).
- [ ] Core with few dependencies; drivers in separate modules.
- [ ] Don't own the JS bundler or wrap `go build`.
- [ ] Clear governance: SemVer, stability policy, predictable releases.
- [ ] Observability and production-readiness from the start: `slog`, health, graceful shutdown.
- [ ] Generators write code the user owns.

**Acceptable positioning:** *"Laravel's productivity, built from idiomatic, `net/http`-native packages usable in isolation — no globals, no reflection DI, with code generation instead of magic."*

---

## 7. Language and stdlib: what matters for a framework

Sources: <https://go.dev/doc/go1.27>, <https://go.dev/doc/go1.26>, <https://go.dev/doc/go1.25>, <https://go.dev/doc/go1.24>.

### Go 1.27

- **Generic methods** (not in interfaces): `q.Pluck[string]("email")` on `*Query[T]` — huge for a query builder.
- `encoding/json/v2` GA (v1 now uses v2 underneath; rejects invalid UTF-8 and duplicate names).
- `httptest.NewTestServer` (in-memory network, compatible with `synctest`), `synctest.Sleep`.
- `database/sql`: `ConvertAssign` and `RowsColumnScanner` for drivers.
- `goroutineleak` profile GA — expose it in the dev dashboard.

### Go 1.26

- `new(expr)` → `Age: new(yearsSince(born))` for optional fields.
- Self-referential generic constraints: `type Model[M Model[M]] interface{...}`.
- `errors.AsType[E](err)`.
- `slog.NewMultiHandler`.
- `go fix` rewritten (modernizers) + **`//go:fix inline`**: Vukem can mark deprecated APIs and the user's `go fix` migrates the calls automatically.
- `signal.NotifyContext` cancels with a cause (signal name).
- `T.ArtifactDir()`; Green Tea GC by default; cgo ~30% cheaper.

### Go 1.25

- **`http.CrossOriginProtection`** — token-less CSRF via `Sec-Fetch-Site`/`Origin`:

```go
cop := http.NewCrossOriginProtection()
cop.AddTrustedOrigin("https://admin.example.com")
cop.AddInsecureBypassPattern("POST /webhooks/")
handler := cop.Handler(mux)
```

- `testing/synctest` GA (virtual clock).
- `sync.WaitGroup.Go`, container-aware GOMAXPROCS, expanded `os.Root`, `ignore` directive in `go.mod` (ignore `node_modules`).

### Earlier baseline

- **1.22:** `mux.HandleFunc("GET /posts/{id}", h)`, `r.PathValue("id")`, `{path...}`, `{$}`; conflicts panic at registration. **Missing:** groups, named routes, URL generation — Vukem adds a thin layer that compiles groups into ServeMux patterns and keeps a name→pattern table.
- **1.23:** iterators — `func (q *Query[T]) Each(ctx) iter.Seq2[T, error]`.
- **1.24:** **`tool`** directive in `go.mod` (`go get -tool ...` → `go tool x`), `os.Root`, `T.Context()`, `B.Loop`, `crypto/rand.Text()`, `omitzero`, `http.Protocols`.
- **1.21/1.20:** `slog`, `slices`, `maps`, `context.WithoutCancel` (post-response work), `errors.Join`, `http.ResponseController` (SSE).

**→ Suggested decision:** router as a thin layer over `ServeMux`; CSRF via `CrossOriginProtection`; storage with `os.Root`; assets with `embed` + `http.FileServerFS`; query streaming with `iter.Seq2`; `//go:fix inline` as the deprecation policy.

---

## 8. Idiomatic patterns

Sources: [Mat Ryer — HTTP services after 13 years](https://grafana.com/blog/2024/02/09/how-i-write-http-services-in-go-after-13-years/), [Google Go Style Guide](https://google.github.io/styleguide/go/), [Code Review Comments](https://go.dev/wiki/CodeReviewComments), [Peter Bourgon](https://peter.bourgon.org/go-best-practices-2016/), [Dave Cheney](https://dave.cheney.net/2014/10/17/functional-options-for-friendly-apis).

- **`main` only calls `run(ctx, args, getenv, stdout) error`** → end-to-end testable; `routes.go` with the whole API surface; `NewServer(deps...) http.Handler`.
- **Functional options** (`New(opts ...Option)`) for few options; **config struct** when there are many and they are static.
- **Small interfaces, defined at the consumer.** "Accept interfaces, return structs."
- **DI:** the community standard is explicit wiring in `main`. Containers (fx, dig, samber/do) are polarizing. If there is a container: optional, typed with generics, no reflection.
- **No globals/`init()`** (tolerated exception: `database/sql` driver registration).
- **`context.Context` as the 1st parameter**, never in a struct; unexported context keys.
- **Errors:** `fmt.Errorf("load user %d: %w", id, err)`; sentinels for conditions; types for data (`*ValidationError`); `errors.Is/AsType`; `errors.Join`. Handlers that return `error` + a central handler that maps to HTTP status.
- **Middleware:** `func(http.Handler) http.Handler`.
- **Graceful shutdown:** `signal.NotifyContext` → `srv.Shutdown(ctx)` → close queues and pools in reverse order. Always set `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, `IdleTimeout`.
- **Official layout** (<https://go.dev/doc/modules/layout>): flat root, `internal/`, `cmd/<name>/`. `golang-standards/project-layout` is **not** official; `pkg/` is noise.
- **Package names:** short, lowercase, singular; no `util`, `common`, `helpers`.

---

## 9. Database

### Drivers

- **PostgreSQL:** `jackc/pgx/v5` (pool, `CopyFrom`, `LISTEN/NOTIFY`, rich types, `pgx.CollectRows(rows, pgx.RowToStructByName[T])`). Via `pgx/v5/stdlib` for `database/sql` compatibility.
- **SQLite:**

| Driver | Mechanism | Pros | Cons |
| --- | --- | --- | --- |
| `mattn/go-sqlite3` | cgo | fastest | requires a C toolchain (bad on Windows) |
| `modernc.org/sqlite` | transpiled C | **pure Go**, `CGO_ENABLED=0`, used by PocketBase/Gitea | ~1.5–2× slower on heavy queries |
| `ncruces/go-sqlite3` | WASM (wazero) | pure Go, good API | larger binary, slower |

- Mandatory pragmas for web: `journal_mode=WAL`, `busy_timeout=5000`, `foreign_keys=ON`, `synchronous=NORMAL`; **write pool with 1 connection** + read pool; `BEGIN IMMEDIATE` on writes.

### ORMs and query builders

| Lib | Style | Comment |
| --- | --- | --- |
| GORM | Active Record with reflection | the most "Eloquent", but `interface{}` API, surprising mutation, silent errors |
| ent | schema-as-code, codegen | excellent, heavy, doesn't feel like Eloquent |
| bun | SQL-first ORM | good middle ground (pg, sqlite, mysql) |
| sqlc | SQL → Go | correct and loved; weak for dynamic filters |
| sqlx | scan into struct | minimal |
| squirrel/goqu | dynamic builders | squirrel in maintenance mode |
| Jet | typed builder from the schema | codegen |
| **Bob** | generic builder + codegen | the closest to "typed Eloquent" — study it |

### Designing a "generic Eloquent"

```go
users, err := orm.Query[User](db).
	Where("active", true).
	OrderBy("created_at", orm.Desc).
	Limit(10).
	Get(ctx)

for u, err := range orm.Query[User](db).Each(ctx) { /* streaming */ }
```

- Map structs via reflection **cached** (at boot) or codegen for typed columns.
- Active Record (`user.Save()`) requires the model to hold a DB handle → conflicts with explicit dependencies. Prefer `db.Save(ctx, &user)` / `orm.Query[User](db)`.
- Relations loaded explicitly (`With("Posts")`); strict mode against N+1.

### Migrations

- **pressly/goose:** SQL or Go, embeddable via `embed.FS`, usable as a library (`goose.NewProvider`) — good for `app migrate`.
- golang-migrate: up/down, awkward library API.
- Atlas: declarative, diffing; partly commercial.

**→ Suggested decision:** core on top of `database/sql` with dialects (Postgres/SQLite); `modernc.org/sqlite` as default (no cgo, crucial on Windows); pgx for Postgres; own lean generic query builder; migrations with a fluent schema builder in Go (Laravel-style) on top of an own runner or goose; Mongo as a separate API (don't force it into the SQL builder).

---

## 10. Testing

- **Table + subtests** (`t.Run`, `t.Parallel()`); no `tt := tt` since 1.22.
- **`httptest`:** `NewRecorder` (unit), `NewServer` (e2e), `NewTestServer` + `synctest` (1.27).
- **Fluent test client** à la Laravel: `app.Test(t).ActingAs(u).PostJSON("/posts", body).AssertCreated().AssertJSONPath("data.title", "x")`.
- **Database:** SQLite `:memory:` or a file in `t.TempDir()`; Postgres with rollback per test or `CREATE DATABASE x TEMPLATE base`; optional `testcontainers-go` (works with Docker Desktop on Windows).
- **Fakes as interface swaps on the app instance:** `q := app.FakeQueue(t); q.AssertPushed[SendEmail](t, ...)`.
- **Golden files** (`-update`, `testdata/`), **fuzzing** (router, parsers), **`-race`** in CI, **coverage** (`-coverprofile`, `go build -cover`), **benchmarks** with `b.Loop()`.
- **Assertions:** the Google Style Guide discourages assertion libs → stdlib + `go-cmp` in the core; testify is the most used in the real world.
- **Mocks:** hand-written fakes > generated mocks, for small interfaces.

---

## 11. Security and authentication

- **Tokens:** `crypto/rand.Text()` (1.24); never `math/rand`.
- **Password hashing:** `x/crypto/bcrypt` (72-byte limit) and `argon2id` (preferred by OWASP, parameters in a PHC string) behind a `Hasher` interface, with `NeedsRehash`.
- **Comparison:** `subtle.ConstantTimeCompare` / `hmac.Equal`.
- **Sessions:** `alexedwards/scs/v2` (postgres/sqlite/redis/mongo/memory stores, `RenewToken` against fixation) or an own encrypted cookie (AES-GCM, key derived via `crypto/hkdf` from `APP_KEY`). Flags: `HttpOnly`, `Secure`, `SameSite=Lax`, `__Host-` prefix.
- **CSRF:** `http.CrossOriginProtection` + SameSite (replaces gorilla/csrf, which had a CVE in 2025).
- **API tokens:** random opaque tokens with SHA-256 in the database (Sanctum/Adonis style). JWT (`golang-jwt/jwt/v5`) optional, always pinning the algorithm.
- **Others:** `os.Root` for uploads, `http.MaxBytesReader`, `html/template` (contextual escaping), `x/time/rate`, `govulncheck` in CI.
- **Secure defaults (Velocity):** refuse to start without `APP_KEY`, CORS deny-by-default, destructive commands require `--force` in production.

---

## 12. Frontend: HTMX v4, Inertia v3, Vite, templates

### HTMX 4.0 (default)

Sources: <https://four.htmx.org/migration-guide-htmx-4/>, <https://four.htmx.org/reference/headers/>.

- Uses `fetch()`; default timeout 60s.
- **Explicit inheritance:** `hx-target:inherited`. **CSRF needs `<body hx-headers:inherited='{"X-CSRF-Token":"…"}'>`.**
- **4xx/5xx responses swap** (except 204/304) → re-rendering a form with **422** becomes natural. `hx-status:422="target:#errors"`.
- Built-in morph (`innerMorph`/`outerMorph`); `<hx-partial>` for multiple targets; OOB after the main content.
- History without local cache: back/forward re-issues the request with `HX-History-Restore-Request` → **every pushed URL must serve a full page**.
- **Request headers:** `HX-Request`, **`HX-Request-Type: partial|full`** (decides fragment vs layout), `HX-Current-URL`, `HX-Source`, `HX-Target` (`tag#id`), `HX-Boosted`, `HX-History-Restore-Request`.
- **Response headers:** `HX-Trigger`, `HX-Location`, `HX-Redirect`, `HX-Refresh`, `HX-Push-Url`, `HX-Replace-Url`, `HX-Retarget`, `HX-Reswap`, `HX-Reselect`. Removed: `HX-Trigger-After-Swap/Settle`.
- Always `Vary: HX-Request`.

**Go helper checklist:** request inspector (`IsHTMX`, `IsPartial`, `IsBoosted`, `IsHistoryRestore`), automatic fragment/layout rendering, fluent response header builder, `<hx-partial>`/OOB helpers, 422 flow, redirect that picks `HX-Location` vs 303, CSRF in `hx-headers:inherited`, SSE.

Existing libs (`angelofallars/htmx-go`, `donseba/go-htmx`) are from the htmx 2 era → write an own helper (it is small).

### Inertia v3 (optional)

Source: <https://inertiajs.com/docs/v3/core-concepts/the-protocol>.

- **Initial page:** `<script data-page="app" type="application/json">` with `/` escaped as `\/`.
- **Request headers:** `X-Inertia`, `X-Inertia-Version`, `X-Inertia-Partial-Component/Data/Except`, `X-Inertia-Reset`, `X-Inertia-Error-Bag`, `X-Inertia-Except-Once-Props`, `Purpose: prefetch`, `Precognition`.
- **Page object:** `component`, `props` (always with `errors`), `url`, `version` + optional `encryptHistory`, `clearHistory`, `mergeProps`, `deferredProps`, `onceProps`, `scrollProps`, `sharedProps`, `rescuedProps`, `flash`...
- **Prop types:** `Optional`, `Always`, `Defer(group, rescue)`, `Merge`, `DeepMerge`, `Prepend`, `Once`, `Scroll` — with recursive dotted paths.
- **Version:** GET with a mismatched version → **409** + `X-Inertia-Location`. **302 on PUT/PATCH/DELETE → 303.**
- **SSR:** POST `http://127.0.0.1:13714/render` → `{head, body}`; **always fall back to CSR on failure**.
- **Go adapters:** `romsar/gonertia` (idiomatic, zero-dep), `petaki/inertia-go` (most complete v3, no CSR fallback), `goravel/inertia`, `mayahiro/go-inertia` (Precognition, no SSR). **Write an own adapter** integrated with session/flash/validation/Vite.

### Vite

- Laravel model: the plugin writes a **`hot`** file with the dev server URL → Go detects dev mode by the file's presence.
- `{{ vite "resources/js/app.tsx" }}`: dev → `@vite/client` + entry (+ React Refresh preamble); prod → reads `.vite/manifest.json`, emits recursive `script`, `css`, `modulepreload`.
- Manifest hash = Inertia version. `/build/*` with immutable caching.
- **HTMX can be zero-Node:** vendor `htmx.min.js` and serve it via `embed`.

### Templates

- **`html/template`:** stdlib, contextual escaping; layouts with `{{block}}`; single-block rendering (`ExecuteTemplate(w, "row", data)`) = htmx's *template fragments* pattern. Weak on type-safety.
- **templ:** compiled typed components, LSP; requires `go tool templ generate`. De facto standard with htmx (GOTH stack).
- **gomponents:** HTML in pure Go, no codegen.
- Common interface: `Render(ctx, w) error` (already satisfied by `templ.Component`).

**→ Suggested decision:** `html/template` as default (zero dependencies) + templ support via the interface; own htmx v4 helper; own Inertia v3 adapter with CSR fallback; Vite via the *hot file* model.

---

## 13. Mongo, Redis, queues

- **MongoDB:** `go.mongodb.org/mongo-driver/v2` (v2.9.x; note the CVE fixed in 2.8.2). Does not fit the SQL builder → separate API (`Collection[T]` with typed filters).
- **Redis:** `redis/go-redis/v9` (official; cluster, pipelines, OTel) or `rueidis` (auto-pipelining, client-side cache). Valkey-compatible. Uses: cache, session, rate limit, locks, queue.
- **Queues:**
  - `riverqueue/river` — Postgres via pgx; typed generic jobs (`river.Worker[Args]`); **transactional enqueue** (`InsertTx`) — killer feature.
  - `maragudk/goqite` — SQS-style queue on SQLite/Postgres, pure `database/sql`.
  - `hibiken/asynq` — Redis, mature, pace has slowed.
  - Design: `Queue` interface with `sync` (dev), `database`, `redis` drivers; generic jobs `queue.Register[SendWelcomeEmail](app, handler)`; drain on shutdown.

---

## 14. Tooling: lint, format, hooks, release

### Lint and formatting

- **golangci-lint v2** (`version: "2"`): formatters `gofumpt` + `gci` + `golines` (120 col, `tab-len: 4`); linters `standard` + errorlint, bodyclose, sqlclosecheck, rowserrcheck, contextcheck, noctx, gosec, revive, gocritic, sloglint, modernize, usetesting, thelper, nolintlint (with explanation). Avoid `wrapcheck` and `paralleltest` (noisy).
- Install the official binary (`winget install GolangCI.golangci-lint`) — upstream **discourages** `go install`/`tool` for it.
- `go vet`, `govulncheck` (CI + weekly cron, outside pre-commit), `go fix -diff` as a CI warning, `gopls` (+ `gopls mcp`).

### Hooks

- **lefthook** (Go binary, great on Windows): pre-commit (fmt, lint on staged, `go mod tidy -diff`, markdownlint), commit-msg (Conventional Commits), pre-push (`go test`, `go vet`, `govulncheck`).
- **Conventional Commits validator:** ~60 lines of Go in the repo itself (`go run ./internal/tools/commitlint`) → no Node. Reinforced in CI with `amannn/action-semantic-pull-request` (PR title).
- On Windows, `-race` requires cgo/gcc → keep `-race` in CI.

### Release

- **git-cliff** generates `CHANGELOG.md` in the **exact Keep a Changelog** format (release-please does not follow the KaC header).
- **svu** or `git cliff --bumped-version` for the next SemVer version.
- **GoReleaser v2** for CLI binaries (changelog disabled, notes from git-cliff).
- **Go versioning rules:** tags `vX.Y.Z`; **v0** with no promise; **v2+** requires `/v2` in the module path; **never move a published tag** (the proxy caches it) → fix with `retract`; submodules with a prefix (`drivers/postgres/v1.2.0`); `go install` fails if `go.mod` has a `replace`.

### Markdown and editor

- **markdownlint-cli2** (Node; CI via action). `MD010` with `ignore_code_languages: ["go","makefile"]` (tabs in Go blocks), `MD024 siblings_only` (required for KaC), `MD013` disabled.
- **`.editorconfig`:** tab (width 4) in `*.go`, `go.mod`, Makefile; **spaces in YAML and Markdown** (YAML forbids tabs).
- **`.gitattributes` with `eol=lf`** — essential on Windows, otherwise gofumpt reports a diff on every file.

### Task runner

- **Taskfile (go-task v3)** — runs POSIX commands via a built-in interpreter, works the same in PowerShell. Make is bad on Windows; mage is verbose.

### CI (GitHub Actions)

- `actions/checkout@v6`, `actions/setup-go@v6`, `golangci-lint-action@v9`.
- Matrix `ubuntu/windows/macos` × `oldstable/stable`.
- Integration job with `services: postgres:17` (and redis) on Linux, tests with `//go:build integration`.
- govulncheck, markdownlint, semantic PR title, CodeQL (default setup), OpenSSF Scorecard (pin actions by SHA), Dependabot with `build`/`ci` prefix.

---

## 15. Repository organization

- **Core without heavy dependencies + separate modules per driver** in the same repo (precedents: aws-sdk-go-v2, opentelemetry-go, gofiber/storage; Goravel does it in separate repos).

```text
vukem/                    module github.com/AlexGalhardo/vukem   tags v0.x.y
├── cmd/vukem/            CLI (go install .../cmd/vukem@latest)
├── drivers/postgres/     own module, tags drivers/postgres/v0.x.y
├── drivers/mongo/
├── drivers/redis/
└── go.work               local dev only; CI runs with GOWORK=off
```

- Release order: core → `go get core@vX` in the drivers → tag the drivers.
- Cost: lint/test/release per module (dynamic matrix). **Start with core + a few drivers**, split only when a dependency demands it.

---

## 16. Skills and rules for agents

- **samber/cc-skills-golang** (~3.5k★, 46 skills, MIT) — **already installed in `.claude/skills/`**. `golang-how-to` orchestrates; `/golang-how-to configure` writes directives into `CLAUDE.md`/`AGENTS.md`.
- **anthropics/skills** — SKILL.md spec and `skill-creator`.
- **PatrickJS/awesome-cursorrules** — Go rules (ServeMux REST, Go+HTMX) as raw material for `.claude/rules/`.
- **obra/superpowers** — TDD, debugging, brainstorming (partially installed).
- **AGENTS.md** (<https://agents.md>, Agentic AI Foundation / Linux Foundation): read by Codex, Copilot, Cursor, Gemini, Amp. Claude Code reads `CLAUDE.md`; recommended pattern: **canonical content in `AGENTS.md`, `CLAUDE.md` with `@AGENTS.md`**.
- **`.claude/rules/`:** one topic per file; `paths:` in the frontmatter loads it only when touching matching files. Suggested: `go.md` (`**/*.go`), `testing.md` (`**/*_test.go`), `commits.md`, `changelog.md`, `markdown.md`, `drivers.md` (`drivers/**`: core never imports a driver).

---

## 17. Open source hygiene

- **LICENSE** MIT (pkg.go.dev only shows docs with a detected OSI license).
- **CONTRIBUTING.md:** setup (Go, Windows via winget), `task hooks`, Conventional Commits with types and scopes (= packages), test/lint commands, how to add a driver, release process.
- **SECURITY.md:** enable *Private vulnerability reporting*; supported versions; SLA; GHSA feeds the Go vulnerability database.
- **CODE_OF_CONDUCT.md** (Contributor Covenant), YAML issue templates (`bug_report.yml` with Go version and OS), PR template, `CODEOWNERS`, branch protection with squash merge (PR title = commit message).
- Badges: Go Reference, Go Report Card, CI, Codecov, OpenSSF Scorecard.

---

## 18. Synthesis: design principles

1. **`net/http` first.** Router is a thin layer over `ServeMux`; middleware `func(http.Handler) http.Handler`; handlers `func(*Ctx) error` adaptable to `http.Handler`.
2. **Explicit > magic.** No globals, no `init()`, no reflection DI. A container, if any, is typed with generics and optional.
3. **Typed > stringly.** Config in structs, generic jobs/events, named routes with checkable `urlFor`, codegen for TS.
4. **Codegen > reflection.** `make:*` edits Go registries; *warmup* mode to generate artifacts.
5. **Stdlib > dependency.** Third parties only when there is no simple path (pgx, modernc/sqlite, x/crypto, possibly goose/scs).
6. **Lean core, drivers in modules.** `go get` of the core does not pull in pgx, mongo or redis.
7. **Laravel/Adonis DX where it matters most:** generator CLI, migrations/factories/seeders, test client + fakes, form requests, auth guard/provider, Vite/Inertia/HTMX.
8. **Secure by default.** Mandatory `APP_KEY`, `CrossOriginProtection`, server timeouts, `os.Root`, `__Host-` cookies.
9. **Production-ready from the prototype.** `slog`, `/up` health, graceful shutdown, Runners.
10. **Governance and longevity.** Strict SemVer, v0 until stable, `//go:fix inline` for deprecations, generated changelog, never move tags.
