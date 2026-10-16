# Frontend

Views, HTMX v4 (default), Inertia v3 (optional, React), and Vite. Decisions `F1`–`F5` in [brainstorming.md](brainstorming.md).

## Views

```go
// web.Renderer is implemented by view.Engine (html/template).
type Renderer interface {
	Render(ctx context.Context, w io.Writer, name string, data any) error
	RenderBlock(ctx context.Context, w io.Writer, name, block string, data any) error
	Has(name string) bool
}

// web.View is satisfied by templ.Component.
type View interface {
	Render(ctx context.Context, w io.Writer) error
}
```

### `html/template` engine (default)

- `lumek.WithViews(fsys)` enables the `view` engine. The starter passes `resources.Views()`: files on disk during development, `embed.FS` in the binary. Views reload on every request outside production; syntax errors fail at boot.
- Directory layout: `layouts/`, `partials/` and `components/` are shared; every other file is a page named by its path without extension (`posts/index`).
- Layouts: a page defines blocks (`{{define "content"}}…{{end}}`) and renders inside `layouts/app.html`. Its first line can pick another layout, `{{/* layout: admin */}}`, or none, `{{/* layout: none */}}`. Each page gets its own template set, so block names never collide.
- **Fragments:** `c.Render("posts/index", data, web.Block("post-list"))` renders the whole page for normal requests and only the `post-list` block for htmx partial requests; `c.Fragment(name, block, data)` always renders the block.
- Funcs: `route`, `asset`, `csrfField`, `csrfToken`, `cspNonce`, `old`, `error`, `errors`, `flash`, `json` (safe in `<script>`), `dict` (data for partials), `htmxScript`. Request-scoped funcs read the session and CSP nonce from the request context. Planned: `vite` (BETA), `can`/`auth` (ALPHA), `t` (BETA).

### templ (supported, not bundled)

```go
return c.View(components.PostList(posts)) // templ.Component satisfies view.View
```

No templ import in the core; templ users add the dependency and `go tool templ generate`.

## HTMX

The starter vendors **htmx 4.0.0** at `resources/public/js/htmx.min.js`, served by the static middleware, so no Node or CDN is needed. Its SHA-256 is pinned in `internal/cli/new.go` and checked by a test: upgrading means replacing the file and the hash together. The layout loads it with the request's CSP nonce:

```html
{{htmxScript (asset "js/htmx.min.js")}}
<!-- <script src="/js/htmx.min.js" defer nonce="…"></script> -->
```

Starter views use no inline scripts, `hx-on`, event-handler or `style` attributes, so they run under the default CSP (`TestSecurity_A02_StarterViewsRespectCSP`). CSRF needs nothing extra for htmx: `CrossOriginProtection` checks `Sec-Fetch-Site`, and plain forms include `{{csrfField}}`.

### Request helpers

| Helper | Meaning |
| --- | --- |
| `c.IsHTMX()`, `htmx.Is(r)` | `HX-Request: true` |
| `c.IsPartial()`, `htmx.IsPartial(r)` | htmx request that is not `HX-Request-Type: full`; render a fragment |
| `htmx.IsBoosted(r)` | `HX-Boosted` |
| `htmx.IsHistoryRestore(r)` | `HX-History-Restore-Request`; must render the full page |
| `htmx.CurrentURL(r)`, `htmx.Source(r)`, `htmx.Target(r)` | parsed `tag#id` values |

### Rendering

```go
func (p *PostController) Index(c *web.Ctx) error {
	posts, err := orm.QueryOf[models.Post](p.db).Latest().Get(c.Context())
	if err != nil {
		return err
	}

	return c.Render("posts/index", map[string]any{"Posts": posts}, web.Block("post-list"))
}
```

`web.Block` makes the render return only that block for partial htmx requests and the full page otherwise. `Vary: HX-Request` is always set.

### Response helpers

```go
c.HTMX().Trigger("post-created", map[string]any{"id": p.ID}).
	PushURL("/posts/" + id).
	Retarget("#flash").Reswap("innerHTML")
```

