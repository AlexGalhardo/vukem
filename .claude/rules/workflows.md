---
paths:
  - ".github/**"
  - ".config/ruleset-protect-main.json"
  - "Taskfile.yml"
  - "**/go.mod"
---

# Workflow and supply-chain rules

- Pin every action by full commit SHA with a `# vX.Y.Z` comment; resolve the SHA with `gh api repos/<owner>/<repo>/git/ref/tags/<tag>`, never guess it. Docker images are pinned by digest (`docker buildx imagetools inspect`).
- Top-level `permissions: contents: read`; raise a permission only on the job that needs it. Never `pull_request_target`; never interpolate `${{ github.event.* }}` into `run:` (pass it through `env:`).
- Validate with `actionlint` before committing.
- Renaming a CI job that is a required check means updating `.config/ruleset-protect-main.json` in the same PR, then re-applying it (docs/maintenance.md#pull-request-flow).
- A new Go module goes into `gomod.directories` in `.github/dependabot.yml`, the `vuln.yml` matrix and `MODULES` in `Taskfile.yml`.
- A new fuzz target goes into the `fuzz.yml` matrix (`pkg` relative to `module`).
- Scheduled workflows report failures through `ci-failure.yml`; add a new scheduled workflow to its `workflows:` list.
- Never add `//nolint:gocognit`; lower `GOCOGNIT_BUDGET` in `ci.yml` when one is removed.
- The Scorecard workflow keeps only the scorecard job (publish_results restrictions); add other jobs elsewhere.
