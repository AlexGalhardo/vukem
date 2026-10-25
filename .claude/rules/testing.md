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
- `t.Context()` is cancelled before `t.Cleanup` functions run: cleanup that talks to a database (drop a table, delete rows) uses `context.Background()`, or the cleanup silently does nothing and leaks state into other tests sharing the service.
- Integration tests on a shared database create and drop only their own tables.
- Tests that compile or boot programs, open a browser or run a helper binary skip under `testing.Short()`: `task test` runs `-short`, `task test:e2e` runs them, CI runs everything.
- A parser of untrusted input (headers, bodies, file formats, patterns) gets a native `Fuzz*` target whose oracle mirrors the parser's own normalisation, plus an entry in `.github/workflows/fuzz.yml`; a crasher becomes an `f.Add` seed or a `testdata/fuzz/<Target>/` file with the fix.