Covers `HX-Trigger`, `HX-Location` (string or object), `HX-Redirect`, `HX-Refresh`, `HX-Push-Url`, `HX-Replace-Url`, `HX-Retarget`, `HX-Reswap`, `HX-Reselect`. `c.Redirect` automatically uses `HX-Location` for htmx requests and `303` otherwise.

### Multiple targets

`htmx.Partial(target, swap, view)` renders `<hx-partial hx-target="…" hx-swap="…">…</hx-partial>`; helpers for `hx-swap-oob` as well.

### Validation flow

Invalid form input → the validation hook flashes the errors and old input and redirects back (`303`, or `HX-Location` for htmx). The form reads them with `{{error "title"}}` and `{{old "title"}}`. JSON requests get a `422` problem+json instead.

### SSE (BETA)

`c.SSE(func(s *sse.Stream) error { return s.Send("message", html) })` — `text/event-stream`, flushing via `http.ResponseController`, heartbeat, stops on client disconnect or server shutdown. Pairs with htmx `hx-sse`.

## Vite

Optional for the HTMX starter (Tailwind, bundling), required for Inertia.

- A tiny Vite plugin shipped with the starter (`resources/vite/lumek.js`) writes `public/hot` with the dev server URL on start and deletes it on exit.
- `{{ vite "resources/js/app.ts" }}`:
  - `public/hot` present → `<script type="module" src="{hot}/@vite/client">` + entry (+ React Refresh preamble via `{{ viteReactRefresh }}`).
  - otherwise → reads `public/build/.vite/manifest.json` and emits `<script type="module">`, `<link rel="stylesheet">` and `<link rel="modulepreload">` for the entry and its recursive imports (deduplicated across entries).
- `{{ asset "img/logo.png" }}` resolves through the manifest when present.
- CSP nonce and SRI (`integrity`) attributes supported.
- `/build/*` served with immutable cache headers.
- `lumek serve` starts the Go app with reload and, if `vite.config.*` exists, `npm run dev` alongside.

## Inertia v3 (BETA)

Own adapter in `inertia/`, React starter only before 1.0.

```go
return inertia.Render(c, "Posts/Index", inertia.Props{
	"posts":   posts,
	"filters": inertia.Optional(func(ctx context.Context) (any, error) { return loadFilters(ctx) }),
	"stats":   inertia.Defer(statsFn).Group("sidebar"),
})
```

| Area | Support |
| --- | --- |
| Initial page | `<script data-page="app" type="application/json">` with `/` escaped |
| Requests | `X-Inertia`, version check (409 + `X-Inertia-Location` on GET mismatch), partial reloads (`Partial-Component/Data/Except`), `X-Inertia-Reset`, error bags, once-props exclusion, `Purpose: prefetch`, Precognition |
| Props | `Optional`, `Always`, `Defer(group, rescue)`, `Merge`, `DeepMerge`, `Prepend`, `MatchOn`, `Once(key, expiresAt)`, `Scroll(page)` — resolved recursively with dotted paths |
| Page object | `component`, `props` (+`errors`), `url`, `version`, `encryptHistory`, `clearHistory`, `preserveFragment`, `mergeProps`, `prependProps`, `deepMergeProps`, `matchPropsOn`, `scrollProps`, `deferredProps`, `rescuedProps`, `sharedProps`, `onceProps`, `flash` |
| Redirects | 302 → 303 for PUT/PATCH/DELETE; `inertia.Location(url)` (409 external); fragment redirects via `X-Inertia-Redirect`; `Back` |
| Shared data | `inertia.Share(key, fn)` globally or per request (middleware); auth user + flash by default |
| Validation | errors flashed + `303` back; surfaced as `props.errors` |
| Version | hash of the Vite manifest |
| SSR | POST to the Node SSR server (`/render`) with timeout; **falls back to client-side rendering** on any failure; dev uses the Vite plugin endpoint; per-route disable; `lumek ssr:start` supervises `node bootstrap/ssr/ssr.js` |
| Testing | `res.AssertInertia(t, "Posts/Index").HasProp("posts", 3).MissingProp("stats")` |

Typed routes for the frontend (Wayfinder/Tuyau-style): `lumek codegen:routes` boots the app in warmup mode (no connections) and emits `resources/js/routes.ts` with typed `route('posts.show', { id })` (BETA).
