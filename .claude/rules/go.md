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
- Root package code goes into the matching theme file (`app.go`, `http.go`, `database.go`, `background.go`, `services.go`, `command.go`, `codegen.go`), not a new root file.
