# AGENTS.md

Vukem: a Laravel/AdonisJS-inspired, stdlib-first Go web framework. Module `github.com/AlexGalhardo/vukem`, package `vukem`, CLI `vukem`.

## Start here

1. Read [PLAN.md](PLAN.md): pick the first unchecked task of the current phase.
2. Read the docs that task touches (index in [README.md](README.md#documentation)).
3. Decisions are final unless [docs/brainstorming.md](docs/brainstorming.md) is updated first.

## Commands

- `task ci`: lint, vet and test (run before finishing any task)
- `task fmt` / `task lint` / `task test` / `task test:integration`
- `go test ./path/...` for a single package

## Rules

- Test first; a task is done only when its `→ verify` line passes and the [Definition of Done](PLAN.md#definition-of-done) holds.
- `net/http`-native APIs; middleware is `func(http.Handler) http.Handler` ([docs/http.md](docs/http.md)).
- No globals, no `init()` registration, no reflection DI; `context.Context` first ([docs/architecture.md](docs/architecture.md)).
- Stdlib first; new deps only via the [dependency policy](docs/architecture.md#dependency-policy).
- Secure by default, fail closed; every default in [docs/security.md](docs/security.md) needs a `TestSecurity_*` test.
- Core never imports `drivers/*`; each driver is its own module.
- Tests: stdlib + go-cmp, table-driven, no network in unit tests ([docs/testing.md](docs/testing.md)).
- Formatting: gofumpt + gci + golines via `golangci-lint fmt`; tabs in Go ([docs/tooling.md](docs/tooling.md)).
- Commits: Conventional Commits, English, scope = package ([docs/tooling.md](docs/tooling.md#conventional-commits)).
- After each PLAN.md task group: push and `task release -- vX.Y.Z` (changelog + GitHub release) — see [release process](docs/tooling.md#release-process).
- Never edit released `CHANGELOG.md` sections by hand; never move a pushed tag.
- Windows 11 is first-class: run `task test` here (Windows) and `task test:linux` (Docker) before releases.
- Everything (code, comments, docs) in English.

## Where things live

| Path | What |
| --- | --- |
| `docs/` | design docs, one per area |
| `internal/tools/` | repo tooling (commitlint) |
| `drivers/*` | optional driver modules (postgres, redis, mongo) |
| `.claude/rules/` | path-scoped rules for agents |
| `.claude/skills/` | Go skills (samber/cc-skills-golang) and workflow skills |

## When stuck

Ask instead of guessing on API design; record the answer in `docs/brainstorming.md`.
