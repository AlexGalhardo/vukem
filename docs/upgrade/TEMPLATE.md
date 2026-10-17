# Upgrading to v1.N

<!-- Copy this file to docs/upgrade/1.N.md when cutting v1.N.0, fill every section, delete the ones that stay empty and link it from README.md in this folder. -->

**Estimated effort:** low / medium / high, and why.

## Before you start

- Supported Go versions for this release and any change to the `go.mod` minimum.
- Upgrade the CLI and the modules together:

```sh
go install github.com/AlexGalhardo/vukem/cmd/vukem@v1.N.0
go get github.com/AlexGalhardo/vukem@v1.N.0
go get github.com/AlexGalhardo/vukem/drivers/<name>@v1.N.0 # each driver you use
go mod tidy
go fix ./... # applies //go:fix inline rewrites for deprecated APIs
```

## Required changes

Changes every app must make (migrations to run, configuration keys renamed, security defaults that changed). Each item: what changed, why, before/after code.

## Deprecations

| Deprecated | Replacement | Removed in |
| --- | --- | --- |
| `pkg.Old` | `pkg.New` (`go fix` rewrites it) | v2 |

## New features worth adopting

Short list with links to the docs.

## Generated code

Files that `vukem new` or the installers now generate differently, and whether existing apps should copy the change.

## Verification

```sh
vukem migrate
go vet ./...
go test ./...
```
