# Stability policy

Vukem 1.x follows the [Go 1 compatibility promise](https://go.dev/doc/go1compat) for its public API: code that builds and passes its tests against `v1.N.M` keeps doing so against every later `v1` release.

## What is covered

- Every exported identifier of the core module `github.com/AlexGalhardo/vukem` and of each published driver and contrib module (`drivers/*`, `contrib/*`): names, signatures, struct fields, interface method sets and documented behaviour.
- CLI commands, flags and the layout `vukem new` generates, so generated apps keep building after a CLI upgrade.
- Configuration keys (`APP_*`, `DATABASE_URL`, …) and their defaults, except security defaults (see below).
- On-disk formats the framework owns: migration tables, session, cache, queue and audit tables, encrypted cookie and column formats.

A CI gate (`apidiff`, see [tooling.md](tooling.md#api-compatibility)) compares every change with the latest `v1` tag and fails on incompatible API changes.

## What is not covered

- Packages under `internal/`, `testdata/`, and identifiers documented as experimental.
- Exact error message text, log output and HTML markup of the built-in error and dashboard pages.
- Performance characteristics (they may improve or regress within reason; see [performance.md](performance.md)).
- Security fixes: when a default is unsafe, it is changed in a minor or patch release even if that breaks an app relying on the unsafe behaviour. Such changes are called out under **Security** in the changelog, with an `Insecure…` opt-out when one is reasonable. A security fix that breaks an exported API also needs an entry in the `apicheck` [exception list](tooling.md#security-exceptions).
- Bugs: behaviour that contradicts the documentation is fixed.

Breaking changes outside these exceptions only ship in a new major version with a new module path (`/v2`).

## Deprecation

- An identifier is deprecated with a `// Deprecated:` comment naming its replacement, plus `//go:fix inline` when the replacement is mechanical, so `go fix ./...` migrates call sites.
- Deprecated identifiers stay for at least **two minor releases** and are removed only in the next major version.
- Deprecations are listed under **Deprecated** in the changelog and in the upgrade guide of the release that introduces them ([upgrade/](upgrade/README.md)).

## Supported Go versions

Each Vukem release supports the **two latest Go releases** (for example Go 1.27 and Go 1.28 once 1.28 ships), matching the Go project's own support window. The minimum version in `go.mod` only moves forward in a minor release and is listed in its upgrade guide.

## Security support window

| Release line | Fixes |
| --- | --- |
| Latest `v1` minor | All bug and security fixes |
| Previous `v1` minor | Security fixes for 6 months after the next minor ships |
| `release/1.x` LTS branch (see [CONTRIBUTING](../.github/CONTRIBUTING.md#release-cadence)) | Security fixes for 12 months |
| Latest `v0` minor (BETA, until `v1.0.0`) | Security fixes, released as the next `v0` patch or minor |
| Older `v0` minors | No fixes; upgrade to the latest `v0` minor, then with the [0.x → 1.0 guide](upgrade/1.0.md) |

Reporting and disclosure are described in [SECURITY.md](../.github/SECURITY.md).
