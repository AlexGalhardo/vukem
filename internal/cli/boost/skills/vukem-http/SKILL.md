---
name: vukem-http
description: Routes, controllers, middleware, responses and errors in a Vukem app. Use when adding or changing endpoints, handlers, route groups, middleware or redirects.
---

# Vukem HTTP

- Routes live in `routes/*.go` and are registered from `routes/registry.go`. Name every route (`.Name("posts.show")`) and build URLs with `c.URL(name, params...)` / `c.RedirectRoute`, never hard-coded paths.
- Handlers: `func(c *web.Ctx) error`. Read input with `c.Param`, `c.Query`, `web.Parse(c, schema)`; respond with `c.JSON`, `c.Render`, `c.Redirect`, `c.Back`, `c.NoContent`.
- Controllers: `vukem make:controller Post` (resource methods `Index/Show/Create/Store/Edit/Update/Destroy`).
- Middleware: `func(http.Handler) http.Handler`; generate with `vukem make:middleware`, attach with `r.Use(...)` or on a group.
- Errors: return them. `errs.NotFound`, `errs.Forbidden`, `orm.ErrNoRows` (404) and validation errors map to the right status and format (problem+json for JSON clients, fragments for htmx, pages for browsers). 5xx details only show with `APP_DEBUG=true`.
- Authorization: `c.Authorize("update", post)` with policies from `vukem make:policy`.
- Check the live route table with the `list_routes` MCP tool before adding routes.
