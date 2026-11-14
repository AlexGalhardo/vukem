# CLI

The `vukem` command: scaffolding, generators, dev server, and delegation to the app binary. Decision `A7` in [brainstorming.md](brainstorming.md).

The user-facing manual (every command, flag, environment variable, error and example) is the site's [CLI reference](../site/content/docs/cli/reference.mdx); a test in `internal/cli` fails when a command or flag of the real registry is missing from it.

## Two binaries

| Binary | Installed by | Knows about the app? | Commands |
| --- | --- | --- | --- |
| **`vukem`** (global) | `go install github.com/AlexGalhardo/vukem/cmd/vukem@latest` | No — works on files | `new`, `make:*`, `install:*`, `serve`, `build`, `key:generate`, `key:rotate`, `codegen:rpc`, `version`, `doctor` |
| **app binary** (`cmd/app`) | built from the user's project | Yes — boots the app | `serve` (default: HTTP server, plus the queue worker and scheduler when enabled), `migrate*`, `db:*`, `route:list`, `queue:*`, `schedule:*`, `openapi:*`, `codegen:types`, `codegen:routes`, `codegen:models`, `codegen:client`, `view:check`, `about`, `key:generate` (prints a key, runs without booting), user commands |

When `vukem` receives a command it does not own, it delegates to the project's binary: `cmd/app` is built into `.vukem/bin/app` (rebuilt only when a `.go`, `go.mod` or `go.sum` file is newer than the binary) and run with the same arguments; its exit code is passed through. So `vukem migrate` works from the project root exactly like `php artisan migrate`. Projects ignore `.vukem/` in `.gitignore`.

The global CLI never imports user code and never wraps `go build` with custom semantics — it shells out to the standard toolchain.

## Command naming

`namespace:action`, kebab-case actions, consistent flags:

| Flag | Meaning |
| --- | --- |
| `--force` | overwrite files / allow destructive commands in production |
| `--json` | machine-readable output (`route:list`, `migrate:status`, `about`, `queue:failed`) |
| `-h`, `--help` | help (stdlib `flag` usage); `vukem list` groups the commands by namespace |

There is no `--env` flag: set `APP_ENV` in the environment or `.env`.

Generator output has one format: each line starts with a prefix, and an indented snippet may follow a `next:` line.

| Prefix | When | Example |
| --- | --- | --- |
| `created <path>` | a new file was written | `created app/models/post.go` |
| `updated <path>` | a codemod changed an existing file | `updated database/seeders/registry.go` |
| `next: <instruction>` | something to do by hand | `next: add vukem.WithAppSeeders(seeders.AllApp()...) to bootstrap/app.go` |
| `note: <remark>` | information that needs no action now | `note: the policy takes *models.User, ...` |

The `install:*` commands and `vukem new` print their own summaries and keep their format. The app binary suggests the closest command when one is mistyped (`./app migrte` prints `unknown command "migrte" (did you mean "migrate"?)`); with no close match it prints the full command list.

Exit codes: `0` success, `1` failure, `2` usage error.

## Commands by phase

### Prototype

