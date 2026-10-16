---
name: vukem-cli
description: The vukem CLI, generators and app commands. Use when creating files, running migrations, queues or the scheduler, or adding custom commands.
---

# Vukem CLI

- `vukem list` shows every command; the `list_commands` MCP tool shows app commands.
- Generators (`make:controller`, `make:model`, `make:migration`, `make:request`, `make:middleware`, `make:policy`, `make:job`, `make:event`, `make:listener`, `make:mail`, `make:factory`, `make:seeder`, `make:command`, `make:provider`, `make:resource`, `make:test`) write code you own and register it in `bootstrap/` or the `registry.go` files. Prefer them over hand-written boilerplate.
- Installers: `install:auth` (sessions, password reset, `--social`), `install:api` (tokens, `/api/v1`).
- App commands run inside the app binary: `migrate`, `migrate:rollback`, `db:seed`, `queue:work`, `schedule:work`, `route:list`, `about`, `openapi:export`, `codegen:types`, `codegen:routes`, `codegen:client` (typed TS or Go API client).
- Custom commands: `vukem make:command reports:send`, then implement `Run(ctx, app, args)`.
- Destructive commands (`migrate:fresh`, `db:wipe`) need `--force` in production; never run them against production data without asking.
