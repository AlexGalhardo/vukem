# AGENTS.md

Vukem: a Laravel/AdonisJS-inspired, stdlib-first Go web framework. Module `github.com/AlexGalhardo/vukem`, package `vukem`, CLI `vukem`.

## Start here

1. Read [PLAN.md](docs/PLAN.md): pick the first unchecked task of the current phase.
2. Read the docs that task touches (index in [docs/README.md](docs/README.md)).
3. Decisions are final unless [docs/brainstorming.md](docs/brainstorming.md) is updated first.

## Commands

- `task ci`: lint, vet and test (run before finishing any task)
- `task fmt` / `task lint` / `task test` (`-short`) / `task test:e2e` / `task test:integration`
- `task api:check`: apidiff gate on exported API (renames keep a deprecated `//go:fix inline` alias) — see [API compatibility](docs/tooling.md#api-compatibility)
- `task mod:check`: every nested module resolves from its own `go.mod` with `GOWORK=off` (part of `task ci`)
- `go test ./path/...` for a single package

## Rules

- Test first; a task is done only when its `→ verify` line passes and the [Definition of Done](docs/PLAN.md#definition-of-done) holds.
- `net/http`-native APIs; middleware is `func(http.Handler) http.Handler` ([docs/http.md](docs/http.md)).
- No globals, no `init()` registration, no reflection DI; `context.Context` first ([docs/architecture.md](docs/architecture.md)).
- Stdlib first; new deps only via the [dependency policy](docs/architecture.md#dependency-policy).
- Secure by default, fail closed; every default in [docs/security.md](docs/security.md) needs a `TestSecurity_*` test.
- Core never imports `drivers/*` or `contrib/*`; each is its own module.
- Tests: stdlib + go-cmp, table-driven, no network in unit tests ([docs/testing.md](docs/testing.md)).
- Formatting: gofumpt + gci + golines via `golangci-lint fmt`; tabs in Go ([docs/tooling.md](docs/tooling.md)).
- Commits: Conventional Commits, English, scope = package ([docs/tooling.md](docs/tooling.md#conventional-commits)).
- Changes land through a PR with green required checks; after each PLAN.md task group, release (`task release -- vX.Y.Z`, or the PR flow once the ruleset requires it) — see [maintenance](docs/maintenance.md) and [release process](docs/tooling.md#release-process).
- Never edit released `CHANGELOG.md` sections by hand; never move a pushed tag; push version tags one at a time ([tag pushes](docs/tooling.md#tag-pushes)).
- GitHub Actions is the CI gate (Linux/macOS/Windows, race, govulncheck, snippetcheck, Postgres): push, then check `gh run list` and fix failures; a `ci-failure` issue blocks the next release. Windows 11 stays first-class.
- Everything (code, comments, docs) in English.

## Where things live

| Path | What |
| --- | --- |
| `docs/` | design docs, one per area, and the roadmap `PLAN.md` |
| `.config/` | git-cliff, GoReleaser, Compose and the `protect-main` ruleset ([layout](docs/tooling.md#repository-layout)) |
| `.github/` | workflows, templates, CONTRIBUTING, SECURITY, CODE_OF_CONDUCT |
| `internal/tools/` | repo tooling (apicheck, commitlint, covergate, fixinline, modcheck, release, snippetcheck) |
| `drivers/*` | optional driver modules (postgres, redis, mongo) |
| `contrib/*` | optional feature modules (graphql, grpc, stripe, imaging, engine) |
| `.claude/rules/` | path-scoped rules for agents (Go, tests, security, workflows, release…) |
| `.claude/skills/` | Go skills, workflow skills and `vukem-release` (release + app bumps) |

## When stuck

Ask instead of guessing on API design; record the answer in `docs/brainstorming.md`.
