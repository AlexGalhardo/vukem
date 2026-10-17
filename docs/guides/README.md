# Starter guides

Step-by-step guides for each project starter, from `vukem new` to a deployed binary. They show the workflow; the design docs in [docs/](../README.md) own the details.

| Starter | Create with | Guide |
| --- | --- | --- |
| HTMX + Tailwind + SQLite (default) | `vukem new app` | [htmx.md](htmx.md) |
| HTMX + templ components | `vukem new app --templ` | [templ.md](templ.md) |
| Inertia with React, Vue or Svelte | `vukem new app --inertia react` (or `vue`, `svelte`) | [inertia.md](inertia.md) |
| JSON API only | `vukem new app --api` | [api.md](api.md) |

All starters need Go 1.27 and the global CLI:

```sh
go install github.com/AlexGalhardo/vukem/cmd/vukem@latest
vukem version
```

The frontend starters (HTMX, templ, Inertia) also need Node.js for Vite and Tailwind; `vukem new` runs `npm install` unless you pass `--no-install`. Without stack flags on a terminal, `vukem new app` asks which stack to use.

`vukem list` prints every command. Commands the global CLI does not own (`migrate`, `route:list`, `db:seed`, `queue:work`, your own) run in the project's app binary ([cli.md](../cli.md#two-binaries)).
