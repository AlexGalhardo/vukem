---
paths:
  - "**/*.go"
---

# Go rules

- Format with `golangci-lint fmt`, lint with `golangci-lint run ./...` before finishing.
- `context.Context` is the first parameter of any function doing I/O; never store it in a struct.
- Wrap errors with context: `fmt.Errorf("load user %d: %w", id, err)`. Inspect with `errors.Is` / `errors.AsType`.
- Sentinel errors (`ErrX`) for conditions, typed errors for data. Error strings lowercase, no trailing punctuation.
- No package-level mutable state, no `init()` registration, no reflection-based DI or discovery.
- Accept small interfaces defined by the consumer; return concrete types.
- Prefer stdlib: `net/http` ServeMux, `log/slog`, `slices`, `maps`, `iter`, `crypto/rand`, `os.Root`, `embed`.
- Every exported identifier has a doc comment starting with its name and ending with a period.
- Use modern Go (1.27): `for i := range n`, `min`/`max`, `errors.AsType`, `t.Context()`, `b.Loop()`, `new(expr)`.
- Windows 11 is a first-class target: `filepath` for OS paths, close files/roots/DB handles before deleting (use `t.Cleanup`), accept CRLF input, no `sh`/`bash` in code paths, no cgo (see docs/architecture.md#platform-support).
- Comment only the *why*; known limits use `// shortcut: <limit>, <when to upgrade>`.
- Deprecated wrappers use `//go:fix inline`; name their parameters differently from common caller variables (`bundle`, `config`): an inliner argument swap was reported when a parameter name matches a caller's argument name (v0.26.0, B2; not reproduced on Go 1.27.2) — run `go run ./internal/tools/fixinline` (part of `task vet` and CI).
- Root package code goes into the matching theme file (`app.go`, `options.go`, `boot.go`, `serve.go`, `validate_config.go`, `services.go`, `instrument.go`, `http.go`, `database.go`, `background.go`, `boost.go`, `crossorigin.go`, `fuzz.go`, `command.go`, `codegen.go`), not a new root file.
- Google Go Style ([guide](https://google.github.io/styleguide/go/); skill `google-go-style`): name the results of a function that returns two or more values of the same type (`(pkg, name string)`, never naked returns), return early instead of `else` after `return`, and never let a parameter shadow an import (`flags`, not `fs`).
- `panic` only for programmer errors at registration or boot, in `Must*` helpers, and to re-raise a recovered panic; input from a request, file or data returns an `error`. Tests report `got`/`want`, not "expected".
- The `revive` rules in `.golangci.yml` enforce the style rules; `struct-tag` (the `validate` tags) and `modifies-value-receiver` (`orm.Query` builders) stay off on purpose. Documented exceptions: per-request `context.Context` fields and the `fmt.Errorf("%w: detail", ErrX)` convention (site `architecture/best-practices`).
- Never add a `//nolint:gocognit`: split the function instead. The count in non-test code is a budget that never rises (`GOCOGNIT_BUDGET` in `.github/workflows/ci.yml`, now 4): the four left are recorded exceptions in `contrib/graphql` (docs/maintenance.md#metrics); lower the budget when you remove one.
