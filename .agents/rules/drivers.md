---
paths:
  - "drivers/**"
---

# Driver module rules

- Each `drivers/<name>` is its own Go module (`go.mod`), tagged `drivers/<name>/vX.Y.Z`.
- Drivers import core; core never imports drivers; drivers never import each other.
- Register drivers explicitly (e.g. `postgres.Driver()` passed to a provider), never through `init()`.
- Published `go.mod` files must not contain `replace`; use `go.work` locally and `GOWORK=off` in CI.
- Every driver passes the shared conformance suite for its interface plus `//go:build integration` tests.