| Command | Description |
| --- | --- |
| `vukem new <name> [--module path] [--api \| --templ \| --inertia react\|vue\|svelte \| --minimal] [--no-install] [--example todo\|finance\|none \| --no-example] [--prebuilt] [--pm bun\|npm\|pnpm\|yarn] [--docker] [--ci] [--deploy railway] [--license mit] [--standards] [--assets-image tag-or-digest] [--framework path]` | Create a project from the embedded starter (HTMX + Tailwind + SQLite by default; templ components instead of `html/template` views with `--templ`; JSON-only with `--api`; HTMX without database code or the SQLite driver with `--minimal`, which writes `.buildtags` with `vukem_nosqlite`), run `go mod tidy`, generate `APP_KEY`, `npm install` (frontend starters), `git init`, and write a production `Dockerfile` + `.dockerignore` for the stack, package manager and `.buildtags`. `--templ` also pins templ as a `go tool` and generates the components ([templ starter](frontend.md#templ-starter)). On a terminal without stack flags it asks for the stack (HTMX, HTMX + templ, Inertia → React/Vue/Svelte, API). It first asks *Example app?*: `todo` (default, also without a terminal) adds a tasks CRUD with validation, migration and tests in the style of the stack, `finance` a personal finance SaaS (forces Inertia React: auth with two-factor, the admin panel, workspaces on the `tenancy` package, Stripe billing per workspace, Resend mail with React Email, budgets, charts and CSV import; scaffolded by `scaffoldFinance`, guide in [finance-saas](../site/content/docs/getting-started/finance-saas.mdx)), `none` the empty starter; `--example` answers it and `--no-example` means `none`. With an example, a terminal then asks *Develop locally (full source)* or *Run ready-made in Docker*; `--prebuilt` answers the second without a terminal: it writes only `compose.yaml`, `.env` (with a generated `APP_KEY`) and `README.md` for the published image `ghcr.io/alexgalhardo/vukem-example-<todo\|finance>:<CLI version>` (`latest` for a development or pre-release build; `finance` adds PostgreSQL and the Mailpit catcher), prints `docker compose up` as the next step and runs no container. `--prebuilt` is a usage error with `--example none`, `--no-example`, `--minimal`, `--seed`, `--docker` or any stack, package-manager or standards flag, because there is no source for them to act on |
| `vukem serve [-no-vite] [-poll 500ms]` | Build + run `cmd/app`, restart on `.go` and `.templ` changes (own watcher using polling + `os.Stat`, every `-poll`; no fsnotify dep; `-no-vite` skips the Vite dev server), reload templates without restart; runs `go tool templ generate` before each build when `.templ` files exist, and `npm run dev` when a Vite config exists |
| `vukem make:controller <Name> [--resource] [--api]` | Controller struct + constructor; `--resource` adds the 7 actions |
| `vukem make:model <Name> [-m] [-f] [-c]` | Model; `-m` migration, `-f` factory, `-c` controller |
| `vukem make:migration <name>` | Timestamped migration; stub chosen by `create_`/`add_`/`alter_` prefix; appended to `migrations/registry.go` |
| `vukem make:middleware <Name>` | `func(http.Handler) http.Handler`; register it with `r.Use`, `group.Use` or `route.Use` |
| `vukem make:request <Name>` | Form request struct |
| `vukem make:command <Name>` | App command, appended to `bootstrap/commands.go` |
| `vukem make:bench <Name> [--force]` | Load-test scenario in `app/benchmarks/`, appended to `Bench()` in `bootstrap/bench.go` (created when missing; pass the list to `vukem.WithBench(Bench()...)`) |
| `vukem make:provider <Name>` | Provider, appended to `bootstrap/providers.go` |
| `vukem make:test <Name>` | Test file using the fluent test client |
| `vukem install:admin [--force] [--stack htmx\|inertia]` | Admin panel at `/admin` (needs `install:auth`; `--stack inertia` writes React pages over the panel JSON instead of server templates): `routes/admin.go`, `app/admin/`, audit-log migration, test, `ADMIN_EMAILS` ([admin.md](admin.md)) |
| `vukem install:flags [--force]` | Feature flags: `app/features/features.go` (`All()`), the `feature_flags` migration and `vukem.WithFlags(features.All()...)` in `bootstrap/app.go`; with the admin panel they are toggled at `/admin/_flags`. Safe to re-run: `app/features/features.go` is never overwritten (not even with `--force`) and an existing feature flags migration is reused (`--force` rewrites it in place) ([subsystems.md](subsystems.md#feature-flags)) |
| `vukem make:admin-resource <Model> [--force]` | Admin resource + policy for an existing model in `app/admin/<model>.go` and a test, registered in `app/admin/registry.go` |
| `vukem key:generate [--show] [--force] [--if-missing]` | Write `APP_KEY` to `.env`; an existing key (also `export APP_KEY=...` or `APP_KEY = ...`, as the config reads it) is kept unless `--force`; `--if-missing` writes one only when it is empty and never prints it |
| `vukem key:rotate` | Write a new `APP_KEY` to `.env` and move the old one to the front of `APP_PREVIOUS_KEYS` (keys are never printed). It refuses a malformed current or previous key, keeps `export`, indentation and inline comments, replaces `.env` atomically (through a symlink too), and warns when more than 3 previous keys remain; see [Key rotation](security.md#key-rotation) |
| `vukem ssr:start [-bundle] [-runtime]`, `vukem ssr:check [-url]`, `serve -with-ssr [-ssr-bundle] [-ssr-runtime] [-ssr-restart-delay]` | App commands: supervise the Inertia SSR server (restart on crash) and probe its `/health`; `serve -with-ssr` runs the same supervisor as the `ssr` runner of the app process (restarting after any exit, a missing bundle is a boot error), for one-container deploys |
| `vukem ssg:generate [-out dir] [-no-clean] [-path /page]` | App command: prerender each `-path` through the app HTTP handler and write `index.html` files under `-out` (default `dist`), replacing the directory unless `-no-clean` writes in place. Registered routes are not crawled. A missing `-path` or an extra argument is a usage error (exit 2). This does not start the Inertia SSR server ([ssg.md](ssg.md)) |
| `vukem install:billing [--force] [--plans basic,pro]` | Stripe subscriptions for the `install:auth` users (required first): plans config, billing columns and `subscriptions` table, Checkout/Portal routes, `POST /stripe/webhook` sync, `Subscribed`/`OnPlan` middleware, tests with a fake Stripe API; requires `contrib/stripe` v0.1.0. See [billing.md](billing.md) |
| `vukem install:inertia --force [--react\|--vue\|--svelte] [--no-install]` | Turn the HTMX starter into an Inertia app (replaces its pages, controllers and tests; `--force` required) and run `npm install` |
| `vukem down [--secret s \| --with-secret] [--retry 60s]`, `vukem up` | Maintenance mode: `storage/framework/down` makes `middleware.Maintenance` (in both default stacks) answer 503 with `Retry-After` (the `errors/503` view or problem+json); `/up` stays served. Visiting `/<secret>` sets a signed bypass cookie; the secret is stored hashed. An unreadable state file keeps the app down |
| `vukem boost:install` | Agent setup (Laravel Boost-style): guidelines block in `AGENTS.md` (re-runnable, text outside the markers is kept), `@AGENTS.md` in `CLAUDE.md`, skills in `.claude/skills/vukem-*`, and the `vukem-boost` MCP server in `.mcp.json`, `.cursor/mcp.json`, `.vscode/mcp.json` |
| `vukem version` | Print the CLI version |
| app: `bench:run [--scenario n] [--model open\|closed] [-c n \| --rate r] [-d d] [--stages s] [--max-workers n] [--timeout d] [--url u [--allow-remote h]] [--database-url u [--yes]] [--seed] [--threshold e] [--save f] [--compare f] [--tolerance p] [--json] [--list] [--ui [--ui-port p] [--no-open]] [--report f]` | Run the project scenarios (`vukem.BenchScenario`, `vukem.WithBench`) against the app booted in process on `127.0.0.1` with `APP_ENV=test` and a temporary SQLite database (never `DATABASE_URL`); `vukem bench` without a URL delegates here. Exit codes: 3 a threshold failed, 4 a regression against the `--save` baseline ([performance.md](performance.md#load-testing)) |
| `vukem bench [-c n \| -rate 200/s] [-d 10s] [-n count] [-m method] [-H "Name: value"] [-body s \| -body-file f] [-timeout d] [-connections n] [-max-workers n] [-no-keepalive] [-http2] [-allow-remote host] [-json] [-o file] [-quiet] [-ui [-ui-port p] [-no-open]] [-report f] <url>` | Load test a URL with the native `loadtest` engine: closed model (`-c`, default 10 workers) or open model (`-rate`), 10 s by default; `-body-file` at most 10 MiB; prints throughput, failures, latency percentiles and a histogram, or JSON with `-json`. Only loopback hosts unless `-allow-remote`; redirects are not followed; Ctrl+C prints the partial report ([performance.md](performance.md#load-testing)) |
| `vukem upgrade [--check] [--version vX.Y.Z]` | Update the CLI itself to the latest stable release (`bun upgrade` style). `go install ...@vX.Y.Z` when the binary is in `GOBIN`/`GOPATH/bin`, else the GoReleaser archive checked against `checksums.txt` SHA-256 (and cosign when installed); atomic replace, `vukem.exe.old` on Windows. HTTPS to GitHub hosts only, size-capped; `GITHUB_TOKEN` optional |
| `vukem doctor` | Report project checks (CLI version, Go runtime, the Go version against the `go.mod` minimum (`go_version`), lockfile, `APP_KEY` set or not, `APP_ENV` (a failed check outside `development`, `test` and `production`), SQLite directory, port, proxy, Vite manifest) and optional tools. `--json` prints one report. `--strict` exits 1 when a project check fails. A missing optional tool still exits 0 |
| `vukem stub:publish [name...] [--force]` | Copy the embedded `make:*` stubs (all, or the named ones: `controller`, `controller.go`, `controller.go.tmpl`) into `stubs/`; refuses an existing file without `--force` and checks every target before writing. A name that matches no stub or several is a usage error |
| `vukem update [--check] [--dry-run] [--go-only] [--js-only] [--json]` | Run inside a project. Moves the `vukem` requirement to the CLI's version, the `drivers/*` and `contrib/*` modules and every other direct Go dependency to the newest stable tag (module proxy, then `go mod tidy`), and every `package.json` dependency to the newest stable version written exactly (npm registry; htmx stays on 4.x, TypeScript within the `vue-tsc`/`svelte-check` peer range), then runs the lockfile's package manager `install` and `go build ./...`. `--dry-run` lists every `old -> new` and writes nothing; `--check` writes nothing and exits 1 when something is behind. Writes the starter notes to `.vukem/upgrade/`; never runs migrations or touches `.env` |
| app: `db [-force] [-print]` | Open `sqlite3` or `psql` for `DATABASE_URL`; the password goes in `PGPASSWORD`, never in argv; `-force` in production. `mysql://` and `mariadb://` are refused until their password handling is verified |
| `vukem studio [--port 4984] [--no-open] [--allow-production] [--database-url url] [--write] [--refresh d] [--mongo-url u] [--mongo-database d] [--redis-url u] [--dir d] [--bin path]` | Open vukem-studio, the data browser of the project's databases (`contrib/studio`, experimental). It installs the pinned release into `.vukem/bin` with `go install` (or runs `--bin`) and starts it on `127.0.0.1:4984` (SQL from `DATABASE_URL`, MongoDB from `MONGO_URL`, Redis from `REDIS_URL`; pages refresh by themselves). SQL: a table list with row estimates, a filterable and sortable grid with keyset paging, a row page with the tables that reference it, JSON Lines and CSV export of the current view (capped at 100000 rows), and a one-statement console that is read-only. Everything is read-only by default: `--write` adds row insert, update and delete (primary key required, the key typed back to delete), a write switch in the SQL console, and edits of documents and keys, each logged on stderr without values. Loopback only, a session token in the printed URL, strict CSP. It refuses `APP_ENV=production` (and an unset `APP_ENV`) without `--allow-production`, never prints credentials, and keeps database errors in its own log, off the page. Exit codes: 0 stopped with Ctrl+C, 1 error, 2 bad usage or a refusal |
| `vukem build [-o bin/app] [-ssr] [-ldflags ...]` | `go tool templ generate` (when `.templ` files exist), `vite build` (if present), then `go build -trimpath -ldflags "-s -w"` with embedded assets. `-ldflags "..."` appends to `-s -w` (e.g. `-X main.version=v1.2.3`, then `vukem.WithVersion(version)`); `-ssr` also runs `vite build --ssr` (error without a Vite config). Every build (`build`, `serve`, delegated app commands) adds `-tags` from the project's `.buildtags` file (one tag per line or comma-separated, `#` comments) |
| app: `migrate [--allow-destructive]`, `migrate:rollback`, `migrate:reset`, `migrate:fresh`, `migrate:status` | Migrations; in production `migrate` refuses a pending migration that drops a table or column without `--allow-destructive` |
| app: `migrate:check [--json]` | Finds migrations that collide (a column added twice, a drop of a missing column...) without a database, and pending ones older than the last that ran; exits 1 on any finding ([database.md](database.md#migrations)) |
| app: `migrate:diff [-name n] [-o dir] [--delete item]... [--rename t.old:t.new]... [-check]` | Writes a migration stub for the differences between the `vukem.WithModels` models and the database; a destructive item needs `--delete`; never applies anything |
| app: `db:pull [-o dir] [-table name]... [-models] [-mark-applied]` | Writes a baseline migration (and with `-models` Go structs) from the existing database |
| app: `db:run -file f\|- [-commit]` | Run a SQL script (or stdin) in one transaction on the app database; a dry run that rolls back unless `-commit` |
| app: `db:backup -to file`, `db:backup -restore -from snapshot -to file` | SQLite snapshot with `VACUUM INTO` and an integrity check, or a restore of that file into another directory |
| app: `hash:make [-env NAME] [-stdin]` | Print the argon2id hash of a password (from `$PASSWORD`, the variable `-env` names, or stdin) |
| app: `cache:prune`, `session:prune` | Delete expired rows of the database cache and of the database session store ([subsystems.md](subsystems.md)) |
| app: `route:list [--json] [--method GET] [--path /api] [--name posts.]` | Route table with a summary column when a route has one; the filters also apply to `--json` |
| app: `queue:work [-queue q] [-concurrency n] [-sleep d] [-once] [-stop-when-empty]`, `queue:failed [-json] [-limit 100]`, `queue:retry`, `queue:forget` | Queue workers and failed jobs; `queue:failed` lists the newest 100 by default, `-limit 0` lists all ([subsystems.md](subsystems.md#queue)) |
| app: `schedule:run`, `schedule:work`, `schedule:list` | Scheduler ([subsystems.md](subsystems.md#scheduler)) |
| app: `api:fuzz [-prefix] [-url] [-image] [-docker] [-dry-run] [-max-examples n] [-workers n] [-rate-limit-bypass] [-report-dir dir]` | Run a pinned Schemathesis Docker image against the running app from the exported OpenAPI document; credential from `VUKEM_FUZZ_TOKEN` or `vukem.WithFuzzAuth`; `-dry-run` prints the command and config (site page rest-apis/fuzz-testing) |
| app: `openapi:export [-prefix /api]` | Print the OpenAPI 3.1 document of the routes ([api.md](api.md#openapi)); `-prefix` keeps only routes under that path prefix, overriding `OPENAPI_PREFIX`; runs in warmup mode (no provider `Boot`, no database, `APP_KEY` optional) |
| app: `openapi:serve [-addr]` | Preview the Scalar API reference locally (register `apidocs.Command()`) |
| app: `codegen:types [-zod] [-o file] [-prefix /api]` | TypeScript types for every schema and operation (`<Op>Body`, `<Op>Query`, `<Op>Response`), plus Zod 4 schemas (`<Name>Schema`, `<Op>BodySchema`) with `-zod`; `-prefix` keeps only routes under that path prefix, overriding `OPENAPI_PREFIX`; runs in warmup mode (no provider `Boot`, no database, `APP_KEY` optional) |
| app: `view:check [-strict] [-json]` | Check the typed `html/template` views (`view.Of`, `vukem.WithViewPages`) against their Go types, route names and htmx `#id` targets; prints `file:line:col: code: message`, exits 1 on problems; warmup mode, no connections; `vukem serve` runs it after each rebuild and only warns ([frontend.md](frontend.md#typed-views-and-viewcheck)) |
| app: `codegen:pages [-o file] [-check]` | `resources/js/page-props.ts` for the typed Inertia pages (`inertia.Define`, `inertia.Config.Pages` and `SharedType`): one type per page, `PageProps`, `SharedProps`, `Page<N>`; fails with `no typed pages` without `Config.Pages`; `-check` writes nothing and fails when stale; warmup mode ([frontend.md](frontend.md#typed-pages)) |
| app: `codegen:routes [-o file]` | Typed `routes.ts` for the frontend (`route(name, params, query)`, `RouteName`, `routes`); default `resources/js/routes.ts`, `-o -` prints to stdout; runs in warmup mode, no connections ([frontend.md](frontend.md)) |
| app: `codegen:models [-o file] [-pkg name] [-check]` | Typed column references (`cols.Users.Email.Eq(...)`) for the models listed with `vukem.WithModels(orm.ModelOf[T]()...)`; default `app/models/cols/cols_gen.go`, `-o -` prints to stdout, `-check` writes nothing and fails when the file is out of date; runs in warmup mode, no connections ([database.md](database.md#typed-columns)) |
| app: `codegen [-check]` | Regenerates the generated files that exist (`resources/js/routes.ts`, `resources/js/page-props.ts`, `resources/js/api.ts`, `internal/apiclient/client.go`, and `app/models/cols/cols_gen.go` for an app that lists its models with `vukem.WithModels`, which keeps the package name the file declares; an app without `WithModels` skips it) with the flags their header records, writing only when the bytes change; `-check` writes nothing and fails when one is stale (CI); `vukem serve` runs it beside the restarted app after every successful build unless `-no-codegen` (it never delays the restart, stops after 60 s, with `PORT=0`, and a failure or timeout is a warning in the log); warmup mode |
| app: `codegen:client [-lang ts\|go] [-style result\|throw] [-tanstack] [-check] [-o file] [-package name] [-prefix /api]` | Typed API client from the OpenAPI document: TypeScript `createClient` (default `resources/js/api.ts`) or a stdlib-only Go `Client` (default `internal/apiclient/client.go`, package `apiclient`); `-o -` prints to stdout; `-prefix` keeps only routes under that path prefix (overriding `OPENAPI_PREFIX`), so page routes stay out of the client; runs in warmup mode ([api.md](api.md#client-generation)) |
| `vukem make:proto <Name> [--force]` | gRPC service skeleton `proto/<name>/v1/<name>.proto` (package `<name>.v1`, `go_package` under `gen/`, one `Get<Name>` RPC), plus `buf.yaml` and a `buf.gen.yaml` running `protoc-gen-go` and `protoc-gen-connect-go` through `go tool` when they do not exist yet; prints the `go get -tool` lines ([api.md](api.md#code-generation)) |
| `vukem codegen:rpc [--protoc] [-proto dir] [-out dir]` | Generate Go and connect-go code: `buf generate` with `buf.gen.yaml` (`go tool buf` when buf is a tool of the module, else `buf` on `PATH`), otherwise `protoc` with the plugins built by `go tool -n`; refuses to run until both plugins are `tool` directives in `go.mod` ([api.md](api.md#code-generation)) |
| app: `about` | Versions, environment, drivers, config summary (secrets redacted) |

### ALPHA

Shipped: `new --api`, `install:api`, `make:resource`, `make:factory`, `make:seeder [--app]` (`--app`: an `AppSeeder` that receives the booted app, registered in `AllApp()`; pass it with `vukem.WithAppSeeders(seeders.AllApp()...)`), `make:policy`, `make:mail`, `make:event`, `make:listener [--event Name] [--async|--queued]`, `make:job`, `install:auth [--social github,google] [--social-only github,google] [--2fa] [--passkeys] [--locales en,es,pt-BR]` (`--2fa`: TOTP two-factor authentication, `--passkeys`: passkey sign-in, both also on a project that already ran `install:auth`; see [authentication.md](authentication.md#two-factor-authentication-installauth---2fa) and [passkeys](authentication.md#installauth---passkeys)), app: `db:seed [Name...] [--class Name] [seeder flags]` (runs every seeder, or the named ones in order; flags after the names go to the seeders that implement `vukem.FlagSeeder`), `db:wipe` (both need `--force` in production). `storage:link` was replaced by `storage.Handler` (see subsystems.md).

### BETA

Shipped: `make:proto`, `codegen:rpc`, app: `openapi:export`, `codegen:types`, `codegen:routes`, `codegen:models`, `codegen:client`, `openapi:serve` (via `apidocs.Command()`), `queue:work`, `queue:failed`, `queue:retry`, `queue:forget`, `schedule:run`, `schedule:work`, `schedule:list` (see the table above).

Also shipped: `install:inertia`, `ssr:start`, `ssr:check`, `down`/`up` (maintenance mode).

## Generators and stubs

- Stubs are `text/template` files embedded in `internal/cli/stubs/` (`embed.FS`).
- A file in the project's `stubs/` with the same name (`stubs/controller.go.tmpl`) takes precedence over the embedded stub. Run `vukem stub:publish [name...]` to copy the embedded stubs there.
- Stub data: `.Name` (Pascal), `.Snake`, `.Kebab`, `.Camel`, `.Plural`, `.Module`, plus `.Table`, `.Timestamp`, `.ID`, `.Func` for migrations and `.Resource`/`.API` for controllers. Unknown keys are errors.
- Flags may follow the name: `vukem make:model Post -m -c`.
- Generated Go files are passed through `go/format` before writing.
- Generators refuse to overwrite existing files without `--force`.
- Generated code passes this repository's own `.golangci.yml`: a stub whose body is a placeholder names its parameters `_` and says in the doc comment which name to give each one, the way `routes/schedule.go` does.
- A package a generator is the first to create gets its package comment in its own `doc.go`, never on the generated file: deleting that file is the first thing a real app does, and it would take `revive`'s `package-comments` with it. An installer shipping a `doc.go` for the same package keeps the one that is already there.
- `make:migration` picks the stub from the name: `create_<table>_table` creates the table, `add_x_to_<table>_table` / `alter_<table>_table` alter it, anything else is blank.

## Codemods

Registration happens by editing Go source, not by reflection or `init()`:

- `internal/codemod` parses the target file with `go/parser`, finds the slice literal returned by a known function (`bootstrap.Providers()`, `bootstrap.Commands()`, `migrations.All()`), inserts the element and imports at the AST positions, and runs `go/format`. Running it twice changes nothing.
- An added import lands in the `gci` group it belongs to — standard library, another module, then the project's own packages — because `go/format` sorts within a group but never moves an import between groups.
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

`vukem.CommandFunc` implements it from a name, a description, optional flags and a function. `vukem make:command SendReports` writes `app/commands/send_reports.go` and appends `commands.SendReports()` to `Commands()` in `bootstrap/commands.go`:

```go
// SendReports is the "send-reports" command: vukem send-reports.
func SendReports() vukem.Command {
	return vukem.CommandFunc{
		CommandName: "send-reports",
		Desc:        "Describe what send-reports does",
		Fn: func(ctx context.Context, app *vukem.App, args []string) error {
			fmt.Fprintln(app.Stdout(), "send-reports ran with", args)
			return nil
		},
	}
}
```

Rename `CommandName` to the `namespace:action` form (`reports:send`) when the command belongs to a group. Commands run with the `console` environment (providers limited to `web` are skipped), after `Boot`.

Commands print through `app.Stdout()` and `app.Stderr()` and read `app.Stdin()` instead of the `os` streams, so `vukem.WithStdout`, `WithStderr` and `WithStdin` (used by tests to capture or silence output) apply to them:

```go
fmt.Fprintf(app.Stdout(), "%d users promoted\n", n)
```

### `boost:mcp`

Built-in app command started by agents (`go run ./cmd/app boost:mcp`): a Model Context Protocol server over stdio (JSON-RPC 2.0, newline-delimited, stdlib only). Tools: `application_info`, `list_routes`, `list_commands`, `database_schema`, `database_query` (one read-only statement, max 100 rows), `get_absolute_url`, `search_docs` (fetches `llms-full.txt` from the docs site).

## Output

- Plain text without colour (so `NO_COLOR` has nothing to turn off) and no banners.
- `--json` for `route:list`, `migrate:status`, `about` and `queue:failed`.
