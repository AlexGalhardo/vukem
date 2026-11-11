---
name: vukem-release
description: End-to-end release cycle for the Vukem framework and its reference apps. Use when cutting a vukem release, tagging drivers/contrib modules, merging a batch of fix branches before a release, or bumping the reference apps (vukem-email, hexascore, vukem-trading, vukem-video, vukem-music, vukem-tools) to a new vukem version.
---

# Vukem release cycle

The order matters: each step depends on the previous one being green. Never skip a hook, never move a pushed tag, never hand-edit a released CHANGELOG section.

## 1. Land the batch

For each fix branch (one worktree per branch as a sibling checkout `../vukem-wt-<name>`, created only with the owner's explicit approval):

1. The branch agent reports: commits, validation commands and results, public API changes, what apps must change.
2. An independent reviewer (code-reviewer agent, read-only, clean context) reviews `git diff main...<branch>`. Security-sensitive changes always get one.
3. Findings go back to the branch agent; it fixes test-first and rebases on the current `main`.
4. Open a PR per branch (or one PR for the batch), run the affected tests (and `go test ./internal/cli/ -run 'EndToEnd$'` for scaffold changes), wait for the required checks and merge: squash a single change, rebase-merge a batch of Conventional Commits. `main` is protected, so there is no direct push. GitHub refuses a rebase merge above 100 commits: split the batch into several PRs.
5. Remove the worktree and the merged branch.

## 2. CI gate

- `gh run list --branch main --workflow CI --limit 1` until the run for the merge commit finishes; every job must pass.
- A red integration job is often test hygiene, not product code: for example a `t.Cleanup` using `t.Context()` (already cancelled) leaves tables behind on the shared Postgres. Fix the root cause, push, wait again.

## 3. Release the core

Before releasing: no open `ci-failure` issue (`gh issue list --label ci-failure`), last `Fuzz`, `Vulnerabilities (weekly)`, `Scorecard`, `Secrets` and `CodeQL` runs green.

```sh
go run ./internal/tools/release -push vX.Y.Z
```

When the `protect-main` ruleset requires pull requests (check `gh api repos/AlexGalhardo/vukem/rulesets/25297704 --jq '.rules[].type'` for `pull_request`), `-push` is rejected: follow [Releasing through a pull request](../../../docs/maintenance.md#releasing-through-a-pull-request) instead, and land the nested-module bumps of step 4 through a PR too.

The tool generates the section from `.config/cliff.toml` (every entry links its commit), commits `chore(release): vX.Y.Z`, tags, pushes `main` and the tag in **separate pushes**, and publishes the GitHub release. It refuses to publish when the section it extracted is empty, so a release never goes out with an empty body.

## 4. Tag nested modules

On an up-to-date `main`:

```sh
go run ./internal/tools/release -modules -dry-run   # the plan: LAST, NEXT and REASON per module
go run ./internal/tools/release -modules -push      # test with GOWORK=off, tag, push one tag at a time
```

- Check the table first. Test-only changes, a core requirement bump and a repo-wide `go` directive bump need no tag. Force a number the rules cannot see (a `feat` that should be a minor, a `!` buried in a squash body) with `-only dir=vX.Y.Z`.
- The tool tests every module to tag with `GOWORK=off`, creates no tag when one fails, and pushes **one tag per `git push`**. Never `git push origin --tags`: GitHub creates no push event when more than three tags go up in a single push.
- Module tags (`<dir>/vX.Y.Z`) do not trigger `release.yml`, so there is no run to wait for after a push.
- When `contrib/stripe` is tagged the tool rewrites `stripeVersion` in `internal/cli/billing.go`; commit it through a PR (`TestStripeVersionIsLatestTag` fails until then).
- When `contrib/studio` is tagged, bump `studioVersion` in `internal/cli/studio.go` by hand to the new tag and commit it through a PR (`TestStudioVersionIsLatestTag` fails until then; until the first tag it skips, and `vukem studio` without `--bin` cannot install).

## 5. Recover a tag that published nothing

A tag that is up without a release is the normal outcome of a batched tag push. Do not move the tag and do not hand-write the publish:

- Core: `go run ./internal/tools/release -publish-only vX.Y.Z` (fails if `CHANGELOG.md` has no section for that version).
- Apps: run their `release.yml` with `workflow_dispatch` and the version as the input — `gh workflow run release.yml -f version=vX.Y.Z --repo <owner>/<app>` — then `gh run list --workflow release.yml --limit 1`.

Both paths are idempotent: a release that was published with an empty body gets its notes replaced.

## 6. The app release workflow

Every app `release.yml` is a copy of [assets/app-release.yml](assets/app-release.yml). Two things in it are not interchangeable:

- The changelog body is extracted with a **literal** match, `index($0, "## [" v "]") == 1`. The regex form `'$0 ~ "^## \[" v "\]"'` is broken: awk reads `"\["` as a plain `[` (`warning: escape sequence '\[' treated as plain '['`), the pattern becomes the character class `^## [0.8.0]`, and it never matches a heading.
- An empty section **fails the job**. Never restore an `echo "See CHANGELOG.md"` fallback: a release published with an empty body is indistinguishable from a successful one, which is how eight app releases shipped with no notes.

## 7. Warm the Go proxy

Poll until every new version answers 200 (the proxy and sum.golang.org cache 404s for a few minutes after a push):

```sh
curl -s -o /dev/null -w '%{http_code}' https://proxy.golang.org/github.com/!alex!galhardo/vukem/@v/vX.Y.Z.info
curl -s -o /dev/null -w '%{http_code}' https://proxy.golang.org/github.com/!alex!galhardo/vukem/<dir>/@v/vA.B.C.info
```

## 8. Bump the reference apps

Ask each app agent to bump before any feature work, with the upgrade notes from `docs/upgrade/` and the list of adoptable features. Each reports CI green and a healthy production probe (`/up?checks=1` or `/ready`).

- Platform settings that only the new version understands (example: `MAIL_MAILER=mailtrap` in v0.26.0) are set **after** the bumped app is live; an unknown value fails the boot.
- Agents never change Railway variables or other production config without the owner's approval relayed by the main session. An approval for one app does not extend to the others.
- Verify the change for real (for mail: trigger a password reset and read the Mailtrap sandbox through its API).

## 9. Record it

In the single `vukem-feedback.md` at the workspace root ([Downstream feedback queue](../../../docs/maintenance.md#downstream-feedback-queue)), mark each entry the release fixed ("fixed in vX.Y.Z"), then triage the new open entries into the next batch.
