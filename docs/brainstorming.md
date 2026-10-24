# Brainstorming

Decision log from the design interview (grilling sessions, 2026-10-16). Each entry records the **decision**, the **alternatives considered**, and the **rationale**. Research backing these choices lives in [golang-web-search.md](golang-web-search.md).

Status legend: ✅ decided · ⏳ open

## Identity

| # | Topic | Decision | Status |
| --- | --- | --- | --- |
| I1 | Name | **Vukem** — coined, unique word; also the CLI command (`vukem new blog`). Picked by the maintainer on 2026-10-16 (v0.3.0) | ✅ |
| I2 | Module path | `github.com/AlexGalhardo/vukem` (repository renamed from `golang-framework` and made public on 2026-10-16); root package name `vukem`. Tags before v0.3.0 declare the old path and are not installable from the new one | ✅ |
| I3 | License | MIT © 2026 Alex Galhardo | ✅ |
| I4 | Language | Code, comments, commits, docs, README: **English**. Discussion with maintainer may be Portuguese | ✅ |
| I5 | Positioning | *Laravel/AdonisJS productivity assembled from idiomatic, `net/http`-native, separately usable packages — no globals, no reflection DI, codegen instead of magic* | ✅ |

**Rejected names** (conflict check on pkg.go.dev, Homebrew, npm, GitHub): ipe, arara, caju, gale, kiln, tessera, anvil, rivet, loom, crucible, foundry, smithy, chisel, ember, blaze, alloy, forge. Clean-but-declined (maintainer wants a coined word, not an existing one): brasa, jabuti, tatu, ingot, quench, hearth, smelt. Coined finalists: zorvo, kovra, trovik, velkor; the maintainer picked **Vukem**.

## Platform

