# Tooling

Formatting, linting, hooks, commit conventions, changelog, releases and CI. Decisions `Q3`–`Q11` and `P3`/`P4` in [brainstorming.md](brainstorming.md).

## Required tools

| Tool | Version | Install (Windows) | Install (macOS/Linux) |
| --- | --- | --- | --- |
| Go | ≥ 1.27 | `winget install GoLang.Go` | go.dev/dl |
| golangci-lint | v2.x | `winget install GolangCI.golangci-lint` | `brew install golangci-lint` |
| Task | v3 | `winget install Task.Task` or `go install github.com/go-task/task/v3/cmd/task@latest` | `brew install go-task` |
| lefthook | v2 | `winget install evilmartians.lefthook` | `brew install lefthook` |
| git-cliff | latest | `winget install orhun.git-cliff` | `brew install git-cliff` |
| Node.js | ≥ 22 | `winget install OpenJS.NodeJS` | — (markdownlint only) |

`task setup` installs remaining Go-based tools and the git hooks.

## Editor settings

`.editorconfig`: tabs (width 4) for `*.go`, `go.mod`, `go.work`, Makefiles, `*.tmpl`/`*.gohtml`; **2 spaces** for YAML, JSON, Markdown (YAML forbids tabs; Markdown lists render inconsistently with tabs). LF everywhere except `*.ps1`/`*.bat`.

`.gitattributes` forces `eol=lf`; without it Windows checkouts (`core.autocrlf=true`) make gofumpt report every file.

gopls: enable `"formatting.gofumpt": true` so editor and CI agree.

## Formatting and linting

- `golangci-lint fmt` — formatters: **gofumpt**, **gci** (std / third-party / `github.com/AlexGalhardo/vukem`), **golines** (120 cols, tab-len 4).
- `golangci-lint run ./...` — `default: standard` plus correctness (errorlint, bodyclose, sqlclosecheck, rowserrcheck, contextcheck, noctx, nilerr, exhaustive, forcetypeassert, musttag), security (gosec), style (revive, gocritic, misspell, unconvert, unparam, usestdlibvars, intrange, perfsprint, nolintlint, godot, sloglint, modernize, copyloopvar), tests (usetesting, thelper, tparallel), and **depguard** (core must not import drivers or non-allowlisted modules).
- `nolint` requires a specific linter and an explanation.
- revive's `exported` and `package-comments` rules are enforced (no `comments` exclusion preset): every exported identifier needs a doc comment starting with its name.
- `go vet ./...` and `go fix -diff ./...` (modernizers) run in CI.
- `go run ./internal/tools/fixinline` (CI lint job, `task vet`) checks every `//go:fix inline` wrapper: through a build overlay (the tree is not written) it adds callers to the wrapper's package whose argument variables reuse the parameter names (in order, rotated by one, and with one argument replaced by an expression while the same-named variable is in scope), runs `go fix -diff` and fails unless each call becomes exactly the wrapper's body with the arguments substituted, or when the in-order call is not inlined at all. A rotated or literal call that `go fix` leaves alone is printed as a `note:` (it still compiles). Supported wrappers forward to one call in a single statement, without type parameters or build constraints; anything else fails the check until the tool learns it.
- `govulncheck ./...` in CI, in the pre-push hook and in the weekly `vuln.yml` cron (every module).
- Multi-module: `task lint` loops over every module; CI runs one job per module with `GOWORK=off`.

## Markdown

`markdownlint-cli2` with `.markdownlint-cli2.jsonc`: ATX headings, dash lists, fenced code, `MD010` ignores `go`/`makefile` fences (tabs), `MD024` siblings-only (Keep a Changelog repeats `### Added`), `MD013` off, `MD033` allows a few HTML tags, `.claude/**` ignored.

## Git hooks (lefthook)

| Hook | Jobs |
| --- | --- |
| `pre-commit` | `golangci-lint fmt` on staged `.go` (re-staged), `golangci-lint run --new-from-rev=HEAD`, `go mod tidy -diff`, `markdownlint-cli2` on staged `.md` |
| `commit-msg` | `go run ./internal/tools/commitlint {1}` |
| `pre-push` | `go vet ./...`, `govulncheck ./...` (tests, race and the OS matrix run in GitHub Actions) |

Install with `task hooks` (`lefthook install`). Personal overrides go in `lefthook-local.yml` (git-ignored).

