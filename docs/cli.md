# CLI

The `vukem` command: scaffolding, generators, dev server, and delegation to the app binary. Decision `A7` in [brainstorming.md](brainstorming.md).

## Two binaries

| Binary | Installed by | Knows about the app? | Commands |
| --- | --- | --- | --- |
| **`vukem`** (global) | `go install github.com/AlexGalhardo/vukem/cmd/vukem@latest` | No — works on files | `new`, `make:*`, `install:*`, `serve`, `build`, `key:generate`, `version`, `upgrade` |
| **app binary** (`cmd/app`) | built from the user's project | Yes — boots the app | `serve:http` (default), `migrate*`, `db:*`, `route:list`, `queue:work`, `schedule:*`, `about`, user commands |

When `vukem` receives a command it does not own, it delegates to the project's binary: `cmd/app` is built into `.vukem/bin/app` (rebuilt only when a `.go`, `go.mod` or `go.sum` file is newer than the binary) and run with the same arguments; its exit code is passed through. So `vukem migrate` works from the project root exactly like `php artisan migrate`. Projects ignore `.vukem/` in `.gitignore`.

The global CLI never imports user code and never wraps `go build` with custom semantics — it shells out to the standard toolchain.

## Command naming

`namespace:action`, kebab-case actions, consistent flags:

| Flag | Meaning |
| --- | --- |
| `--force` | overwrite files / allow destructive commands in production |
| `--env <name>` | select `APP_ENV` |
| `--json` | machine-readable output where it makes sense |
| `-h`, `--help` | help (stdlib `flag` usage, grouped by namespace in `vukem list`) |

Exit codes: `0` success, `1` failure, `2` usage error.

## Commands by phase

### Prototype

| Command | Description |
| --- | --- |
| `vukem new <name> [--module path] [--api]` | Create a project from the embedded starter (HTMX + SQLite; JSON-only with `--api` from ALPHA), run `go mod tidy`, generate `APP_KEY`, `git init` |
| `vukem serve` | Build + run `cmd/app`, restart on `.go` changes (own watcher using polling + `os.Stat`; no fsnotify dep), reload templates without restart; runs `npm run dev` when a Vite config exists |
| `vukem make:controller <Name> [--resource] [--api]` | Controller struct + constructor; `--resource` adds the 7 actions |
| `vukem make:model <Name> [-m] [-f] [-c]` | Model; `-m` migration, `-f` factory, `-c` controller |
| `vukem make:migration <name>` | Timestamped migration; stub chosen by `create_`/`add_`/`alter_` prefix; appended to `migrations/registry.go` |
| `vukem make:middleware <Name>` | `func(http.Handler) http.Handler`; register it with `r.Use`, `group.Use` or `route.Use` |
| `vukem make:request <Name>` | Form request struct |
| `vukem make:command <Name>` | App command, appended to `bootstrap/commands.go` |
| `vukem make:provider <Name>` | Provider, appended to `bootstrap/providers.go` |
| `vukem make:test <Name>` | Test file using the fluent test client |
| `vukem key:generate [--show]` | Write `APP_KEY` to `.env` |
| `vukem build` | `vite build` (if present) then `go build -trimpath -ldflags "-s -w"` with embedded assets |
| app: `migrate`, `migrate:rollback`, `migrate:reset`, `migrate:fresh`, `migrate:status` | Migrations |
| app: `route:list [--json]` | Route table |
| app: `about` | Versions, environment, drivers, config summary (secrets redacted) |

### ALPHA

Shipped: `new --api`, `install:api`, `make:resource`, `make:factory`, `make:seeder`, `make:policy`, `make:mail`, `make:event`, `make:listener [--event Name] [--async]`, `install:auth [--social github,google]`, app: `db:seed`, `db:wipe` (both need `--force` in production). Planned: `stub:publish`, app: `db` (opens `sqlite3`/`psql` with the configured URL). `storage:link` was replaced by `storage.Handler` (see subsystems.md).

### BETA

`make:job`, `install:inertia`, `openapi:export`, `codegen:routes`, `ssr:start`, app: `queue:work`, `queue:retry`, `queue:failed`, `schedule:run`, `schedule:work`, `schedule:list`, `down`/`up` (maintenance mode).

## Generators and stubs

- Stubs are `text/template` files embedded in `internal/cli/stubs/` (`embed.FS`).
- `vukem stub:publish` copies them into the project's `stubs/`; local stubs take precedence.
- Stub data: `.Name` (Pascal), `.Snake`, `.Kebab`, `.Camel`, `.Plural`, `.Module`, plus `.Table`, `.Timestamp`, `.ID`, `.Func` for migrations and `.Resource`/`.API` for controllers. Unknown keys are errors.
- Flags may follow the name: `vukem make:model Post -m -c`.
- Generated Go files are passed through `go/format` before writing.
- Generators refuse to overwrite existing files without `--force`.
- `make:migration` picks the stub from the name: `create_<table>_table` creates the table, `add_x_to_<table>_table` / `alter_<table>_table` alter it, anything else is blank.

## Codemods

Registration happens by editing Go source, not by reflection or `init()`:

- `internal/codemod` parses the target file with `go/parser`, finds the slice literal returned by a known function (`bootstrap.Providers()`, `bootstrap.Commands()`, `migrations.All()`), inserts the element and imports at the AST positions, and runs `go/format`. Running it twice changes nothing.
- If the marker is not found (user restructured the file), the generator prints the exact snippet to add manually instead of failing silently.
- Every codemod has golden-file tests.

## App commands

```go
type Command interface {
	Name() string                          // "reports:send"
	Description() string
	Flags(fs *flag.FlagSet)                // define flags/args on a stdlib FlagSet
	Run(ctx context.Context, app *vukem.App, args []string) error
}
```

`vukem make:command SendReports` generates this and appends it to `bootstrap/commands.go`. Commands run with the `console` environment (providers limited to `web` are skipped).

## Output

- Plain, colour only when stdout is a terminal and `NO_COLOR` is unset.
- `--json` for `route:list`, `migrate:status`, `about`.
- When an AI coding agent is detected (`CLAUDECODE`, `CODEX`, etc. env vars), verbose banners are suppressed (AdonisJS v7 idea).
