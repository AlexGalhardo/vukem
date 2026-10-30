# Maintenance

The recurring work that keeps the repository healthy and the [OpenSSF Scorecard](https://scorecard.dev/viewer/?uri=github.com/AlexGalhardo/vukem) honest. Release mechanics live in [tooling.md](tooling.md#release-process); security defaults in [security.md](security.md).

## Pull request flow

Every change reaches `main` through a pull request, including the maintainer's:

1. Branch from `main` (`feat/…`, `fix/…`, `docs/…`, `release/vX.Y.Z`), commit with Conventional Commits, run `task ci`.
2. `gh pr create --fill`; the PR title is validated by `pr-title.yml` and becomes the squash commit.
3. Wait for the required checks (below), then merge: squash a single-change PR; rebase-merge a batch of Conventional Commits (`gh pr merge --rebase`), since git-cliff builds the changelog from each commit and a squash would leave only the PR title.

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

### Known CI issues

- Windows runtime crashes in the `go test -race` job ([golang/go#81238](https://github.com/golang/go/issues/81238), Go 1.27.3): on `windows-latest` (Windows Server 2025) hosts with Intel AMX, Windows writes an exception frame larger than the stack reserve Go keeps, below the goroutine stack, and the heap is corrupted. Seen as `fatal error: unknown caller pc` (a GC worker stack with a `sigtrampgo` frame), `fatal error: found pointer to free object` (`reportZombies`) or a nil dereference inside a stdlib function (`log/slog`), in CI runs 40972862220, 40813653835 and 40801161416. They never reproduce on Linux, macOS or AMD hosts (a 40-run local loop on a Ryzen stayed clean). The Test step prints the runner CPU first: re-run the failed job once when the CPU is an AMX Xeon (8573C, 6973P-C) and the failure has one of these signatures; any other failure, or the same crash on a non-AMX CPU, is a real bug. Remove this entry once a Go release ships the fix ([CL 828724](https://go.dev/cl/828724)).

## Release routine

After each [PLAN.md](PLAN.md) task group:

1. Refresh `site/content/last-updated.json` (`npm run last-updated` in `site/`, full history) and commit it before tagging, so every page shows its real "Last updated" date.
2. Release through a PR as above; required checks green before the merge.
3. Post-release, 15 minutes:
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

## Secret scanning

[Betterleaks](https://github.com/betterleaks/betterleaks) `v1.9.0` (latest stable release on 2026-10-29; the `v2.0.0-rc.*` tags are pre-releases and stay out until a stable v2) scans for leaked secrets on top of GitHub's server-side scanning:

- CI: `.github/workflows/secrets.yml` installs the Linux x64 release archive checked against its sha256 from the release's `checksums.txt`, scans the commits a pull request or push adds, and the whole history every Monday (failures reach `ci-failure.yml`).
- Locally: `task secrets:scan` (full history; `task secrets:scan -- --log-opts=main..HEAD` for a branch). Install with `go install github.com/betterleaks/betterleaks@v1.9.0` or the release archive.
- `.betterleaks.toml` extends the default rules with vukem's `APP_KEY` (`base64:` + 43 chars + `=`) and API token (`vk_<26>.<26>` base32) formats, and allowlists only literal placeholder values (`hunter2`, `correct horse...`, `_FAKE_`, `placeholder`, `example`), never paths.
- `.betterleaksignore` lists reviewed historical findings (fixtures, documentation samples, translated "password" labels) by fingerprint. A new finding is either a placeholder that should say so (`_FAKE_`, `example`) or a real secret: rotate it, do not ignore it.
- Validation (`--validation`, which sends findings to credential providers) stays off.

To bump: change `BETTERLEAKS_VERSION` and `BETTERLEAKS_SHA256` in `secrets.yml` (sha256 of `betterleaks_<v>_linux_x64.tar.gz` from `checksums.txt`) and the version in `Taskfile.yml`, then run `task secrets:scan`.

## Metrics

Checked at each release (the "Scorecard tracking" issue #5 was closed on 2026-10-29; the history and the open levers live here):

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

### Scorecard history

Add a row at each release from `https://api.scorecard.dev/projects/github.com/AlexGalhardo/vukem` (the score of the release commit's main push).

| Release | Date | Commit | Score | Notes |
| --- | --- | --- | --- | --- |
| v0.31.0 | 2026-10-25 | `d3c19ee` | 7.1 | Baseline from the specialist audit |
| v0.32.0 | 2026-10-25 | `deb7aab` | 7.5 | Vulnerabilities 5→9, Pinned-Dependencies 9→10, CI-Tests -1→10 |
| v0.33.0 | 2026-10-29 | `ca2565f` | 7.3 | Branch-Protection 3→4; Vulnerabilities 9→5: `golang.org/x/net` GO-2026-6860 and GO-2026-6866 (`drivers/otel`, `internal/bench`) and echo v4 GHSA-246p-cpwv-v3jq and GHSA-99jh-6h7p-pp36 (`internal/bench`), fixed after the release by x/net v0.62.0 and echo v5.4.2; GO-2026-5932 has no fix |

### Scorecard levers that need the maintainer

The automatable checks are at 10 (Binary-Artifacts, CI-Tests, Dangerous-Workflow, Dependency-Update-Tool, Fuzzing, License, Packaging, Pinned-Dependencies, SAST, Security-Policy, Signed-Releases, Token-Permissions). What is left needs a person or time:

- [ ] CII-Best-Practices 0 → 5: apply for the OpenSSF Best Practices *passing* badge at <https://www.bestpractices.dev> and add the badge to the README.
- [ ] Code-Review 0 and Branch-Protection 4 → 8+: require one approving review (and code-owner review, last-push approval) in the `protect-main` ruleset, which needs a second human maintainer; a sole maintainer cannot approve their own PRs.
- [ ] Contributors 0: rises when people from other organizations contribute.
- [ ] Maintained 0: rises on its own 90 days after the repository was created (after 2027-01-14) with weekly activity.
- [x] Branch protection ruleset applied (`.config/ruleset-protect-main.json`, 2026-10-25).

## Maintainer-only settings

These live in GitHub settings or external services, not in the repository:

- The `protect-main` ruleset (above).
- Private vulnerability reporting, Dependabot alerts and security updates, secret scanning and push protection (Settings → Code security).
- The OpenSSF Best Practices badge (<https://www.bestpractices.dev>), whose answers point at this page, [CONTRIBUTING](../.github/CONTRIBUTING.md), [SECURITY.md](../.github/SECURITY.md), [testing.md](testing.md) and [tooling.md](tooling.md#release-process).
- Two-factor authentication on the maintainer account.
