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
- Comment only the *why*; known limits use `// shortcut: <limit>, <when to upgrade>`.
