# Maintenance

The recurring work that keeps the repository healthy and the [OpenSSF Scorecard](https://scorecard.dev/viewer/?uri=github.com/AlexGalhardo/vukem) honest. Release mechanics live in [tooling.md](tooling.md#release-process); security defaults in [security.md](security.md).

## Pull request flow

Every change reaches `main` through a pull request, including the maintainer's:

1. Branch from `main` (`feat/…`, `fix/…`, `docs/…`, `release/vX.Y.Z`), commit with Conventional Commits, run `task ci`.
2. `gh pr create --fill`; the PR title is validated by `pr-title.yml` and becomes the squash commit.
3. Wait for the required checks (below), then squash-merge.

The `protect-main` ruleset enforces this. Its intended state is versioned in [`.config/ruleset-protect-main.json`](../.config/ruleset-protect-main.json): a pull request with 0 required approvals, the required status checks `Lint (Go)`, `API compatibility (apidiff)`, `Lint (OpenAPI golden)`, `Lint (Markdown)` and `Test (<os>, Go stable)` on Linux, Windows and macOS, linear history, no force push, no deletion and no bypass actors. Apply or re-apply it with:

```sh
gh api -X PUT repos/AlexGalhardo/vukem/rulesets/25297704 --input .config/ruleset-protect-main.json
```

A job renamed in `ci.yml` must be renamed in that file in the same PR, or every PR waits for a check that never reports.

### Releasing through a pull request

`task release -- vX.Y.Z` pushes the release commit and the nested-module bumps straight to `main`, which the ruleset rejects. Until the release tool opens the PR itself:

1. On an up-to-date local `main`, `go run ./internal/tools/release vX.Y.Z` (no `-push`; the tool refuses other branches): it writes `CHANGELOG.md` and the PLAN.md status, commits `chore(release): vX.Y.Z` and creates a local tag.
2. Move that commit to a branch: `git tag -d vX.Y.Z`, `git switch -c release/vX.Y.Z`, `git branch -f main origin/main`, `git push -u origin release/vX.Y.Z`. Open the PR, wait for CI, squash-merge.
3. `git switch main && git pull`, then `git tag -a vX.Y.Z -m vX.Y.Z` on the merged commit and `git push origin vX.Y.Z` (one tag per push, [Tag pushes](tooling.md#tag-pushes)).
4. `go run ./internal/tools/release -publish-only vX.Y.Z` publishes the GitHub release notes.
5. Raise the nested modules' core requirement in a second PR (`chore(deps): require vukem vX.Y.Z in nested modules`), then tag changed nested modules as described in [tooling.md](tooling.md#release-process).

### Code review

Vukem has one maintainer. GitHub does not let authors approve their own pull requests and Scorecard ignores bot reviews, so the `Code-Review` check stays at 0 until a second person reviews changes. Creating a second account to approve is metric fraud and breaks GitHub's terms; it is never done. What compensates, and is checked on every PR: the `TestSecurity_*` contract in [security.md](security.md), fuzzing, CodeQL, govulncheck, the coverage gate and the recorded reviews in [security-review.md](security-review.md) and [api/review.md](api/review.md). The way up is a co-maintainer or invited reviewer for release and security PRs.

## Release routine

After each [PLAN.md](PLAN.md) task group:

1. Release through a PR as above; required checks green before the merge.
2. Post-release, 15 minutes:
   - `gh run list --workflow release.yml` is green;
   - `cosign verify-blob` on the published `checksums.txt` ([SECURITY.md](../.github/SECURITY.md#verifying-release-artifacts));
   - read the score at <https://api.scorecard.dev/projects/github.com/AlexGalhardo/vukem> and record it in the pinned "Scorecard tracking" issue;
   - bump the reference apps once the Go proxy serves the version.

## Dependency routine

Weekly, on Monday:

1. Dependabot opens grouped PRs (`go-deps`, `actions`, `site-deps`, plus Docker for `docs/media`); `vuln.yml` scans every module at 06:17 UTC. Merge the PRs once CI is green. Actions stay pinned by full commit SHA with a `# vX.Y.Z` comment.
2. On every `golang.org/x/crypto` upgrade, re-check [`osv-scanner.toml`](../osv-scanner.toml) and drop `GO-2026-5932` once a fix exists.
3. Quarterly: count third-party modules per `go.mod` (`go mod graph`); every new direct dependency needs a decision under the [dependency policy](architecture.md#dependency-policy).

A module added to the repository must be added to the `gomod` directories in `.github/dependabot.yml`, to the `vuln.yml` matrix and to `MODULES` in `Taskfile.yml`.

## Security review routine

1. A change in `auth`, `session`, `csrf`, `crypt`, `signed`, `storage`, `upload`, `router`, `orm` or `middleware` updates its row in [security.md](security.md) and its `TestSecurity_*` test in the same PR.
2. A failure of `Fuzz`, `Vulnerabilities (weekly)` or `Scorecard` opens or updates an issue labelled `ci-failure` (`ci-failure.yml`). It blocks the next release until it is triaged: fixed, with the crasher committed under `testdata/fuzz/<Target>/`, or documented as a false positive.
3. Quarterly: re-run [security-review.md](security-review.md) against the OWASP Top 10 and ASVS and record the date on that page.
4. A security release gets a **Security** changelog entry (a `fix!:` commit with a `BREAKING CHANGE:` footer when it breaks an API), an `Insecure…` opt-out when reasonable, and an [apicheck exception](tooling.md#security-exceptions) when it breaks an API after `v1`.

## Metrics

Tracked in the pinned "Scorecard tracking" issue, updated at each release:

| Metric | Source | Target |
| --- | --- | --- |
| Scorecard score and per-check scores | `api.scorecard.dev` | ≥ 8.5 by `v1.0.0`; no check drops between releases |
| Open fixable vulnerabilities | `govulncheck`, Scorecard `Vulnerabilities` | 0 (only `GO-2026-5932` while it has no fix) |
| Scheduled workflows green | `gh run list --workflow fuzz.yml` (and `vuln.yml`, `scorecard.yml`) | 100% over the last 4 weeks |
| Coverage | `covergate` in `ci.yml` | ≥ 80% overall, ≥ 90% strict packages; no drop between releases |
| `//nolint:gocognit` in non-test code | `GOCOGNIT_BUDGET` in `ci.yml` | decreasing every release, 0 at `v1.0.0` |
| Dependabot PRs open for more than 7 days | `gh pr list --author app/dependabot` | 0 |
| Time from a `ci-failure` issue to its fix | `ci-failure` issues | < 7 days |
| Doc and code drift | `web/docs_test.go`, `z/tags_rules_test.go`, `internal/cli/reference_docs_test.go`, `snippetcheck` | 0 failures; each new API table in `docs/` gets a test |

## Maintainer-only settings

These live in GitHub settings or external services, not in the repository:

- The `protect-main` ruleset (above).
- Private vulnerability reporting, Dependabot alerts and security updates, secret scanning and push protection (Settings → Code security).
- The OpenSSF Best Practices badge (<https://www.bestpractices.dev>), whose answers point at this page, [CONTRIBUTING](../.github/CONTRIBUTING.md), [SECURITY.md](../.github/SECURITY.md), [testing.md](testing.md) and [tooling.md](tooling.md#release-process).
- Two-factor authentication on the maintainer account.
