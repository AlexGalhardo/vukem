---
paths:
  - "**/*_test.go"
  - "**/testdata/**"
---

# Testing rules

- Write the failing test first, then the code.
- Table-driven tests with `t.Run` subtests and `t.Parallel()` when safe.
- Assertions: stdlib + `github.com/google/go-cmp/cmp` (`cmp.Diff(want, got)`). No testify.
- Use `t.Context()`, `t.TempDir()`, `t.Helper()`; inject `getenv` instead of `t.Setenv` when possible.
- No network or Docker in unit tests; integration tests need `//go:build integration`.
- Time-dependent code uses `testing/synctest` or the fake clock, never `time.Sleep`.
- Golden files live in `testdata/` and are refreshed with `-update`.
- Failure messages follow `Func(args) = got, want want`.
