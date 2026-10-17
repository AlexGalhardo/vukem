## Vukem (Go web framework)

This project is a [Vukem](https://vukem.dev) application: A Model-View-Controller (MVC) FullStack web framework built on `net/http` and the Go standard library.

### Tools

- The `vukem-boost` MCP server (`go run ./cmd/app boost:mcp`) exposes `application_info`, `list_routes`, `list_commands`, `database_schema`, `database_query` (read-only), `get_absolute_url` and `search_docs`. Use them instead of guessing routes, tables or framework APIs.
- Skills in `.claude/skills/vukem-*` cover HTTP, validation, database, testing, frontend and the CLI.
- Full docs in one file: <https://vukem.dev/llms-full.txt>.

### Conventions

- Generate code with the CLI (`vukem make:controller`, `make:model -m -f`, `make:migration`, `make:request`, `make:test`, …); generators register what they create in `bootstrap/*.go` and the `registry.go` files. Never hand-write registrations a generator owns.
- Handlers are `func(c *web.Ctx) error`; middleware is `func(http.Handler) http.Handler`. Return errors, do not write error responses by hand: the error handler renders problem+json, htmx fragments or HTML pages.
- Validate every input with `z` schemas (`web.Parse(c, Schema)`); invalid input becomes a 422 or a redirect back with flashed errors.
- Database access goes through `orm.Query[T](db)` and `orm.Create/Update/Delete`; schema changes go through migrations, never ad-hoc SQL.
- No globals, no `init()` registration, no reflection-based DI; `context.Context` is the first parameter of anything doing I/O.
- Secure by default: keep CSRF, security headers and CSP nonces on; never wrap user input in `template.HTML`.

### Commands

- `vukem serve`: run with reload (and Vite when present).
- `go test ./...`: tests use `vukemtest` (fluent HTTP client, fakes, database assertions).
- `vukem migrate`, `vukem route:list`, `vukem about`: app commands delegated to `go run ./cmd/app`.
