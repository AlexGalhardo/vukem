# Frontend

Views, HTMX v4 (default), Inertia v3 (optional, React), and Vite. Decisions `F1`–`F5` in [brainstorming.md](brainstorming.md).

## Views

```go
type View interface {
	Render(ctx context.Context, w io.Writer) error // satisfied by templ.Component
}

type Engine interface {
	Render(w io.Writer, name string, data any) error
	Fragment(w io.Writer, name, block string, data any) error
}
```

### `html/template` engine (default)

- Templates live in `resources/views/` and are loaded from an `fs.FS` (disk in development with reload on change, `embed.FS` in production builds).
- Layouts: a page declares `{{ define "content" }}` and names its layout in a header comment or via `c.Render("posts/index", data, view.Layout("app"))`. Each page is parsed together with its layout and `partials/*` into its own template set (avoids name collisions).
- **Fragments:** `c.Fragment("posts/index", "post-list", data)` executes only the named `{{ block }}`. The HTMX helper picks page vs fragment automatically.
- Built-in funcs: `route`, `asset`, `vite`, `csrfField`, `old`, `error`, `errors`, `flash`, `can`, `auth`, `json`, `dict`, `htmxScript`, `t` (i18n, BETA).
- Every render receives shared data (`.Flash`, `.Errors`, `.Old`, `.User`, `.CSRF`) merged with handler data.

### templ (supported, not bundled)

```go
return c.View(components.PostList(posts)) // templ.Component satisfies view.View
```

No templ import in the core; templ users add the dependency and `go tool templ generate`.

## HTMX

htmx **4.x** vendored at `public/js/htmx.min.js` by the starter and served by the static middleware — no Node required. The starter layout includes:

```html
<body hx-headers:inherited='{"X-CSRF-Token": "{{ .CSRF }}"}'>
<script src="{{ asset "js/htmx.min.js" }}" defer></script>
```

(The token header is only needed when the token CSRF fallback is enabled; `CrossOriginProtection` covers browsers by default.)

### Request helpers

| Helper | Meaning |
| --- | --- |
| `c.HTMX().Is()` | `HX-Request: true` |
| `c.HTMX().IsPartial()` | `HX-Request-Type: partial` — render a fragment |
| `c.HTMX().IsBoosted()` | `HX-Boosted` |
| `c.HTMX().IsHistoryRestore()` | `HX-History-Restore-Request` — must render full page |
| `c.HTMX().CurrentURL()`, `Source()`, `Target()` | parsed `tag#id` values |

### Rendering

```go
func (p *PostController) Index(c *web.Ctx) error {
	posts, err := orm.Query[models.Post](p.db).Latest().Get(c.Context())
	if err != nil { return err }
	return c.Render("posts/index", web.Map{"Posts": posts}, htmx.Block("post-list"))
}
```

`htmx.Block` makes the render return only that block for partial htmx requests and the full page otherwise. `Vary: HX-Request` is always set.

### Response helpers

```go
c.HTMX().Trigger("post-created", web.Map{"id": p.ID}).
	PushURL(c.URLFor("posts.show", "id", p.ID)).
	Retarget("#flash").Reswap("innerHTML")
```

Covers `HX-Trigger`, `HX-Location` (string or object), `HX-Redirect`, `HX-Refresh`, `HX-Push-Url`, `HX-Replace-Url`, `HX-Retarget`, `HX-Reswap`, `HX-Reselect`. `c.Redirect` automatically uses `HX-Location` for htmx requests and `303` otherwise.

### Multiple targets

`htmx.Partial(target, swap, view)` renders `<hx-partial hx-target="…" hx-swap="…">…</hx-partial>`; helpers for `hx-swap-oob` as well.

### Validation flow

Invalid form submitted via htmx → error handler re-renders the form block with **422** (htmx 4 swaps 4xx by default), errors and old input populated.

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
