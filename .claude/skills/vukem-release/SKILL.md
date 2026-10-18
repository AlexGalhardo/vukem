---
name: vukem-release
description: End-to-end release cycle for the Vukem framework and its reference apps. Use when cutting a vukem release, tagging drivers/contrib modules, merging a batch of fix branches before a release, or bumping the quiz, rifa and marketplace apps to a new vukem version.
---

# Vukem release cycle

The order matters: each step depends on the previous one being green. Never skip a hook, never move a pushed tag, never hand-edit a released CHANGELOG section.

## 1. Land the batch

For each fix branch (one worktree per branch under `../vukem-worktrees/<name>`, created only with the owner's explicit approval):

1. The branch agent reports: commits, validation commands and results, public API changes, what apps must change.
2. An independent reviewer (code-reviewer agent, read-only, clean context) reviews `git diff main...<branch>`. Security-sensitive changes always get one.
3. Findings go back to the branch agent; it fixes test-first and rebases on the current `main`.
4. Merge with `git merge --no-ff`, run the affected tests (and `go test ./internal/cli/ -run 'EndToEnd$'` for scaffold changes), push.
5. Remove the worktree and the merged branch.

## 2. CI gate

- `gh run list --branch main --workflow CI --limit 1` until the run for the merge commit finishes; every job must pass.
- A red integration job is often test hygiene, not product code: for example a `t.Cleanup` using `t.Context()` (already cancelled) leaves tables behind on the shared Postgres. Fix the root cause, push, wait again.

## 3. Release the core

```sh
go run ./internal/tools/release -push vX.Y.Z
```

The tool generates the section from `.config/cliff.toml` (every entry links its commit), commits `chore(release): vX.Y.Z`, tags, pushes and publishes the GitHub release.

## 4. Tag nested modules

```sh
for m in $(git ls-files '*/go.mod' | grep -E '^(drivers|contrib)/' | xargs -n1 dirname); do
  t=$(git tag -l "$m/v*" --sort=-v:refname | head -1)
  echo "$m last=${t##*/} changes=$(git log --oneline ${t:+$t..}HEAD -- $m | wc -l)"
done
```

- Look at `git diff --stat <last tag>..HEAD -- <module>`: test-only changes need no tag.
- `cd <module> && GOWORK=off go test ./...` must pass against the published core before tagging.
- Breaking change (`!` commit) → minor bump while below v1. Annotated tag `<dir>/vX.Y.Z`, push the tags.
- After tagging `contrib/stripe`, bump `stripeVersion` in `internal/cli/billing.go` (pinned by `install:billing`; `TestStripeVersionIsLatestTag` fails until it matches the newest tag) and commit it for the next core release.

## 5. Warm the Go proxy

Poll until every new version answers 200 (the proxy and sum.golang.org cache 404s for a few minutes after a push):

```sh
curl -s -o /dev/null -w '%{http_code}' https://proxy.golang.org/github.com/!alex!galhardo/vukem/@v/vX.Y.Z.info
curl -s -o /dev/null -w '%{http_code}' https://proxy.golang.org/github.com/!alex!galhardo/vukem/<dir>/@v/vA.B.C.info
```

## 6. Bump the reference apps

Ask each app agent to bump before any feature work, with the upgrade notes from `docs/upgrade/` and the list of adoptable features. Each reports CI green and a healthy production probe (`/up?checks=1` or `/ready`).

- Platform settings that only the new version understands (example: `MAIL_MAILER=mailtrap` in v0.26.0) are set **after** the bumped app is live; an unknown value fails the boot.
- Agents never change Railway variables or other production config without the owner's approval relayed by the main session. An approval for one app does not extend to the others.
- Verify the change for real (for mail: trigger a password reset and read the Mailtrap sandbox through its API).

## 7. Record it

Update `status.md`, `framework-feedback/audit.md` and each app's `framework-feedback/*.md` entry status ("fixed in vX.Y.Z"), then triage the new open entries into the next batch.
