---
name: vukem-frontend
description: Views (html/template), htmx, Vite, Tailwind and Inertia in a Vukem app. Use when building pages, partials, forms, assets or Inertia components.
---

# Vukem frontend

- Views live in `resources/views`: `layouts/`, `partials/`, `components/` are shared; pages define `{{define "content"}}`. Render with `c.Render("posts/index", data, web.Block("post-list"))` so htmx partial requests get only the block.
- Template funcs: `route`, `asset`, `csrfField`, `cspNonce`, `old`, `error`, `flash`, `json`, `dict`, `can`, `vite`, `viteReactRefresh`.
- Every form posts `{{csrfField}}`. No inline scripts, `style` or event-handler attributes: the CSP is strict; scripts get `nonce="{{cspNonce}}"` (the `vite` func adds it).
- htmx: `c.IsHTMX()`, `c.HTMX().Trigger(...)`, redirects become `HX-Location` automatically.
- Vite: `{{vite "resources/css/app.css" "resources/js/app.ts"}}` in the layout; `vukem serve` runs the dev server; `vukem build` builds assets and the binary.
- Inertia: `inertia.Render(c, "Posts/Index", inertia.Props{...})`; shared data with `inertia.Share(ctx, key, value)`; external redirects with `inertia.Location`.
