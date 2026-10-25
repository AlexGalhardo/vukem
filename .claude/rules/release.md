---
paths:
  - "internal/tools/release/**"
  - ".config/**"
  - "drivers/**/go.mod"
  - "contrib/**/go.mod"
  - "docs/upgrade/**"
---

# Release rules

- Follow [Release process](../../docs/tooling.md#release-process); the `vukem-release` skill has the full checklist.
- Release only from a green GitHub Actions run on `main` (`gh run list --branch main --workflow CI`); fix red jobs at the root cause first.
- Tag nested modules only when they have non-test changes since their last tag, after `GOWORK=off go test ./...` passes inside the module.
- Push version tags **one at a time**, never `git push origin --tags`: GitHub creates no push event for a push of more than three tags, so `on: push: tags` never fires and nothing is published, silently. Confirm each push with `gh run list`; recover a tag that started no run with `-publish-only` (core) or the `release.yml` `workflow_dispatch` version input (apps) — never by moving the tag.
- Never give a release-notes extraction a placeholder fallback: an empty section fails the release instead ([changelog extraction](../../docs/tooling.md#changelog-extraction-in-app-workflows)).
- After pushing tags, poll the Go proxy until the new versions answer 200 (it caches 404s for a few minutes); only then ask the apps to bump.
- Every breaking change has a `!` commit with a `BREAKING CHANGE:` footer and an entry in `docs/upgrade/` (the apicheck gate expects it).
- Settings that only a new version understands are set on the platform after the bumped app is deployed (unknown values fail the boot).
- An open `ci-failure` issue (failed Fuzz, Vulnerabilities or Scorecard run) blocks the release until it is triaged.
- Once the `protect-main` ruleset requires pull requests, `-push` to `main` is rejected: release through a PR as in [docs/maintenance.md](../../docs/maintenance.md#releasing-through-a-pull-request), then record the Scorecard score in the "Scorecard tracking" issue.
