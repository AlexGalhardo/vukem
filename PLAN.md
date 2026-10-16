# Lumek — Plan

The single source of truth for **what to build next**. Every work session (human or agent) starts here.

## How to use this file

1. Read [AGENTS.md](AGENTS.md) (rules) and the docs linked from the task you pick.
2. Find the **first unchecked task** in the **current phase** (see [Status](#status)). Tasks within a group are ordered; groups may run in parallel unless marked `needs:`.
3. Work test-first. A task is done only when its **→ verify** line passes and the [Definition of Done](#definition-of-done) holds.
4. Tick the box (`- [x]`), update [Status](#status), and commit with a Conventional Commit whose scope matches the group.
5. Never start a task from a later phase while the current phase's exit criteria are unmet.

Task IDs: `<phase>.<group>.<task>` (e.g. `P.3.2`). Reference them in commit bodies (`Refs: P.3.2`).

## Status

| Field | Value |
| --- | --- |
| Current phase | **Prototype** |
| Next task | `P.3.1` (P.2.5 after the router; P.0.9 waits for GitHub billing, CI runs locally via `task ci:full`) |
| Last release | `v0.0.1` (foundation, 2026-10-16) |
| Updated | 2026-10-16 |

## Design references

| Topic | Doc |
| --- | --- |
| Decisions log | [docs/brainstorming.md](docs/brainstorming.md) |
| Research | [docs/golang-web-search.md](docs/golang-web-search.md) |
| Architecture | [docs/architecture.md](docs/architecture.md) |
| Configuration | [docs/configuration.md](docs/configuration.md) |
| HTTP (routing, ctx, middleware, validation, sessions, errors) | [docs/http.md](docs/http.md) |
| Typed schemas `z` (validation in and out) | [docs/validation.md](docs/validation.md) |
| REST APIs | [docs/api.md](docs/api.md) |
| Security (OWASP Top 10:2025 defaults) | [docs/security.md](docs/security.md) |
| Databases | [docs/database.md](docs/database.md) |
| Auth | [docs/authentication.md](docs/authentication.md) |
| Frontend (views, HTMX, Inertia, Vite) | [docs/frontend.md](docs/frontend.md) |
| CLI | [docs/cli.md](docs/cli.md) |
| Subsystems (mail, cache, queue…) | [docs/subsystems.md](docs/subsystems.md) |
| Testing | [docs/testing.md](docs/testing.md) |
| Tooling & releases | [docs/tooling.md](docs/tooling.md) |

## Definition of Done

Applies to **every** task:

- [ ] Tests written first and passing: `task test` (and `task test:integration` when DB drivers are touched).
- [ ] `task lint` clean (golangci-lint + markdownlint), no new `nolint` without explanation.
- [ ] Exported identifiers documented (godoc, full sentences); runnable `Example` for new public APIs.
- [ ] Coverage of touched packages does not drop below the gate in [docs/testing.md](docs/testing.md#policy-framework).
- [ ] No new third-party dependency unless listed in [docs/architecture.md](docs/architecture.md#dependency-policy) (update it + brainstorming otherwise).
- [ ] Core does not import `drivers/*`; no package-level mutable state; no `init()` registration.
- [ ] Every security default the task introduces (see [docs/security.md](docs/security.md)) has a `TestSecurity_<Axx>_<Behaviour>` test; unsafe options are opt-out with an explicit name; failures deny (fail closed).
- [ ] Relevant doc in `docs/` updated if behaviour or API changed.
- [ ] Conventional Commit; `CHANGELOG.md` regenerated at release time only.

## Phase overview

| Phase | Version | Goal | Exit criteria |
| --- | --- | --- | --- |
| **Prototype** | `v0.1.x` | Smallest end-to-end framework: `lumek new blog` → CRUD with HTMX on SQLite, plus JSON endpoints with problem+json errors, tested | [P exit](#prototype-exit-criteria) |
| **ALPHA** | `v0.2`–`v0.x` | REST API starter, auth, authorization, relations, Postgres, mail, cache, storage, events | [A exit](#alpha-exit-criteria) |
| **BETA** | `v1.0.0-beta.N` → `rc.N` | OpenAPI, queue, scheduler, Inertia + React, Vite, Redis, Mongo, i18n, SSE, encryption | [B exit](#beta-exit-criteria) |
| **1.0 Stable** | `v1.0.0` | API freeze, docs site, stability policy, LTS | [S exit](#10-stable-exit-criteria) |

---

## Phase 1 — Prototype (`v0.1.x`)

**Goal:** a developer installs the CLI, creates an app, and ships a CRUD page with forms, validation, flash messages and HTMX partials on SQLite, plus JSON endpoints for the same data — everything covered by tests.

### P.0 Repository foundation

- [x] **P.0.1** Initialise module `github.com/AlexGalhardo/golang-framework` (`go 1.27.0`), root `doc.go` (`package lumek`).
  → verify: `go build ./...` succeeds.
- [x] **P.0.2** Add `.editorconfig`, `.gitattributes`, `.gitignore`.
  → verify: `git ls-files --eol` shows `i/lf` for text files.
- [x] **P.0.3** Add `.golangci.yml` (v2, formatters + linters + depguard per [tooling.md](docs/tooling.md#formatting-and-linting)).
  → verify: `golangci-lint config verify` and `golangci-lint run ./...` pass.
- [x] **P.0.4** Add `.markdownlint-cli2.jsonc`.
  → verify: `npx markdownlint-cli2 "**/*.md"` passes.
- [x] **P.0.5** Implement `internal/tools/commitlint` (Conventional Commits 1.0 validator) with table-driven tests (valid, invalid type, missing colon, uppercase start, trailing period, breaking `!`, footer `BREAKING CHANGE:`, merge/revert/fixup exceptions, header > 100).
  → verify: `go test ./internal/tools/commitlint` ≥ 95% coverage.
- [x] **P.0.6** Add `lefthook.yml` (pre-commit, commit-msg, pre-push).
  → verify: a commit with message `bad message` is rejected; `feat(repo): add hooks` is accepted.
- [x] **P.0.7** Add `Taskfile.yml` (`setup`, `fmt`, `lint`, `test`, `test:race`, `test:integration`, `cover`, `vuln`, `changelog`, `hooks`, `ci`).
  → verify: `task ci` passes on Windows and in CI.
- [x] **P.0.8** Add `cliff.toml` (Keep a Changelog 1.1.0 mapping) and initial `CHANGELOG.md` with `[Unreleased]`.
  → verify: `git cliff --unreleased` renders KaC sections; markdownlint passes.
- [ ] **P.0.9** GitHub: `ci.yml`, `pr-title.yml`, `codeql.yml`, `dependabot.yml`, issue forms, PR template, `CODEOWNERS`.
  → verify: CI green on first push; a PR titled `update stuff` fails the title check.
- [x] **P.0.10** OSS files: `README.md`, `LICENSE` (MIT), `CONTRIBUTING.md`, `SECURITY.md`, `CODE_OF_CONDUCT.md`.
  → verify: markdownlint passes; GitHub detects MIT license.
- [x] **P.0.11** Agent config: `AGENTS.md` (≤ 50 lines), `CLAUDE.md`, `.claude/rules/*`, `.claude/settings.json`.
  → verify: `AGENTS.md` ≤ 50 lines; markdownlint passes.

### P.1 Config & environment — needs: P.0

- [x] **P.1.1** `.env` parser (quotes, escapes, comments, `export`, `${VAR}` interpolation, `file:` prefix).
  → verify: table tests + `FuzzParse` runs 30s without crash.
- [x] **P.1.2** Source precedence (`getenv` > `.env.<env>.local` > `.env.local` > `.env.<env>` > `.env`; `.env.local` skipped in test).
  → verify: tests with a fake FS and fake `getenv`.
- [x] **P.1.3** `config.Load[T]`: `env`/`default` tags, supported types, aggregated errors.
  → verify: tests for every supported type and an error list with ≥ 2 failures.
- [x] **P.1.4** `config.Secret` (redaction in `String`, `slog`, JSON, `%v`).
  → verify: tests asserting `[redacted]` in each path.
- [x] **P.1.5** `APP_KEY` decoding + HKDF sub-key derivation helper.
  → verify: deterministic test vectors; boot fails without key.

### P.2 App & lifecycle — needs: P.1

- [x] **P.2.1** `App` struct, `New(opts...)`, functional options.
  → verify: unit tests for option application.
- [x] **P.2.2** Provider interfaces (`Register`, `Booter`, `Starter`, `Shutdowner`) + environment filtering.
  → verify: order test (register all → boot all → start; shutdown reverse).
- [x] **P.2.3** Runner orchestration with `errgroup` + `signal.NotifyContext`; graceful shutdown with timeout.
  → verify: `synctest` test: SIGINT triggers shutdown in reverse order within timeout.
- [x] **P.2.4** HTTP server runner with mandatory timeouts and `BaseContext`.
  → verify: test asserts non-zero timeouts; in-flight request completes during shutdown.
- [ ] **P.2.5** `run(ctx, args, getenv, stdout)` pattern helper for `cmd/app`.
  → verify: end-to-end test boots app on random port and polls `/up`.

### P.3 Router & ctx — needs: P.2

- [ ] **P.3.1** Route registration over `http.ServeMux` (verbs, params, `{path...}`, `{$}`).
  → verify: table tests per verb/pattern; `FuzzPattern`.
- [ ] **P.3.2** Groups (prefix, nested, middleware, name prefix).
  → verify: tests for nesting 3 levels.
- [ ] **P.3.3** Named routes + `URLFor` (missing/extra params error) + conflict errors naming both routes.
  → verify: tests including conflict case.
- [ ] **P.3.4** `Resource` / `APIResource` with interface detection, `Only/Except`.
  → verify: controller implementing 3 of 7 actions registers exactly 3 routes.
- [ ] **P.3.5** `*Ctx`: request helpers, responses (`String`, `HTML`, `JSON`, `NoContent`, `Redirect`, `RedirectRoute`, `Back`, `File`, `Download`, `Stream`).
  → verify: `httptest` tests for each response type (status, headers, body).
- [ ] **P.3.6** `Bind[T]` for JSON, form, multipart, query (+ `MaxBytesReader`).
  → verify: tests per content type; oversized body → 413.
- [ ] **P.3.7** Adapters `Adapt`/`Wrap` between `HandlerFunc` and `http.Handler`.
  → verify: chi-style third-party middleware works in a test.
- [ ] **P.3.8** Three middleware stacks (server, router, named) + `MethodOverride`.
  → verify: unmatched route runs only server stack; `_method=DELETE` routed correctly.
- [ ] **P.3.9** `route:list` data provider (`[]RouteInfo`).
  → verify: golden test of the table output.
- [ ] **P.3.10** Benchmarks for routing and ctx allocation.
  → verify: `task bench` runs; results recorded in PR description.

### P.4 Errors & built-in middleware — needs: P.3

- [ ] **P.4.1** `errs` types (`HTTPError`, sentinels, `ValidationError`) + `errors.Is/AsType` support.
  → verify: unit tests.
- [ ] **P.4.2** Central handler with content negotiation (HTML / problem+json RFC 9457 / HTMX).
  → verify: golden tests per format.
- [ ] **P.4.3** Status pages (`errors/<status>.html` fallback to built-ins) and dev debug page (stack, request, redacted config).
  → verify: debug page never rendered when `APP_ENV=production` (test).
- [ ] **P.4.4** Middleware: `Recover`, `RequestID`, `Logger` (slog), `Timeout`, `CORS` (deny by default), `Static` (`os.Root`, immutable cache for hashed files).
  → verify: tests each; path traversal attempt on `Static` → 404.
- [ ] **P.4.5** `/up` health route.
  → verify: test returns 200 after boot.
- [ ] **P.4.6** JSON response helpers (`JSON`, `Created` + `Location`, `Accepted`, `NoContent`, `Problem`), 405 with `Allow`, automatic `HEAD`, `api.JSONOnly()` (415, body limit).
  → verify: the status table in [docs/api.md](docs/api.md#errors) is covered by tests.
- [ ] **P.4.7** `ETag`/`If-None-Match` (304) and `If-Match` (412) helpers, gzip middleware.
  → verify: conditional request tests.
- [ ] **P.4.8** Security headers middleware (nosniff, frame options, referrer, COOP, permissions policy, HSTS on HTTPS) + CSP builder with per-request nonce (`cspNonce` template func).
  → verify: `TestSecurity_A02_*` header tests; nonce differs per request.
- [ ] **P.4.9** Boot-time misconfiguration guard: refuse `APP_DEBUG` in production, short/missing `APP_KEY`, CORS `*` + credentials, insecure cookies in production; `MaxHeaderBytes` set.
  → verify: one failing-boot test per rule.
- [ ] **P.4.10** Security event logging (`event` attribute: `csrf.rejected`, `session.invalid`, `ratelimit.exceeded`…) and redaction of `Authorization`/`Cookie` headers and secrets.
  → verify: log capture tests assert events present and secrets absent.

### P.5 Typed schemas `z` (validation) — needs: P.3

- [ ] **P.5.1** Core: `Schema[T]` interface, `Issues`/`Issue` (path, code, message, params), immutable modifiers (`Optional`, `Nullable`, `Default`, `Refine`, `Transform`, `Message`, `Describe`).
  → verify: unit tests for each modifier; schemas safe for concurrent use (`-race`).
- [ ] **P.5.2** Primitives: `String` (+ checks/transforms), integer family, `Float`, `Bool`, `Time`, `Enum`, with lossless coercion from strings.
  → verify: table tests per check (pass/fail/edge) and per coercion.
- [ ] **P.5.3** Composites: `Slice`, `Map`, `Struct[T](Shape)` with field mapping by `json`/`form` tag, type compatibility checked at construction, `Strict()`, nested paths in issues.
  → verify: mismatched shape panics at construction; nested issue paths `a.b.2.c`.
- [ ] **P.5.4** `FromTags[T]`, `Register[T]`/`For[T]` registry, `FieldError` helper, `ParseJSON(io.Reader, schema)`.
  → verify: tag schemas equal hand-written ones on a shared test corpus; `FuzzParse` on JSON input.
- [ ] **P.5.5** Web integration: `web.Parse`, `web.Bind[T]`, `ParseQuery`, `ParseParams` (form arrays/brackets), `JSONWith` output checks; issues → `*errs.ValidationError` → 303+flash / 422 / problem+json.
  → verify: three end-to-end tests (form, HTMX, JSON) + output mismatch test.

### P.6 Sessions & CSRF — needs: P.1, P.3

- [ ] **P.6.1** Session manager (lazy load, dirty write, regenerate, invalidate, idle/absolute timeout).
  → verify: `synctest` tests for expiry.
- [ ] **P.6.2** Stores: cookie (AES-GCM, 4 KB guard), memory, database (SQLite).
  → verify: shared store conformance test suite run against each store.
- [ ] **P.6.3** Flash, reflash, old input, errors bag.
  → verify: two-request tests.
- [ ] **P.6.4** CSRF: `http.CrossOriginProtection` wiring + config (trusted origins, bypass patterns) + optional token fallback.
  → verify: cross-origin POST rejected; same-origin accepted; webhook bypass works.

### P.7 Database, ORM & migrations (SQLite) — needs: P.1

- [ ] **P.7.1** `database.Manager`, SQLite connection with pragmas, writer/reader pools, `BEGIN IMMEDIATE`.
  → verify: concurrent write test (50 goroutines) without `SQLITE_BUSY`.
- [ ] **P.7.2** Dialect interface (placeholders, quoting, type mapping) + SQLite dialect.
  → verify: unit tests for SQL generation.
- [ ] **P.7.3** Model metadata cache (table/column naming, `Model`, `SoftDeletes`, `ULIDModel`, optional interfaces).
  → verify: tests for naming conventions and overrides.
- [ ] **P.7.4** `Query[T]`: where family, select/join/group/having/order/limit/offset, unknown-column check.
  → verify: golden SQL tests + execution tests on SQLite.
- [ ] **P.7.5** Reads: `Get`, `First`, `Find`, `Count`, `Exists`, `Pluck[V]`, `Each` (`iter.Seq2`), `Chunk`, `Paginate`.
  → verify: tests incl. early `break` in `Each` closes rows (`sqlclosecheck` clean).
- [ ] **P.7.6** Writes: `Create`, `Update`, `Query.Update`, `Delete` (soft), hooks.
  → verify: tests incl. timestamps and soft-delete scoping.
- [ ] **P.7.7** Transactions `db.Tx` (commit/rollback/panic) + executor interface for `db`/`tx`.
  → verify: panic inside tx rolls back (test).
- [ ] **P.7.8** `orm.JSON[T]`, `sql.Null[T]` support.
  → verify: round-trip tests.
- [ ] **P.7.9** Schema builder (create/alter/drop, column types, modifiers, foreign keys, indexes, `Raw`) for SQLite incl. table rebuild for unsupported `ALTER`.
  → verify: golden DDL tests + apply on SQLite.
- [ ] **P.7.10** Migration runner (batches, up/down, status, reset, fresh, step) with registry slice.
  → verify: tests for each command on a temp DB.
- [ ] **P.7.11** Injection hardening: identifiers validated against model metadata, `WhereRaw`/`Raw` require placeholders, validator rejects invalid UTF-8/control chars.
  → verify: `TestSecurity_A05_*` including payloads from the OWASP SQLi cheat sheet; fuzz `FuzzWhere`.
- [ ] **P.7.12** Migration checksums; `migrate:status` reports modified applied migrations.
  → verify: test edits an applied migration and sees the warning.

### P.8 Views & HTMX — needs: P.3

- [ ] **P.8.1** `view.Engine` over `html/template` with `fs.FS`, per-page sets, layouts, partials, dev reload.
  → verify: golden render tests; reload test modifies a temp file.
- [ ] **P.8.2** Fragment rendering (`Fragment`, `htmx.Block`).
  → verify: tests full vs partial request.
- [ ] **P.8.3** Template funcs (`route`, `asset`, `csrfField`, `old`, `error(s)`, `flash`, `json`, `dict`) + shared data.
  → verify: tests each func.
- [ ] **P.8.4** `View` interface for templ components (`c.View`).
  → verify: test with a hand-written `View` implementation (no templ dependency).
- [ ] **P.8.5** `htmx` request helpers (v4 headers) + response builder + `<hx-partial>`/OOB helpers + `Vary`.
  → verify: header table tests; golden HTML for partials.
- [ ] **P.8.6** Redirect behaviour (`HX-Location` for htmx, 303 otherwise).
  → verify: tests.

### P.9 Testing toolkit — needs: P.3, P.6, P.7

- [ ] **P.9.1** `lumektest.New(t, bootFn)` with test env, in-memory SQLite, migrations.
  → verify: used by ≥ 3 framework tests.
- [ ] **P.9.2** Fluent client (requests, status/redirect/body/session/HTMX assertions) with rich failure output.
  → verify: self-tests using a fake `testing.TB` that records failures.
- [ ] **P.9.3** DB helpers (`RefreshDatabase`, `AssertDatabaseHas/Missing/Count`).
  → verify: tests.
- [ ] **P.9.4** `FakeClock` (injectable `clock.Clock` used by sessions).
  → verify: session expiry test via fake clock.
- [ ] **P.9.5** API assertions: `WithToken`, `GetJSON`, `PostJSON`, `AssertJSONPath`, `AssertJSONCount`, `AssertProblem`, `AssertJSONValidationErrors`.
  → verify: self-tests with a fake `testing.TB`.

### P.10 CLI — needs: P.2

- [ ] **P.10.1** `cmd/lumek` skeleton: command registry, `flag`-based parsing, `list`, `version`, help, exit codes.
  → verify: golden help output.
- [ ] **P.10.2** Stub engine (`embed.FS`, local `stubs/` override, naming helpers, `go/format`).
  → verify: golden tests per stub.
- [ ] **P.10.3** Codemod engine (`go/ast` append to known slices/functions + imports; fallback snippet).
  → verify: golden tests incl. missing-marker fallback.
- [ ] **P.10.4** Generators: `make:controller [--resource|--api]`, `make:model [-m -f -c]`, `make:migration`, `make:middleware`, `make:request`, `make:command`, `make:provider`, `make:test`.
  → verify: each generator in a temp project then `go build ./...` + `go vet ./...` pass.
- [ ] **P.10.5** `key:generate`.
  → verify: writes valid base64 32-byte key; refuses overwrite without `--force`.
- [ ] **P.10.6** Delegation to app binary (`go run ./cmd/app <cmd>`), with cached build.
  → verify: `lumek migrate` in a temp project runs migrations.
- [ ] **P.10.7** App commands: `migrate*`, `route:list`, `about`, user `Command` interface.
  → verify: tests via `run(ctx, args, …)`.
- [ ] **P.10.8** `serve` with polling watcher, rebuild/restart, template reload, optional `npm run dev`.
  → verify: e2e test edits a handler and observes new response within 5s.
- [ ] **P.10.9** `build` (optional `vite build`, `go build -trimpath`).
  → verify: e2e build produces runnable binary.

### P.11 Starter & `lumek new` — needs: P.4–P.10

- [ ] **P.11.0** Vendored htmx with pinned version and SHA-256 checked by a test; strict CSP in starter layout.
  → verify: tampered file fails the test; starter pages render with no CSP violations (headless check in e2e).
- [ ] **P.11.1** HTMX + SQLite starter (skeleton per [brainstorming.md](docs/brainstorming.md#generated-app-skeleton)), vendored htmx 4.x, minimal CSS, welcome page, example `PostController` CRUD with validation + flash + partials, tests.
  → verify: starter's own tests pass.
- [ ] **P.11.2** `lumek new <name> [--module]`: copy starter, rewrite module path, `go mod tidy`, `key:generate`, `git init`.
  → verify: e2e on ubuntu/windows/macos: `lumek new blog && cd blog && go test ./... && lumek build`.

### P.12 Release `v0.1.0` — needs: P.0–P.11

- [ ] **P.12.1** GoReleaser config + `release.yml`.
  → verify: `goreleaser release --snapshot --clean` builds all targets.
- [ ] **P.12.2** README quick start validated by following it on a clean machine/container.
  → verify: checklist in PR.
- [ ] **P.12.3** Tag `v0.1.0`, warm proxy, verify `go install …/cmd/lumek@v0.1.0`.
  → verify: pkg.go.dev shows docs and MIT license.

### Prototype exit criteria

- [ ] `go install github.com/AlexGalhardo/golang-framework/cmd/lumek@latest` works on Windows, Linux, macOS.
- [ ] `lumek new blog` → `lumek serve` → CRUD with HTMX partials, validation errors (422), flash messages — **no Node required**.
- [ ] The starter's example controller also exposes JSON endpoints that return problem+json on errors, covered by API assertions.
- [ ] Core coverage ≥ 80% overall; router, validate, session, orm, schema, config ≥ 90%.
- [ ] Zero third-party deps in core except `modernc.org/sqlite` and `golang.org/x/crypto`.
- [ ] CI green on the full matrix; `govulncheck` clean.
- [ ] Router benchmark: < 1 µs/op and ≤ 2 allocs for a static route on CI hardware (documented baseline).
- [ ] `go test -run TestSecurity ./...` covers every **P** row of [docs/security.md](docs/security.md); `gosec` and `govulncheck` clean.

---

## Phase 2 — ALPHA (`v0.2`–`v0.x`)

**Goal:** real apps with users, permissions, relations, Postgres, mail, cache, storage and events.

### A.0 REST API starter — needs: P.4, P.5, P.9

- [ ] **A.0.1** `APIResource`, nested and `Shallow()` resources.
  → verify: route table golden test.
- [ ] **A.0.2** Resource transformers (`make:resource`, `api.Map`) and pagination envelopes for offset and cursor pages (`orm.CursorPaginate`).
  → verify: golden JSON tests.
- [ ] **A.0.3** Query conventions (`api.Filters`, `Sorts`, `Includes`, `Fields`, `PerPage`) with allowlists and a 400 problem on unknown params.
  → verify: table tests including SQL-injection attempts in sort/filter values.
- [ ] **A.0.4** CORS middleware (deny by default, preflight, credentials) and API rate limiting per token/IP.
  → verify: preflight and 429 tests.
- [ ] **A.0.5** Unique-violation → 409 mapping per dialect; `Deprecation`/`Sunset` header helper.
  → verify: tests on SQLite and Postgres.
- [ ] **A.0.6** `lumek new <name> --api` starter and `lumek install:api` (token issue/revoke endpoints, `routes/api.go`, tests). needs: A.3.4
  → verify: e2e: generated API app tests pass on all OSes.

### A.1 Postgres driver module

- [ ] **A.1.1** `drivers/postgres` module (pgx stdlib), dialect, `go.work` wiring, separate CI job.
  → verify: `GOWORK=off go test ./...` inside module; integration suite passes against `postgres:17`.
- [ ] **A.1.2** Run the ORM/schema conformance suite against Postgres (shared suite with SQLite).
  → verify: same suite green on both dialects.
- [ ] **A.1.3** Database session store + migrations on Postgres.
  → verify: store conformance suite.

### A.2 Relations, factories, seeders — needs: P.7

- [ ] **A.2.1** `HasOne`, `HasMany`, `BelongsTo` with `With(...)` eager loading (batched `IN`).
  → verify: query-count assertions (1 + 1 per relation).
- [ ] **A.2.2** `ManyToMany` with pivot (`Attach`, `Detach`, `Sync`).
  → verify: tests on both dialects.
- [ ] **A.2.3** Strict mode `ErrNotLoaded`.
  → verify: test.
- [ ] **A.2.4** `factory` package (Faker subset, states, `Count`, `Make`, `Create`, relations).
  → verify: tests.
- [ ] **A.2.5** Seeders + `db:seed`, `db:wipe`, `make:factory`, `make:seeder`.
  → verify: e2e in temp project.
- [ ] **A.2.6** DB validation rules `unique`, `exists`.
  → verify: tests.

### A.3 Authentication — needs: P.6, A.2

- [ ] **A.3.1** Hashers (argon2id default, bcrypt) + `NeedsRehash` + fake.
  → verify: PHC round-trip tests; 72-byte bcrypt guard.
- [ ] **A.3.2** `Authenticatable`, `UserProvider`, `ORMProvider`.
  → verify: tests.
- [ ] **A.3.3** Session guard (attempt, login, logout, regenerate, timing-safe miss, remember-me).
  → verify: tests incl. session fixation check.
- [ ] **A.3.4** Access-token guard (create, hash, abilities, expiry, revoke, `last_used_at`).
  → verify: tests.
- [ ] **A.3.5** Middleware `Required`, `Guest`, `Verified`, `Ability`.
  → verify: tests.
- [ ] **A.3.6** `install:auth` scaffold (controllers, actions, views, migrations, routes, tests) incl. password reset + email verification.
  → verify: e2e: generated tests pass in a new app.
- [ ] **A.3.7** `ActingAs` in test client.
  → verify: tests.

### A.4 Authorization — needs: A.3

- [ ] **A.4.1** Gates (`Define`, `Allows`, `Authorize`, `Before`).
  → verify: tests.
- [ ] **A.4.2** `Policy[T]` registry by type, ability resolution at registration, `DenyAsNotFound`.
  → verify: tests.
- [ ] **A.4.3** `c.Authorize`, template `can`, `make:policy`.
  → verify: tests + generator e2e.
- [ ] **A.4.4** Fail-closed authorization (unregistered policy/ability denies) + `authz.denied` security event.
  → verify: `TestSecurity_A01_UnregisteredPolicyDenies`.

### A.5 Mail — needs: P.8

- [ ] **A.5.1** Mailable interface, SMTP (`net/smtp` + STARTTLS), log, array drivers.
  → verify: SMTP tested against an in-process fake SMTP server.
- [ ] **A.5.2** HTML + text views, attachments, `mail:preview` dev route, `make:mail`, `FakeMail`.
  → verify: tests.

### A.6 Cache, storage, events, rate limiting

- [ ] **A.6.1** Cache interface + memory (LRU/TTL) + database drivers; `Remember` with singleflight; locks.
  → verify: conformance suite; stampede test.
- [ ] **A.6.2** Storage `Disk` + local (`os.Root`) + memory; signed temporary URLs; uploads helper; `storage:link`.
  → verify: traversal tests; conformance suite.
- [ ] **A.6.3** Typed events (`Listen[T]`, `Dispatch`), async pool tracked for shutdown, `make:event`/`make:listener`, `FakeEvents`.
  → verify: tests.
- [ ] **A.6.4** Rate limiter (memory) + middleware + headers; default login throttle.
  → verify: `synctest` tests.

### A.6b Security features

- [ ] **A.6b.1** `httpclient.Safe()` SSRF-safe client (post-DNS IP checks, redirect re-check, timeouts, size limits).
  → verify: `TestSecurity_A01_SSRF*` against loopback, private, link-local, metadata and DNS-rebinding cases.
- [ ] **A.6b.2** Signed URLs (HMAC + expiry) and `webhook.VerifyHMAC`.
  → verify: tampered/expired URL and bad signature rejected in constant time.
- [ ] **A.6b.3** `security.Event` stream (`events.Listen[security.Event]`) for alerting; login/lockout/token events.
  → verify: events emitted in auth flows.
- [ ] **A.6b.4** Lint guard: `math/rand` forbidden in `auth`, `session`, `crypt`, `token` packages (depguard rule).
  → verify: a test file importing it fails lint in CI.

### A.7 Release train

- [ ] **A.7.1** Each ALPHA group shipped as a minor (`v0.2.0`, `v0.3.0`…) with changelog.
  → verify: tags exist; `CHANGELOG.md` sections per release.
- [ ] **A.7.2** Example app `examples/blog` using auth + policies + relations on Postgres and SQLite.
  → verify: CI runs its tests on both.

### ALPHA exit criteria

- [ ] `lumek install:auth` produces a working login/register/reset/verify flow with passing generated tests.
- [ ] `lumek new shop --api` produces a token-authenticated JSON API with pagination, filtering, CORS, rate limiting and problem+json errors, with passing generated tests.
- [ ] ORM + schema conformance suite green on SQLite and Postgres.
- [ ] Mail, cache, storage, events, rate limiting each have a fake and docs.
- [ ] Core deps still within the allowlist; Postgres pulled only via `drivers/postgres`.
- [ ] Every **A** row of [docs/security.md](docs/security.md) has its `TestSecurity_*` test.
- [ ] Coverage gates hold; integration CI green.

---

## Phase 3 — BETA (`v1.0.0-beta.N` → `v1.0.0-rc.N`)

**Goal:** feature-complete 1.0 surface; API stabilisation begins.

### B.0 OpenAPI & API hardening

- [ ] **B.0.1** Typed handlers `api.Register(g, pattern, op, func(c, In) (Out, error))`.
  → verify: tests for binding, validation and response encoding.
- [ ] **B.0.2** OpenAPI 3.1 generator (schemas from `json`/`validate` tags, problem catalogue, security schemes), `/openapi.json`, `lumek openapi:export`.
  → verify: generated document validates against the OpenAPI 3.1 JSON Schema; golden test.
- [ ] **B.0.3** `AssertMatchesOpenAPI()` test helper.
  → verify: a mismatching response fails the assertion.
- [ ] **B.0.5** `z` exports: `JSONSchema()` and `lumek codegen:types` (TypeScript types + optional Zod schemas).
  → verify: golden JSON Schema/TS output; `tsc --noEmit` passes on generated types.
- [ ] **B.0.4** `Idempotency-Key` middleware (database/Redis store).
  → verify: a duplicate POST returns the stored response.

### B.1 Queue & scheduler

- [ ] **B.1.1** Queue interface, typed jobs (`Kind()`), registry, sync driver, options (retries, backoff, timeout, delay, queue name).
  → verify: tests.
- [ ] **B.1.2** Database driver (SQLite + Postgres `SKIP LOCKED`), `DispatchTx`, `failed_jobs`, `queue:work/retry/failed`, graceful drain.
  → verify: concurrency tests; transactional enqueue rollback test.
- [ ] **B.1.3** `make:job`, `FakeQueue`, queued mail/listeners.
  → verify: tests.
- [ ] **B.1.4** Scheduler (cron parser, `Every`, `DailyAt`, overlap locks), `schedule:run/work/list`.
  → verify: `synctest` tests; cron parser fuzzed.

### B.2 Redis & Mongo driver modules

- [ ] **B.2.1** `drivers/redis`: connection, cache store, session store, queue driver, rate limiter store, locks, SSE pub/sub.
  → verify: conformance suites against `redis:7` (and Valkey).
- [ ] **B.2.2** `drivers/mongo`: connection manager, `Collection[T]` (find/iterate/insert/update/delete/aggregate), health check.
  → verify: integration tests against `mongo:8`.

### B.3 Vite & Inertia

- [ ] **B.3.1** `vite` package: hot file, manifest (recursive imports, CSS, dedupe), `asset`, React Refresh, CSP nonce, SRI.
  → verify: golden tests for dev/prod tag output.
- [ ] **B.3.2** `inertia` core: page object, root template script, version check (409), 302→303, shared props, validation errors, redirects (`Location`, fragment).
  → verify: protocol tests per header/status.
- [ ] **B.3.3** Prop types (Optional, Always, Defer+rescue, Merge, DeepMerge, Prepend, MatchOn, Once, Scroll) with dotted-path recursion; partial reload filtering.
  → verify: table tests mirroring the protocol spec cases.
- [ ] **B.3.4** History encryption flags, flash, prefetch, Precognition.
  → verify: tests.
- [ ] **B.3.5** SSR client with timeout + CSR fallback + health + dev endpoint discovery; `ssr:start`.
  → verify: fallback test with a failing fake SSR server.
- [ ] **B.3.6** `install:inertia` React starter (Vite, TS, pages, layouts, auth pages) + `AssertInertia`.
  → verify: e2e: new Inertia app builds and its tests pass.
- [ ] **B.3.7** `codegen:routes` → typed `routes.ts` (warmup boot mode).
  → verify: golden TS output; `tsc --noEmit` passes in starter.

### B.4 Remaining subsystems

- [ ] **B.4.1** Encryption with key rotation; encrypted cookies; `orm.Encrypted[T]`.
  → verify: rotation tests.
- [ ] **B.4.2** i18n (JSON catalogs, plurals, locale resolution, translated validation messages, `t` func).
  → verify: tests.
- [ ] **B.4.3** SSE (`c.SSE`, hub, heartbeat, shutdown).
  → verify: `synctest` tests.
- [ ] **B.4.4** Maintenance mode (`down --secret`, `up`), `install:api` (token guard + API routes + problem+json).
  → verify: tests.
- [ ] **B.4.5** Optional OpenTelemetry module (`drivers/otel` or `contrib/otel`).
  → verify: span emitted per request in test exporter.

### B.5 Stabilisation

- [ ] **B.5.1** Public API review: every exported identifier justified; remove/rename before freeze; `//go:fix inline` for renamed APIs.
  → verify: `apidiff` report reviewed and committed under `docs/api/`.
- [ ] **B.5.2** Security review against [docs/security.md](docs/security.md) (OWASP Top 10:2025, ASVS L2 checklist for CSRF, sessions, auth, uploads, SQL building, headers) + fuzzing campaign (≥ 1h per fuzz target).
  → verify: findings tracked and closed; every **B** row has its test.
- [ ] **B.5.5** Supply chain: cosign keyless signing of release artifacts, SBOM published, actions pinned by SHA, OpenSSF Scorecard ≥ 7.
  → verify: `cosign verify-blob` in release workflow; Scorecard badge.
- [ ] **B.5.3** Performance baseline (router, ORM, render, full request) vs net/http, chi, echo.
  → verify: `docs/benchmarks.md` committed.
- [ ] **B.5.4** Docs completeness: every package has overview + examples; guides for each starter.
  → verify: checklist.

### BETA exit criteria

- [ ] Two RCs with no API changes between them.
- [ ] All conformance suites green on SQLite, Postgres, Redis, Mongo.
- [ ] Inertia React starter and HTMX starter both pass e2e on all OSes.
- [ ] No open `security` issues; `govulncheck` clean; OpenSSF Scorecard ≥ 7.

---

## Phase 4 — 1.0 Stable (`v1.0.0`)

**Goal:** a framework others can depend on for years.

### S.1 Stability & governance

- [ ] **S.1.1** `docs/stability.md`: compatibility promise (Go 1 style), deprecation policy (≥ 2 minors), supported Go versions (latest two), security support window.
  → verify: linked from README and SECURITY.md.
- [ ] **S.1.2** Release cadence: minor every ~3 months, patches as needed; `release/1.x` LTS branch policy.
  → verify: documented in CONTRIBUTING.
- [ ] **S.1.3** Upgrade guide template (`docs/upgrade/`) used for every minor.
  → verify: present for 0.x → 1.0.

### S.2 Documentation site

- [ ] **S.2.1** Docs site generated from `docs/` (static, hosted on GitHub Pages), versioned per minor.
  → verify: deployed; link checker green.
- [ ] **S.2.2** Tutorials: blog with HTMX, SaaS with Inertia, JSON API with tokens.
  → verify: each tutorial's code lives in `examples/` and is tested in CI.

### S.3 Release

- [ ] **S.3.1** `v1.0.0` tag for core and all driver modules (`drivers/*/v1.0.0`).
  → verify: `go get` each module at v1.0.0 works; pkg.go.dev updated.
- [ ] **S.3.2** Announcement (README, GitHub release, Go community channels).
  → verify: published.

### 1.0 Stable exit criteria

- [ ] All previous exit criteria still hold.
- [ ] Stability policy published; API frozen (apidiff gate in CI against `v1.0.0`).
- [ ] At least one real project (maintainer's) running on 1.0 in production.

---

## Post-1.0 backlog (not scheduled)

WebSockets/broadcasting, embedded admin panel, local dev dashboard (routes, queries, jobs, traces), JWT guard, Vue/Svelte Inertia starters, S3 storage driver, social login, TOTP 2FA, MySQL dialect, templ-first starter, typed RPC client generation.