| # | Topic | Decision | Alternatives | Rationale |
| --- | --- | --- | --- | --- |
| P1 | Go version | **1.27 minimum** (`go 1.27.0`) | 1.26 | Generic methods make the query builder ergonomic (`Query[T].Pluck[V]`); by 1.0, 1.28/1.29 will exist |
| P2 | Repo layout | **Monorepo: core module + one module per heavy driver** (`drivers/postgres`, `drivers/redis`, `drivers/mongo`) | single module | `go get` of core never pulls pgx/redis/mongo; precedents: aws-sdk-go-v2, otel-go, gofiber/storage |
| P3 | Versioning | **v0.x until 1.0**; BETA ships `v1.0.0-beta.N` → `-rc.N` → `v1.0.0`. Never move a tag; use `retract` | pre-releases from day one | Go modules: v0 has no compat promise; v2+ needs `/v2` path, so v1 must be durable |
| P5 | Platforms | **Windows 11/10 first-class** alongside Linux and macOS; no cgo by default; Windows rules in [architecture.md](architecture.md#platform-support) | Unix-first with best-effort Windows | Maintainer requirement (2026-10-16) |
| P4 | Git flow | **Trunk-based + squash merge**; PR title = Conventional Commit; initial scaffolding committed straight to `main` | direct commits forever | Clean linear history feeds git-cliff |

## Architecture

| # | Topic | Decision | Alternatives | Rationale |
| --- | --- | --- | --- | --- |
| A1 | Router | **Thin layer over `http.ServeMux`**: groups, named routes, `Resource()`, `URLFor()`; handlers `func(c *Ctx) error`; middleware `func(http.Handler) http.Handler`; any `http.Handler` mountable | chi underneath; plain `http.HandlerFunc` | stdlib-first; whole ecosystem's middleware works; central error handling (Echo pattern) |
| A2 | DI | **`App` struct with typed fields + providers** (`Register`/`Boot`/`Shutdown`) wired explicitly in `bootstrap/app.go` | generic container; uber/fx; samber/do | Idiomatic, no hidden deps, no facades/globals |
| A3 | Controllers | **Structs with methods** (`func (p *PostController) Index(c *Ctx) error`); `r.Resource("posts", ctrl)` detects `Index/Create/Store/Show/Edit/Update/Destroy` via small interfaces | free functions | Laravel familiarity; interface detection is compile-safe, no reflection on signatures |
| A4 | Errors | **Central handler + typed errors** (`HTTPError`, `ValidationError`, `ErrNotFound`) → HTML status pages, redirect+flash (web), 422 re-render (HTMX), `application/problem+json` RFC 9457 (API); debug page in dev | plain `{error}` JSON | One place maps errors to responses (like Laravel `withExceptions`) |
| A5 | Config | **Typed structs per module**; own `.env` parser (no godotenv); precedence `.env.<env>.local` > `.env.local` > `.env.<env>` > `.env`; validated at boot; refuses to start without `APP_KEY`; `Secret` type logs `[redacted]` | caarlos0/env + godotenv | Removes stringly `config("a.b")`; AdonisJS-style boot validation |
| A6 | App skeleton | Hybrid Laravel/Go layout (below) | `internal/`-only; literal Laravel copy | Familiar to Laravel/Adonis users, still short idiomatic package names |
| A7 | CLI | **Global CLI** (`go install …/cmd/vukem@latest`) does `new`, `make:*`, `serve` (hot reload + optional Vite), `build`. App-bound commands (`migrate`, `db:seed`, `queue:work`, user commands) compile **into the app binary**; the global CLI delegates (`go run ./cmd/app <cmd>`). Generators register what they create by editing `bootstrap/*.go`. Parser: stdlib `flag` | Goravel-style `go run . artisan` only; cobra | Fast generators; no reflection discovery; zero deps |
| A8 | Agent tooling | **Vukem Boost**: `vukem boost:install` writes AGENTS.md guidelines, `.claude/skills/vukem-*` and MCP configs; the app command `boost:mcp` is a stdlib MCP server (stdio) with read-only app tools | Separate module; third-party MCP SDK | Maintainer requirement (2026-10-16), Laravel Boost parity; zero deps |

### Generated app skeleton

```text
blog/
├── cmd/app/main.go        # run(ctx, args, getenv, stdout) error
├── bootstrap/
│   ├── app.go             # fluent builder
│   ├── providers.go       # edited by make:provider
│   └── commands.go        # edited by make:command
├── config/                # typed structs per module
├── app/
│   ├── controllers/
│   ├── models/
│   ├── middleware/
│   ├── requests/          # form requests (validation)
│   └── policies/          # created on demand
├── database/{migrations,factories,seeders}/
├── routes/web.go
├── resources/views/       # html/template
├── public/                # htmx.min.js, css
├── storage/
├── tests/
└── .env.example
```

## Data

| # | Topic | Decision | Alternatives | Rationale |
| --- | --- | --- | --- | --- |
| D1 | ORM | **Own generic query builder over `database/sql`** with Postgres/SQLite dialects: `orm.Query[User](db).Where(...).Get(ctx)`, `Each()` → `iter.Seq2`; explicit relations (`With`) from ALPHA; sqlc interoperable (same `*sql.DB`) | wrap bun; wrap GORM; sqlc-only | stdlib-first; avoids GORM baggage; generics give typed results |
| D2 | Migrations | **Fluent schema builder in Go** (`s.Create("users", func(t *schema.Table){ t.ID(); t.String("email").Unique(); t.Timestamps() })`) + `s.Raw(sql)`; own runner with batches table | goose SQL files; both | Laravel DX; no goose dependency |
| D3 | SQLite | **`modernc.org/sqlite`** (pure Go, no cgo) default; WAL, `busy_timeout`, `foreign_keys` on; single-connection write pool; mattn swappable by driver name | mattn (cgo); ncruces (wasm) | Windows + static binaries; PocketBase precedent |
| D4 | Postgres | `jackc/pgx/v5` via `stdlib` in `drivers/postgres` module | lib/pq | De-facto standard |
| D5 | Mongo / Redis | Separate modules; Mongo gets its own `Collection[T]` API (not the SQL builder); Redis backs cache/session/queue/locks | force Mongo into ORM | Different data models |

## HTTP features

| # | Topic | Decision | Alternatives | Rationale |
| --- | --- | --- | --- | --- |
| H1 | Validation | **`z`: Zod-inspired typed schemas** (`z.String().Min(3)`, `z.Struct[T](z.Shape{...})`) that parse untrusted input into typed values and check outgoing data; struct tags compile into the same schemas; exports to JSON Schema/OpenAPI and TypeScript. See [validation.md](validation.md) | tag-only validator; go-playground/validator | Maintainer requirement (2026-10-16): strongly typed checks on all data entering and leaving the app. Supersedes the earlier tag-only decision |
| H2 | Sessions | **Own implementation**: cookie store (AES-GCM, key via HKDF from `APP_KEY`), database, memory; Redis in driver module; flash + old input; `RenewToken` anti-fixation | alexedwards/scs | Integrates tightly with flash/validation/Inertia; small surface |
| H3 | CSRF | `http.CrossOriginProtection` (Go 1.25) + `SameSite=Lax` cookies; HTMX token via `hx-headers:inherited` when needed | gorilla/csrf | stdlib; gorilla/csrf had a 2025 CVE |
| H4 | Authentication | **Guard × UserProvider**; guards `session` and `access_token` (opaque, SHA-256 in DB, abilities); hasher argon2id (default) + bcrypt with `NeedsRehash`; `install:auth` generates login/register/reset/verify as user-owned code (Fortify-style actions) | JWT default for APIs | Opaque tokens are revocable and simple; JWT can be added later |
| H6 | REST APIs | **First-class**: `vukem new <app> --api` JSON-only starter; `APIResource`, typed resource transformers, problem+json errors, allowlisted filter/sort/include/fields, offset + cursor pagination envelopes, token auth, CORS, rate limit, ETag/If-Match; OpenAPI 3.1 from typed handlers in BETA. See [api.md](api.md) | web-only with JSON helpers | Maintainer requirement (2026-10-16): APIs, mainly REST, are a primary use case |
| H7 | Security | **Secure by default, fail closed, refuse to boot when misconfigured**; defaults mapped to OWASP Top 10:2025 with a `TestSecurity_*` test each. See [security.md](security.md) | opt-in hardening | Maintainer requirement (2026-10-16) |
| H8 | Social login | **Own `social` package, AdonisJS Ally-style**: OAuth2 code flow + PKCE, signed state/nonce cookie, OIDC ID-token verification, GitHub/Google/GitLab/Discord/Microsoft + generic OAuth2/OIDC; stdlib only. See [authentication.md](authentication.md#social-login-social) | `golang.org/x/oauth2` + goth; post-1.0 | Maintainer requirement (2026-10-16); moved from post-1.0 to ALPHA |
| H9 | Mail providers | **Resend first-class** (HTTP driver, stdlib `net/http`) alongside SMTP, log, array | SMTP only | Maintainer requirement (2026-10-16) |
| H10 | Telegram logging | **`slog.Handler` for Telegram** ported from the maintainer's TypeScript telegram-logger (per-level chats, HTML formatting, 429 retry, async, contact forms) | external log shippers only | Maintainer requirement (2026-10-16) |
| H5 | Authorization | **Gates + generic `Policy[T]`** registered by type: `authz.Authorize(c, "update", post)`; `Before()` hook; deny as 403 or 404 | gates only | Laravel/Bouncer parity without reflection discovery |

## Frontend

| # | Topic | Decision | Alternatives | Rationale |
| --- | --- | --- | --- | --- |
| F1 | Templates | **`html/template` default** (layouts + fragment rendering for HTMX); templ supported through `View.Render(ctx, w) error` with zero core dependency | templ default; html/template only | stdlib-first, but no lock-out of templ users |
| F2 | HTMX | htmx 4 vendored + served via `embed`. **Every starter ships Vite + Tailwind CSS v4 (`@tailwindcss/vite`) preconfigured**, so the HTMX starter needs Node too | No Node for HTMX (original decision); Tailwind standalone CLI; Tailwind opt-in flag | Maintainer requirement (2026-10-16): Tailwind ready out of the box with one tooling for HTMX and Inertia |
| F3 | HTMX helpers | Own v4-aware helpers: `IsHTMX`, `IsPartial` (`HX-Request-Type`), history-restore detection, auto fragment/layout rendering, response header builder, `<hx-partial>`, 422 flow, `Vary: HX-Request` | angelofallars/htmx-go (htmx 2 era) | Existing libs predate v4 |
| F4 | Inertia | **Own v3 adapter in BETA** (full protocol, CSR fallback on SSR failure, integrated with session/flash/validation/Vite); React, Vue and Svelte starters ship together in B.3.6 | ALPHA; React only; Vue/Svelte later (B.9) | Maintainer requirement (2026-10-16): pick the Inertia framework during `vukem new` |
| F5 | Vite | Laravel "hot file" model; `{{ vite "…" }}` template func; manifest hash = Inertia version | olivere/vite | Go can't host Vite in-process |
| F6 | Starter choice | `vukem new` asks for the stack on a TTY (HTMX, Inertia, API; with Inertia: React, Vue or Svelte) using stdlib prompts; flags (`--api`, `--inertia=react\|vue\|svelte`) skip the prompt; no TTY uses the HTMX default | Flags only | Maintainer choice (2026-10-16); no prompt dependency |
| F7 | Debug error page | Own youch-style page (no dependency): error chain via `Unwrap`, stack frames with source snippets, request (secrets redacted), route and app info; ANSI variant for the terminal. Stacks come from recovered panics and from `errs.New`/`Wrap`/`WithStack`, which call `runtime.Callers` on every error (cheap, error paths only, no global switch); the page itself only renders with `Services.Debug`, never in production | Stack only for panics; port of youch | Maintainer choice (2026-10-16): better debugging DX like AdonisJS |

## Quality & tooling

| # | Topic | Decision | Alternatives | Rationale |
| --- | --- | --- | --- | --- |
| Q1 | Test assertions | **stdlib + `go-cmp`** in framework tests; framework ships fluent HTTP test client and fakes | testify | Google Go Style; users don't need testify |
| Q2 | Integration tests | `//go:build integration` + env URLs (`DATABASE_URL`, `REDIS_URL`, `MONGO_URL`); CI service containers; local docker compose | testcontainers-go | No extra dep |
| Q3 | Lint/format | golangci-lint v2 (gofumpt + gci + golines @120, tab-len 4) | — | Community standard |
| Q4 | Hooks | lefthook: pre-commit (fmt, lint, `go mod tidy -diff`, markdownlint), commit-msg (Conventional Commits), pre-push (test, vet, govulncheck) | pre-commit (Python), husky (Node) | Go binary, Windows-friendly |
| Q5 | Commit lint | Own ~60-line validator in Go (`go run ./internal/tools/commitlint`) + PR-title check in CI | @commitlint/cli | No Node for commits |
| Q6 | Task runner | Taskfile (go-task v3) | Makefile; mage | Identical behaviour on Windows |
| Q7 | Changelog | git-cliff → Keep a Changelog **1.1.0** | release-please; KaC 2.0.0 | Exact KaC format |
| Q8 | Release | GoReleaser v2 for CLI binaries (changelog from git-cliff) | — | Cross-compile, checksums, SBOM |
| Q9 | Markdown | markdownlint-cli2 (MD010 ignores `go` fences; MD024 siblings-only; MD013 off) | rumdl | Requested; Node already present |
| Q10 | CI | GitHub Actions: lint, test matrix (ubuntu/windows/macos × stable; oldstable once Go 1.28 ships), integration (PG/Redis/Mongo services), govulncheck, markdownlint, semantic PR title, CodeQL, Dependabot | — | — |
| Q11 | Agent config | `AGENTS.md` canonical (≤50 lines, links to docs), `CLAUDE.md` imports it; `.claude/rules/*` path-scoped; `.claude/settings.json` with permission allowlist + PostToolUse format hook | rules only | Maintainer approved |
| Q12 | Comparative benchmarks (B.5.3) | Own module `internal/bench` (in `go.work`, not in `MODULES`) depending on chi and echo; run by `task bench` and a manual `Bench` workflow; results in [performance.md](performance.md) | Benchmarks in core tests; third-party benchmark suites | A comparison needs the competitors as code, and keeping them in a never-published module leaves the core's `go.mod` untouched |
| Q13 | 1.0 announcement (S.3.2) | README and the GitHub release only; posts to Go community channels (r/golang, Gophers Slack, golang-nuts) are the maintainer's call, outside the plan | Agent-written posts on community channels | Maintainer decision (2026-10-17): those channels speak for the maintainer |
| Q14 | 1.0 validation | No release candidates: v1.0.0 follows the latest v0.x once three maintainer projects (Forms, Go interview quiz, Rifa) run in production on Railway, each in its own repository using the published modules; payments only in Stripe and AbacatePay sandboxes | Two RCs with no API changes between them | Maintainer decision (2026-10-17): real apps exercise the API better than an RC soak |

## Phase scope

| Phase | Version | Scope |
| --- | --- | --- |
| Prototype | v0.1.x | JSON binding/responses + problem+json, router, config/env, App+providers, CLI (`new`, `serve`, `make:controller/model/migration`), ORM core + migrations on SQLite, validation, sessions + flash, CSRF, `html/template` + HTMX helpers, error handler, test client, health `/up`, graceful shutdown |
| ALPHA | v0.2–v0.x | API starter (`new --api`, `install:api`, resources, query conventions, pagination envelopes, CORS), full auth (session + token guards, `install:auth`), gates/policies, relations, factories/seeders, mail, cache, storage (`os.Root`), events, Postgres driver |
| BETA | v1.0.0-beta.N → rc.N | OpenAPI 3.1 generation, idempotency keys, queue (sync/database/redis), scheduler, Inertia v3 + React starter, Vite, Redis and Mongo drivers, i18n, SSE, encryption with key rotation, rate limiting; plus (maintainer requirement, 2026-10-16): GraphQL, WebSockets/broadcasting, embedded admin panel, local dev dashboard, JWT guard, TOTP 2FA, Apple/LinkedIn/X social providers, S3 storage driver, MySQL dialect, Vue/Svelte Inertia starters, templ-first starter, typed RPC client generation, Stripe payments, advanced file uploads, Scalar API docs, gRPC (connect-go) |
| 1.0 Stable | v1.0.0 | API freeze, docs complete, stability policy, LTS line |
| Post-1.0 | v1.x | Whatever the 1.0 users ask for; nothing scheduled |

### BETA additions (2026-10-16)

The maintainer moved the post-1.0 backlog into BETA. Choices that keep the core rules (stdlib first, no core deps beyond the allowlist, drivers as modules):

| Feature | Decision |
| --- | --- |
| GraphQL | Own `graphql` module (`contrib/graphql`): schema-first SDL parser + executor (queries, mutations, variables, fragments, introspection), resolvers as typed Go funcs, dataloader-style batching over the ORM, depth/complexity limits, problem-style errors. No gqlgen/graphql-go dependency in core. |
| WebSockets/broadcasting | `drivers/websocket` module wrapping `github.com/coder/websocket` (stdlib has no RFC 6455 server); core `broadcast` package with channels, private/presence authorization through gates, memory and Redis fan-out, SSE fallback. Refined in B.6.2: public channels must be declared too (fail closed, unlike Laravel); presence `here` lists are per instance until a shared store lands. |
| Admin panel | Embedded HTMX admin (`admin` package): resources declared in Go from ORM models (list, filter, sort, search, create/edit forms from `z` schemas, delete), protected by auth + policies, served under `/admin`, no Node. |
| Dev dashboard | `devtools` package mounted at `/_vukem` only when `APP_ENV=development` (boot error otherwise): routes, last requests with timings, SQL queries (with N+1 detection), jobs, events, mail previews, slog records, traces; in-memory ring buffers. |
| JWT guard | Stdlib JOSE subset in `auth/jwt`: HS256/RS256/ES256/EdDSA, `alg` allowlist (no `none`), `kid` + JWKS rotation, exp/nbf/iat/iss/aud checks, refresh tokens with rotation and reuse detection. |
| TOTP 2FA | `auth/totp` (RFC 6238/4226, stdlib HMAC): enrolment QR (otpauth URI), recovery codes (hashed), replay protection per time step, rate limited; `install:auth --2fa`. |
| Social providers | Apple (ES256 client secret JWT, form_post), LinkedIn (OIDC), X (OAuth 2.0 + PKCE) on top of the ALPHA `social` core. |
| S3 storage | `drivers/s3` module with a stdlib SigV4 client (no AWS SDK): put/get/delete/list/stat, multipart upload, presigned URLs; tested against MinIO; works with R2/Spaces. |
| MySQL | `drivers/mysql` module (`go-sql-driver/mysql`) with MySQL 8 / MariaDB 11 dialect; runs the shared ORM/schema conformance suite. |
| Inertia Vue/Svelte | `install:inertia --vue` / `--svelte` starters with the same pages and tests as React. |
| templ starter | `vukem new <name> --templ`: same app as the HTMX starter with templ components; templ is a dependency of the generated app only. |
| Typed RPC client | `vukem codegen:client` emits a typed TypeScript (and Go) client from the typed handlers/OpenAPI document (B.0), with `z`-derived request/response types. |
| Stripe (2026-10-16) | `contrib/stripe` module with an own stdlib client (stripe-go is large and versioned per API release): Checkout, Billing Portal, subscriptions, signed webhooks into `events.Bus`, `install:billing` scaffold, fake Stripe API for tests. |
| File uploads (2026-10-16) | Builds on `storage`: streaming multipart, per-route limits, tus-style resumable uploads, presigned direct-to-S3, `z` file rules, htmx progress component, optional virus-scan hook. Implementation choices (B.10.4): a later `web.LimitBody` replaces the router-wide body limit instead of nesting under it (so a route or `upload.Rules` can raise it); `MethodOverride` reads `_method` from the query string for multipart forms so their bodies stay unread; tus and presign handlers require an `Authorize` hook (boot error when missing). |
| API docs UI (2026-10-16) | Scalar API Reference (scalar.com) renders the generated OpenAPI 3.1 document; assets vendored and hash-pinned like htmx, CSP-compatible, disabled in production unless enabled. |
| gRPC (2026-10-16) | `contrib/grpc` on connect-go: it is plain `net/http`, so gRPC, gRPC-Web and Connect share the router, middleware and auth; google.golang.org/grpc's separate server is not needed. Codegen through `buf`/`protoc-gen-connect-go` run with `go tool`. |
| Image thumbnails (2026-10-17) | `contrib/imaging` on `golang.org/x/image` v0.47.0 (approved by the maintainer while building the marketplace showcase): the stdlib has no WebP decoder and only nearest-neighbour scaling. A separate module keeps the core free of the dependency. |
| Feature flags (2026-10-17, A29) | Core `flags` package, shipped in v0.27.0 so it freezes with v1. Flags are typed values declared in code (`flags.Bool`, `flags.Int`, `flags.String`) with a default, optionally `Lock()`ed, and registered with `vukem.WithFlags`; invalid or duplicate names and bad `FLAG_<NAME>` values are boot errors, and so are `FLAG_*` keys in `.env` files that name no flag and env overrides of locked flags. Resolution, lowest first: code default, `FLAG_<NAME>`, stored global value, stored value for the request scope (`flags.WithScope`, one string such as `user:42` or `tenant:7`); locked flags always have their code default. Stored values live in `feature_flags` (`flags.Migration`, `vukem install:flags`), read through the app cache with a 30 s TTL and forgotten on every write. Fail closed: a store or cache error returns the default (code default or `FLAG_<NAME>`) and logs a warning, never an error to the request. The admin panel gets a Flags page (`admin.Config.Flags`, ability `manageFlags`) that writes `web.AuditEvent` and an admin audit-log row. Views and Inertia only see the flags passed to `Expose`/`Share` (allowlist). API review before v1 (2026-10-17): `flags.Type` constraint, `flags.Scope` validated on read and write (`a-z 0-9 : . _ -`, 1-191 bytes), `All()` returns `[]flags.Info` (`AnyFlag` stays sealed, for registration only), `Change.Sensitive` for string flags (audited as length + SHA-256 prefix), cache keys carry a per-scope generation bumped on write, generic `Get`/`Put` stay functions (methods cannot be generic); a transactional store interface can be added later as an optional interface. Not in scope: percentage rollouts, A/B variants, rule engines and per-flag resolver closures (Pennant's `define` callbacks); an app writes those as code over `flags.WithScope`. Rejected: Pennant-style closures resolved per scope and stored lazily (more API to freeze, and a hidden write on read) and a registry filled by `init()`. Source: the marketplace app's `app/flags`, which this package replaces. |
| SQLite stays in the core module (2026-10-24, ARQ-01) | `modernc.org/sqlite` remains in the core `go.mod`: zero-config development (`vukem new` + `vukem serve` with no database to install) outweighs the cost. Trade-off accepted: a larger core dependency graph and a bigger binary for apps that only use Postgres or MySQL. Revisit only if a driver-style split can keep `vukem new` working with no setup. |
| WebP output (2026-10-24, FUN-08) | `contrib/imaging` decodes WebP (`golang.org/x/image/webp`) but encodes PNG and JPEG only: `x/image` has no WebP encoder and cgo `libwebp` bindings break the no-cgo rule. A WebP encoder needs an external pure-Go or WASM package or an own VP8L (lossless) encoder; either is a new dependency or sizeable code, so it waits for a decision under the [dependency policy](architecture.md#dependency-policy). Pending. |

## Open questions

None — all branches of the design tree were resolved on 2026-10-16. New questions are appended here as they arise.
