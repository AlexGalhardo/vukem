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
- After pushing tags, poll the Go proxy until the new versions answer 200 (it caches 404s for a few minutes); only then ask the apps to bump.
- Every breaking change has a `!` commit with a `BREAKING CHANGE:` footer and an entry in `docs/upgrade/` (the apicheck gate expects it).
- Settings that only a new version understands are set on the platform after the bumped app is deployed (unknown values fail the boot).
