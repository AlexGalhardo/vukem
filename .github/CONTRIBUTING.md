# Contributing to Vukem

Thanks for helping. This guide covers setup, workflow and the rules every change follows.

## Setup

1. Install the tools listed in [docs/tooling.md](../docs/tooling.md#required-tools) (Go ≥ 1.27, golangci-lint v2, Task, lefthook, Node.js for markdownlint). On Windows everything installs with `winget`.
2. Clone and install the git hooks:

   ```sh
   git clone https://github.com/AlexGalhardo/vukem.git
   cd vukem
   task setup
   ```

3. Check everything works: `task ci`.

## Workflow

1. Pick a task from [PLAN.md](../docs/PLAN.md) (or open an issue first for anything not in the plan).
2. Branch from `main`: `feat/<short-name>`, `fix/<short-name>`, `docs/<short-name>`.
3. Write the test first, then the code. Keep changes small and focused.
4. Run `task ci` before pushing (the `pre-push` hook runs tests, vet and govulncheck).
5. Open a pull request against `main`. The **PR title** must be a Conventional Commit; it becomes the squash-merge commit message (a batch of Conventional Commits is rebase-merged instead, so each one reaches the changelog). The `main` ruleset requires a pull request, the CI status checks and a linear history, so the maintainer's own changes go through the same flow.
6. Fill in the checklist of the [PR template](pull_request_template.md). Vukem has a single maintainer: a PR from an outside contributor is reviewed by the maintainer, and the maintainer's own PRs are merged once CI is green (GitHub does not let authors approve their own PRs; see [docs/maintenance.md](../docs/maintenance.md#code-review)).

## Commit messages

[Conventional Commits 1.0](https://www.conventionalcommits.org/en/v1.0.0/), enforced by the `commit-msg` hook and CI:

```text
feat(router): add named route groups
fix(session)!: regenerate id on login

BREAKING CHANGE: Session.Login now returns an error.
```

Types: `feat`, `fix`, `perf`, `refactor`, `docs`, `test`, `build`, `ci`, `chore`, `style`, `revert`. Scope is the package or area (`router`, `orm`, `cli`, `postgres`…). Full rules: [docs/tooling.md](../docs/tooling.md#conventional-commits).

## Code rules

- Format and lint: `task fmt`, `task lint`. Do not add `//nolint` without a specific linter and a reason.
- Standard library first. A new dependency needs a design note (see [docs/architecture.md](../docs/architecture.md#dependency-policy)).
- No package-level mutable state, no `init()` registration, `context.Context` as the first parameter for I/O.
- Core packages never import `drivers/*`.
- Tests use the standard library and `go-cmp`; no testify in core. See [docs/testing.md](../docs/testing.md).
- Every exported identifier has a doc comment; new public APIs include an `Example`.

## Changelog and releases

Do not edit `CHANGELOG.md` by hand: each release section is generated from commit messages by git-cliff when a PLAN.md task group is released (`task release -- vX.Y.Z`) ([Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/)). Versioning follows [SemVer](https://semver.org); see [docs/tooling.md](../docs/tooling.md#versioning).

## Release cadence

- **Minor releases** (`v1.N.0`) ship about every **3 months** with new features, deprecations and an upgrade guide in [docs/upgrade/](../docs/upgrade/README.md) copied from its [template](../docs/upgrade/TEMPLATE.md).
- **Patch releases** (`v1.N.M`) ship whenever a fix is ready: bug and security fixes only, never API changes.
- **Release candidates**: each minor gets at least one `v1.N.0-rc.N` tag a week before the final release; an RC is promoted only if nothing in the public API changed since it.
- **`release/1.x` LTS branch**: created from the last `v1` minor when `v2` work starts. It receives security fixes for 12 months, cherry-picked from `main` by the maintainer and tagged as patches of that minor.
- Driver and contrib modules are released with the same version numbers as the core when they change in that release.

What each kind of release may change is defined in the [stability policy](../docs/stability.md).

## Code of conduct

This project follows the [Contributor Covenant](CODE_OF_CONDUCT.md).
