## Summary

<!-- What does this change and why? End with the plan task or finding, e.g. "Refs: P.3.2". -->

## Checklist

- [ ] PR title is a Conventional Commit (`type(scope): description`, `!` for a breaking change); body has `Refs: <task>`
- [ ] `task ci` passes locally (lint, vet, `mod:check`, `snippets:check`, `test -short`)
- [ ] Every changed security default has its row in `docs/security.md` and a `TestSecurity_*` test
- [ ] New exported API has godoc, a site page and a row in `docs/api/review.md`; a rename keeps a deprecated `//go:fix inline` alias
- [ ] No new `//nolint` without a one-line reason; no new `//nolint:gocognit`
- [ ] No new dependency without a decision in `docs/brainstorming.md`
- [ ] Workflows: actions pinned by full SHA with a `# vX.Y.Z` comment; minimal `permissions`
- [ ] `CHANGELOG.md` untouched (generated from commits by `task release`)
- [ ] Breaking changes have a `BREAKING CHANGE:` footer and an entry in `docs/upgrade/`
