# Commit rules

- Conventional Commits 1.0, English: `type(scope): description`.
- Types: feat, fix, perf, refactor, docs, test, build, ci, chore, style, revert.
- Scope = package or area (`router`, `orm`, `cli`, `postgres`, `repo`, `docs`…), lowercase.
- Description: imperative, lowercase start, no trailing period, header ≤ 100 chars.
- Breaking change: `!` after type/scope; describe the impact in a `BREAKING CHANGE:` footer.
- Reference the plan task in the body: `Refs: P.3.2`.
- One logical change per commit. Validator: `go run ./internal/tools/commitlint <file>`.