## Conventional Commits

Spec: <https://www.conventionalcommits.org/en/v1.0.0/>. Enforced by the in-repo validator and by the PR-title check in CI.

```text
<type>(<scope>)!: <description>

[body]

[footer(s)]
```

- **Types:** `feat`, `fix`, `perf`, `refactor`, `docs`, `test`, `build`, `ci`, `chore`, `style`, `revert`.
- **Scopes:** package or area — `router`, `ctx`, `config`, `validate`, `session`, `view`, `htmx`, `orm`, `schema`, `database`, `auth`, `authz`, `mail`, `cache`, `storage`, `event`, `queue`, `schedule`, `inertia`, `vite`, `cli`, `postgres`, `redis`, `mongo`, `otel`, `websocket`, `graphql`, `deps`, `release`, `docs`, `repo`.
- Description: imperative, lowercase start, no trailing period, ≤ 100 chars header.
- Breaking changes: `!` after type/scope; describe the impact in a `BREAKING CHANGE:` footer.
- Allowed exceptions: `Merge …`, `Revert "…"`, `fixup!`/`squash!` (local only; blocked on push to `main`).

## Versioning

- [SemVer 2.0.0](https://semver.org/spec/v2.0.0.html). Tags `vMAJOR.MINOR.PATCH` (leading `v`, required by Go).
- **v0.x** during Prototype/ALPHA: minor bumps may break; patch never breaks.
- BETA: `v0.N.0` per group, then `v1.0.0` directly once the 1.0 exit criteria hold (no release candidates, see brainstorming Q14).
- Driver modules are tagged with their path prefix: `drivers/postgres/v0.3.0`.
- **Never move or delete a pushed tag** (the Go proxy caches forever). Bad release → new patch with `retract` in `go.mod`.
- Release order for multi-module changes: core → update drivers' `go.mod` → tag drivers.
- Published `go.mod` files never contain `replace` (breaks `go install`).

## Changelog

[Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/). `CHANGELOG.md` is generated by **git-cliff** from Conventional Commits:

| Commit type | Section |
| --- | --- |
| `feat` | Added |
| `fix` | Fixed |
| `perf`, `refactor` | Changed |
| `revert` | Removed |
| footer `Deprecated:` | Deprecated |
| body/footer mentioning `security` | Security |
| `docs`, `test`, `ci`, `build`, `chore`, `style` | omitted |

The template lives in `.config/cliff.toml` (git-cliff is called with `--config .config/cliff.toml`). Every entry ends with its short commit hash linked to the commit on GitHub, so each Added/Fixed/Security line points at the code that changed; the docs site renders the same file at [vukem.dev/changelog](https://vukem.dev/changelog). `task changelog` previews the next section; released sections are never edited by hand (change the template or the commit history instead). The `[Unreleased]` section exists at all times.

## Release process

**One release per PLAN.md task group** (P.1, P.2, …, A.0, A.1, …, B.0, …), cut as soon as the group's tasks are checked — never batch several groups into one release.

| Phase | Version per group | Phase completion |
| --- | --- | --- |
| Prototype | `v0.0.N` (patch bump per group) | `v0.1.0` when P.12 closes |
| ALPHA | `v0.N.0` (minor bump per group) | last ALPHA minor |
| BETA | `v0.N.0` per group (B.0 = `v0.15.0`, B.1 = `v0.16.0`, …) | `v1.0.0` when BETA and the 1.0 exit criteria close (Q14) |

1. Push the group's commits to `main`.
2. `go run ./internal/tools/release -push vX.Y.Z` (or `task release -- vX.Y.Z`, which runs `task ci:full` first) once GitHub Actions is green for the pushed commits:
   - requires a clean tree on `main`;
   - generates the version's section with git-cliff and inserts it below `[Unreleased]` in `CHANGELOG.md` (Keep a Changelog 1.1.0; released sections are never edited);
   - commits `chore(release): vX.Y.Z`, creates an annotated tag, pushes `main` and the tag;
   - publishes a GitHub release whose notes are that changelog section (`v0.x` and pre-releases are marked pre-release).
3. Missed a group? Cut it retroactively: `go run ./internal/tools/release -no-commit -ref <commit> vX.Y.Z`, tag `<commit>`, commit the changelog, push, and create the GitHub release from the section.
4. Nested modules (`drivers/*`, `contrib/*`) are tagged separately as `<dir>/vX.Y.Z` (e.g. `drivers/redis/v0.4.0`) after the core release:
   - tag only modules with non-test changes since their last tag (`git log <dir>/<last tag>..HEAD -- <dir>`); a breaking change is a minor bump before v1;
   - first run each changed module against the published core: `cd <dir> && GOWORK=off go test ./...` (the workspace hides a missing `go.mod` bump);
   - test-only changes (integration tests) need no tag.
5. From `v0.1.0` on, release tags also get `vukem` CLI binaries (linux, macOS, Windows; amd64 and arm64) built by GoReleaser (`.config/goreleaser.yaml`), attached to the existing release without touching its notes. `release.yml` does it on tag push and also uploads SBOMs and the cosign signature ([Supply chain](#supply-chain)). The local fallback, `task release:binaries` on the tagged commit (Docker; `SNAPSHOT=1` builds into `dist/` without publishing), skips signing: keyless signing needs the Actions OIDC token.
6. Warm the proxy: `GOPROXY=https://proxy.golang.org go list -m github.com/AlexGalhardo/vukem@vX.Y.Z`. Right after a tag push the proxy and `sum.golang.org` can answer 404 for a few minutes (negative cache): poll `https://proxy.golang.org/github.com/!alex!galhardo/vukem/@v/vX.Y.Z.info` until it returns 200 before bumping apps.
7. Bump the reference apps only after the proxy serves the new versions. A setting that only the new version understands (for example `MAIL_MAILER=mailtrap`, added in v0.26.0) is set on the platform **after** the bumped app is deployed: an unknown value fails the boot (fail closed).

## CI (GitHub Actions)

| Workflow | Trigger | Jobs |
| --- | --- | --- |
| `ci.yml` | push to `main`, PRs | lint (per module), unit matrix, integration (services), e2e, markdownlint, govulncheck, coverage gate, `apidiff` ([API compatibility](#api-compatibility)) |
| `vuln.yml` | weekly, manual | govulncheck on the core and every module (`drivers/*`, `contrib/*`, `examples/*`) with `GOWORK=off` |
| `pr-title.yml` | PR opened/edited | semantic PR title |
| `release.yml` | tag `v*` | GoReleaser binaries, SBOMs, cosign keyless signature, build provenance |
| `codeql.yml` | push, weekly | CodeQL Go |
| `scorecard.yml` | push to `main`, weekly | OpenSSF Scorecard |
| `site.yml` | push to `main` and PRs touching `site/**` or `CHANGELOG.md` | docs site `npm ci`, lint and `next build` (Vercel only builds after the push, so MDX errors are caught here first) |
| Dependabot | weekly | `gomod` (all modules, prefix `build(deps)`), `npm` (`site/`), `github-actions` (prefix `ci`) |

Workflows declare `permissions: contents: read` at the top; jobs widen it only for what they write. Every action is pinned by full commit SHA with a `# vX.Y.Z` comment (Dependabot bumps both), and tools fetched in workflows use exact versions (`govulncheck@v1.9.0`, `@redocly/cli@1.34.3`), never `@latest`.

### Supply chain

- `release.yml` runs GoReleaser with syft (one SPDX SBOM per archive, `<archive>.sbom.json`) and cosign keyless: `checksums.txt.sigstore.json` is a Sigstore bundle signed with the workflow's GitHub OIDC identity, so no signing key or secret exists. `actions/attest-build-provenance` then attests every file listed in `checksums.txt`.
- `checksums.txt` lists the archives and their SBOMs, so its signature covers all of them.
- `scorecard.yml` runs OpenSSF Scorecard (target ≥ 7), publishes the results and uploads SARIF to code scanning.
- Repository settings that Scorecard also checks are applied by an admin, never by workflows: a branch ruleset on `main` (pull request with the CI checks required, no force pushes or deletion), Dependabot alerts and security updates, private vulnerability reporting.

### Verifying a release

```sh
VERSION=v0.30.0 # the release tag
ARCHIVE="vukem_${VERSION#v}_linux_amd64.tar.gz"
gh release download "$VERSION" -R AlexGalhardo/vukem \
  -p checksums.txt -p checksums.txt.sigstore.json -p "$ARCHIVE" -p "$ARCHIVE.sbom.json"
cosign verify-blob checksums.txt \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp '^https://github\.com/AlexGalhardo/vukem/\.github/workflows/release\.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
sha256sum --ignore-missing -c checksums.txt
gh attestation verify "$ARCHIVE" -R AlexGalhardo/vukem
```

## API compatibility

The 1.x [stability policy](stability.md) is enforced by the `apidiff` job in `ci.yml` on pull requests and pushes to `main`:

- `go run ./internal/tools/apicheck` (stdlib only, Windows-safe) walks the core module and every `drivers/*` and `contrib/*` module, picks the latest stable tag of the major version (core `v1.*`, nested modules `drivers/x/v1.*`; pre-releases are ignored), extracts it with `git archive` into a temporary directory and runs [`apidiff`](https://pkg.go.dev/golang.org/x/exp/cmd/apidiff) `-m -incompatible` on the tag and the working tree, both type-checked with `GOWORK=off`.
- Any incompatible change fails the job with one `::error` annotation per identifier. A breaking change needs a new major version (`/v2` module path); renames keep the old name as a deprecated alias or wrapper marked `//go:fix inline`.
- A module without a stable `v1` tag is skipped with a `::notice`, so the job passes until `v1.0.0` and the driver `v1` tags exist; a module added later is gated from its first `v1` tag.
- The job needs the tags: it checks out with `fetch-depth: 0`. `apidiff` comes from `golang.org/x/exp`, which has no releases, so it is pinned to an exact pseudo-version (`APIDIFF_VERSION` in `Taskfile.yml`, same value in `ci.yml`) and installed with `go install`, never added to a `go.mod`.

Locally, `task api:check` installs the pinned `apidiff` and runs the gate. `-major v0` compares with the latest `v0` tags instead, and `-report file.md` writes every compatible and incompatible change grouped by package without failing: the pre-freeze review in [docs/api/](api/review.md) was produced with `task api:check -- -major v0 -head v1.0.0 -report docs/api/apidiff-v0.20.1-to-v1.0.0.md`.

### The v1 baseline

There is no baseline file: the latest stable `v1` tag of each module is the baseline, so every new tag moves it forward. When `v1.0.0` is cut ([v1.0 API review](v1-api-review.md#the-v1-gate)):

1. On the release commit, `task api:check -- -major v0 -head v1.0.0 -report docs/api/apidiff-v0.25.0-to-v1.0.0.md` (base: the last `v0` tag) lists what changed since the last `v0`; every incompatible entry must be in the [1.0 upgrade guide](upgrade/1.0.md). Commit the report.
2. Tag the core `v1.0.0` and every published nested module `drivers/<name>/v1.0.0` / `contrib/<name>/v1.0.0` (after their `go.mod` requires core `v1.0.0`). A module left on `v0` stays outside the gate until its first `v1` tag.
3. From then on the `apidiff` job fails any incompatible change. A security fix that must break an API (allowed by the [stability policy](stability.md#what-is-not-covered)) needs an entry in the [exception list](#security-exceptions), never an admin merge over the failed check. A `/v2` module needs the CI step switched to `-major v2` for that module.

### Security exceptions

`.config/apicheck-exceptions.json` is the only sanctioned way to ship an incompatible change after `v1` (decision B6). Each entry names one change exactly as the gate prints it:

```json
{
  "exceptions": [
    {
      "module": "github.com/AlexGalhardo/vukem",
      "symbol": "github.com/AlexGalhardo/vukem/auth/hash.DefaultArgon2",
      "change": "removed",
      "justification": "the default parameters are below the OWASP minimum; callers must pick explicit ones",
      "advisory": "https://github.com/AlexGalhardo/vukem/security/advisories/GHSA-xxxx-xxxx-xxxx",
      "version": "v1.4.1"
    }
  ]
}
```

- `module` is a published module path, `symbol` the import path and identifier from the `::error` annotation, `change` the apidiff message (matched exactly, so a different break to the same symbol is not covered), `advisory` an `https` link to the GitHub security advisory or issue, and `version` the release of that module that ships the break (`vX.Y.Z`).
- The gate turns a covered change into a `::warning` and still fails on every other incompatible change. One entry covers one change.
- The gate fails when the file is malformed (unknown fields, trailing data, bad JSON), when an entry misses a field, names an unknown module or a symbol outside it, has a non-`https` advisory or a non-stable version, is duplicated, matches no change (unused, including for a module without a `v1` tag) or has expired: once the module's base tag is at or past `version`, the break is in the baseline and the entry must be removed right after that release.
- The file is validated in report mode too; a missing file means no exceptions. Ship the break as `fix(scope)!:` with a `BREAKING CHANGE:` footer naming the advisory, so it lands under **Security** in the changelog.

## Task runner

`Taskfile.yml` is the single entry point (same commands locally and in CI):

| Task | Does |
| --- | --- |
| `task setup` | install Go tools, `lefthook install` |
| `task fmt` | `golangci-lint fmt` |
| `task lint` | golangci-lint (all modules) + markdownlint |
| `task test` | unit tests (all modules) |
| `task test:race` | with `-race` |
| `task test:integration` | integration tests |
| `task cover` | coverage report + gate |
| `task vuln` | govulncheck |
| `task api:check` | apidiff gate against the latest `v1` tags ([API compatibility](#api-compatibility)) |
| `task changelog` | git-cliff |
| `task ci` | fast local gate: lint, vet, unit tests |
| `task ci:full` | full pipeline locally (Windows tests, Linux `-race` tests in Docker, integration via Docker services, vuln, coverage) — optional locally; GitHub Actions runs the same jobs on every push and PR and is the gate before a release |
| `task services:up` / `services:down` | Postgres, Redis, Mongo via `.config/compose.yaml` |

## Repository layout

The root keeps only what tools or Go require there; everything else has a fixed home:

| Path | What lives there |
| --- | --- |
| root | the `vukem` package (grouped by theme: `app.go`, `http.go`, `database.go`, `background.go`, `services.go`, `command.go`, `codegen.go`, `boost.go`, `doc.go` and their tests), one directory per public package, `go.mod`/`go.sum`/`go.work`, `README.md`, `LICENSE`, `CHANGELOG.md`, `AGENTS.md`, `Taskfile.yml`, `lefthook.yml` and the configs tools only discover at the root (`.editorconfig`, `.gitattributes`, `.gitignore`, `.golangci.yml`, `.markdownlint-cli2.jsonc`, `osv-scanner.toml`, which OpenSSF Scorecard reads) |
| `.config/` | `cliff.toml` (passed with `--config`), `goreleaser.yaml` (GoReleaser finds it on its own), `compose.yaml` (passed with `-f`; `name: vukem` keeps the container names), `apicheck-exceptions.json` (read by `internal/tools/apicheck`, see [Security exceptions](#security-exceptions)) |
| `.github/` | workflows, Dependabot, issue/PR templates and the community files `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `SECURITY.md` (GitHub detects them there) |
| `.claude/` | `CLAUDE.md`, rules, skills and settings |
| `docs/` | design docs and the roadmap `PLAN.md` |

Add a new file to the root only when a tool cannot be pointed elsewhere; new root package code goes into the matching theme file instead of a new one.

### Windows and Git Bash

- Git Bash rewrites arguments that start with `/` into Windows paths: `railway ssh -- /app/rifa …` becomes `C:/Program Files/Git/app/rifa`. Prefix the command with `MSYS_NO_PATHCONV=1`, or double the slash for Windows flags (`taskkill //PID 123 //F`).
- Never kill processes by image name (`taskkill /IM app.exe`): other projects' servers share the name. Stop your own background task, or a PID you started.
- The first `railway ssh` needs the host key in `~/.ssh/known_hosts`; non-interactive shells cannot answer the prompt (`Host key verification failed`).
- A stale `site/.next` from `next dev` breaks `next build` type checks; delete it before a local production build.

## Agent configuration

- `AGENTS.md` — canonical agent instructions (≤ 50 lines, links to docs); the roadmap is [`docs/PLAN.md`](PLAN.md).
- `.claude/CLAUDE.md` — imports `@../AGENTS.md`, Claude-specific notes.
- `.claude/rules/` — path-scoped rules (`go.md`, `testing.md`, `commits.md`, `changelog.md`, `markdown.md`, `drivers.md`, `secure-coding.md`, `site.md`, `release.md`).
- `.claude/skills/` — samber/cc-skills-golang + workflow skills, and `vukem-release` (the end-to-end release and app-bump cycle).
- `.claude/settings.json` — permission allowlist (`go`, `golangci-lint`, `task`, `git` read commands) and a PostToolUse hook that runs `golangci-lint fmt` on edited `.go` files.
