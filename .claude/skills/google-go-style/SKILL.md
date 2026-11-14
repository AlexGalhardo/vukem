---
name: google-go-style
description: Audit and check vukem's Go code against the Google Go Style Guide (guide, decisions, best practices) and vukem's own exceptions. Load it at the start of every work session in this repo, and run it before finishing any change to Go code, when reviewing a PR, or when asked to "audit the code style", "check Google best practices" or "google-go-style".
---

# google-go-style

Source of truth: <https://google.github.io/styleguide/go/> (the [guide](https://google.github.io/styleguide/go/guide), [decisions](https://google.github.io/styleguide/go/decisions) and [best practices](https://google.github.io/styleguide/go/best-practices)). vukem's summary and its documented exceptions live in `site/content/docs/architecture/best-practices.mdx` ("Go style"). When the guide and this file disagree, the guide wins and this file is fixed.

## When to use

- At the start of every session: load this skill so the rules below shape every edit.
- Before calling a Go change done: run the check on the packages you touched.
- When asked for a full style audit: run it on `./...` and every module in `go.work`.

## 1. Automated check (most rules)

`.golangci.yml` enforces the rules a linter can see. Run them on what you touched:

```sh
golangci-lint fmt ./pkg/...          # gofumpt + gci + golines
golangci-lint run ./pkg/...          # revive, gocritic, errorlint, nakedret, godot, ...
go vet ./pkg/...
```

For an audit, run `golangci-lint run ./...` at the root and in each nested module (`contrib/*`, `drivers/*`). Zero issues is the bar; never add `//nolint` to pass (see `.claude/rules/go.md`).

Covered by the linter (do not re-check by hand): MixedCaps and initialisms (`ID`, `URL`), a doc comment on every exported name that starts with the name and ends with a period, lowercase error strings without punctuation, `context.Context` first, short consistent receiver names, import groups, no naked returns in long functions, `errors.Is`/`errors.As` instead of `==`, early return and no `else` after `return`, no import shadowing, no dot imports, `any` instead of `interface{}`, named results when a function returns two or more values of the same type (`confusing-results`), and a parameter that shadows an import (`import-shadowing`, e.g. `fs` next to `io/fs`). Two revive rules stay off on purpose: `struct-tag` (the `validate` tags) and `modifies-value-receiver` (the immutable `orm.Query` builders).

## 2. Manual checklist (what a linter cannot judge)

Go through each item for the diff (or the whole tree in an audit) and report findings as `file:line — rule — fix`:

1. **Naming**
   - No `Get` prefix on getters (`Name()`, not `GetName()`); generated protobuf code is exempt.
   - No `util`, `common`, `helpers`, `misc` or `base` packages; name a package after what it provides.
   - No repetition between package and identifier (`orm.Query`, not `orm.ORMQuery`); short names in small scopes, descriptive ones in large scopes.
2. **Errors**
   - Wrap with context: `fmt.Errorf("load user %d: %w", id, err)`, with `%w` at the end.
   - vukem exception: `fmt.Errorf("%w: detail", ErrSentinel)` (category first, then detail) is the house convention for sentinel errors; keep it.
   - Return errors for anything driven by input (requests, files, config, data). `panic` only for programmer errors at registration or boot, invariant violations, and `Must*` helpers.
   - Do not log and also return the same error; handle it once.
3. **Context**
   - Never store a `context.Context` in a long-lived struct.
   - Documented exceptions, per-request or short-lived state: `drivers/otel` `serverRequest`, `inertia` `resolver`, `sse.Stream`, the `sse` hub's lifetime context, and `schema.Schema` (exported; changing it breaks the API).
   - Do not add new ones without updating the exception list in the docs.
4. **Interfaces**: define them where they are consumed, keep them small, return concrete types, and do not create an interface before a second implementation needs it.
5. **Tests**
   - Failure messages read `Func(%v) = %v, want %v` or `got %v, want %v`; never "expected" (a stray-event message such as "unexpected message %v" is fine).
   - Diffs use `cmp.Diff(want, got)` with the `(-want +got)` legend.
   - Table-driven where cases repeat.
   - Helpers call `t.Helper()`.
   - Use `t.Fatal` only when the test cannot continue; prefer `t.Error` so one run shows every failure.
6. **Documentation**: comments explain why, not what. Package docs live in `doc.go` or the main file. Examples (`Example*`) compile.
7. **Simplicity**
   - Prefer the standard library.
   - No globals or `init()` registration.
   - No reflection-based DI.
   - Least mechanism first: plain code, then a function, then an interface.

## 3. Output

- For a check: the commands run and their result, then any manual findings (or "no findings").
- For an audit: a table `rule | count | where | action` (fix now, document as exception, or out of scope), then the fixes as microcommits (one rule per commit, `style(pkg): ...` or `refactor(pkg): ...`), and an update to `site/content/docs/architecture/best-practices.mdx` when an exception is added or removed (refresh `site/content/last-updated.json` with `npm run last-updated` in `site/`).
