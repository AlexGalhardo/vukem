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

- `vukem.WithViews(fsys)` enables the `view` engine. The starter passes `resources.Views()`: files on disk during development, `embed.FS` in the binary. Views reload on every request outside production; syntax errors fail at boot.
- Directory layout: `layouts/`, `partials/` and `components/` are shared; every other file is a page named by its path without extension (`posts/index`).
- Layouts: a page defines blocks (`{{define "content"}}…{{end}}`) and renders inside `layouts/app.html`. Its first line can pick another layout, `{{/* layout: admin */}}`, or none, `{{/* layout: none */}}`. Each page gets its own template set, so block names never collide.
- **Fragments:** `c.Render("posts/index", data, web.Block("post-list"))` renders the whole page for normal requests and only the `post-list` block for htmx partial requests; `c.Fragment(name, block, data)` always renders the block.
- Funcs: `route`, `asset`, `csrfField`, `csrfToken`, `cspNonce`, `old`, `error`, `errors`, `flash`, `json` (safe in `<script>`), `dict` (data for partials), `htmxScript`, `can` (authorization), `vite`, `viteReactRefresh` (see [Vite](#vite)). Request-scoped funcs read the session and CSP nonce from the request context. Planned: `t` (BETA).

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

Invalid input from a classic form post → the validation hook flashes the errors and old input and redirects back (`303`); the form reads them with `{{error "title"}}` and `{{old "title"}}`. htmx requests get a **422** HTML fragment listing the errors, which htmx 4 swaps into the target. JSON requests get a `422` problem+json.

### SSE (BETA)

`c.SSE(func(s *sse.Stream) error { return s.Send("message", html) })` — `text/event-stream`, flushing via `http.ResponseController`, heartbeat, stops on client disconnect or server shutdown. Pairs with htmx `hx-sse`.

## Vite

Every starter ships Vite 8 + Tailwind CSS v4 (`@tailwindcss/vite`) preconfigured (decision F2): `package.json`, `vite.config.ts`, `resources/css/app.css` (`@import "tailwindcss"` plus the starter styles in `@layer base`), `resources/js/app.ts` and the hot-file plugin `resources/vite/vukem.js`, which builds into `resources/public/build` (embedded with `//go:embed all:public`). `vukem new` runs `npm install` when npm is available (`--no-install` skips it); `vukem build` refuses to run without `node_modules`. Outside production the CSP allows the running dev server (`SecureHeadersConfig.DevServer`), and the layout carries `<meta property="csp-nonce">` so Vite's injected styles get the nonce. Without a dev server or a build, `{{vite}}` renders an HTML comment in development (tests need no Node) and fails in production.

- `vukem.WithVite(os.DirFS("public"))` (or an `embed.FS`) enables the `vite` package (`vite.New(vite.Config{FS, Dev})`) behind the view funcs. `Dev` is on outside production.
- A tiny Vite plugin shipped with the starter (`resources/vite/vukem.js`) writes `public/hot` with the dev server URL on start and deletes it on exit.
- `{{ vite "resources/css/app.css" "resources/js/app.ts" }}`:
  - `public/hot` present and `Dev` on → `<script type="module" src="{hot}/@vite/client">` + each entry (CSS entries as `<link rel="stylesheet">`); `{{ viteReactRefresh }}` emits the React Refresh preamble (empty otherwise). The hot file must hold an `http(s)` URL; it is ignored in production.
  - otherwise → reads `public/build/.vite/manifest.json` (cached in production, re-read in development) and emits `<link rel="modulepreload">` for recursive static imports, `<link rel="stylesheet">` for entry and import CSS, then `<script type="module">`, each file once across entries.
- `{{ asset "resources/img/logo.png" }}` resolves through the dev server or the manifest when Vite knows the file, else falls back to the public path.
- Every tag carries the request's CSP nonce; manifest `integrity` values (e.g. from `vite-plugin-manifest-sri`) become SRI attributes.
- `/build/*` served with immutable cache headers.
- `vukem serve` starts the Go app with reload and, if `vite.config.*` exists, `npm run dev` alongside.

## Inertia v3 (BETA)

Own adapter in `inertia/` with React, Vue and Svelte starters (B.3.6).

```go
in, err := inertia.New(inertia.Config{Version: viteInstance.Version}) // RootView "app", RootID "app"
r.Use(in.Middleware) // Vary, version check, 302→303, fragment redirects

// Root view (resources/views/app.html): {{.Head}} in <head>, {{.Inertia}} in <body>.
```

`inertia.Share(c.Context(), key, value)` adds per-request shared props (`Config.Shared` holds global ones); `inertia.Location(c, url)` forces a full visit; `inertia.PreserveFragment(c)` before a redirect keeps the URL fragment. Prop values of type `func(context.Context) (any, error)` resolve lazily.

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
| Props | `Optional(v)`, `Always(v)`, `Defer(v).Group(g).Rescue()`, `Merge(v)` with `.Append(paths...)`/`.Prepend(paths...)`/`.MatchOn(fields...)`, `DeepMerge(v)`, `Once(v).As(key).Fresh().Until(t)`, `Scroll(v, meta)` / `ScrollPage(orm.Page)` / `ScrollCursor(orm.CursorPage, name)`; modifiers chain (`Defer(v).Merge()`, `Merge(v).Once()`). Values may be `func(ctx) (any, error)`; nested `map[string]any`/`Props` resolve recursively, top-level `"a.b"` keys unpack, and partial reloads filter by dotted path (`only`, `except`, `reset`, except-once) |
| Page object | `component`, `props` (+`errors`), `url`, `version`, `encryptHistory`, `clearHistory`, `preserveFragment`, `mergeProps`, `prependProps`, `deepMergeProps`, `matchPropsOn`, `scrollProps`, `deferredProps`, `rescuedProps`, `sharedProps`, `onceProps`, `flash` |
| Redirects | 302 → 303 for PUT/PATCH/DELETE; `inertia.Location(url)` (409 external); fragment redirects via `X-Inertia-Redirect`; `Back` |
| History & flash | `Config.EncryptHistory` (opt-in, needs a secure context) or `inertia.EncryptHistory(ctx, bool)` per page; `inertia.ClearHistory(c)` on logout; `inertia.Flash(c, key, value)` fills `page.flash` once (survives redirects; prefetch requests do not consume it) |
| Precognition | `r.Post("/users", inertia.Precognitive(schema, h))`: `Precognition: true` requests only validate (204 + `Precognition-Success`, or 422 `{message, errors}` filtered by `Precognition-Validate-Only`), `Vary: Precognition` always |
| Shared data | `inertia.Share(key, fn)` globally or per request (middleware); auth user + flash by default |
| Validation | errors flashed + `303` back; surfaced as `props.errors` |
| Version | hash of the Vite manifest |
| SSR | `Config.SSR = &inertia.SSRConfig{HotURL: vite.HotURL, Bundle: "bootstrap/ssr/ssr.js"}`: POST to the Node SSR server (`/render`, default `127.0.0.1:13714`, 3s timeout); **falls back to client-side rendering** on any failure (reported to `OnFailure`, default a warning log); dev uses the Vite plugin endpoint (`{hot}/__inertia_ssr`); `Except` path prefixes and `inertia.DisableSSR(ctx)` per request; `vukem ssr:start` runs `node bootstrap/ssr/ssr.js` and restarts it when it crashes, `vukem ssr:check` probes `/health` |
| Testing | `app.Client(t).Inertia("").Get("/posts").AssertInertia("Posts/Index").HasProp("posts", 3).MissingProp("stats").PropEquals("filters.q", "go")` — works on first visits (HTML) and Inertia visits (JSON); `Inertia(version)` also sends the Inertia `Accept` header so `PostJSON` behaves like a form submission |

### Starters

`vukem new app --inertia react|vue|svelte` (or the stack prompt) and `vukem install:inertia --force [--react|--vue|--svelte]` on an HTMX starter build an Inertia app: `vukem.WithInertia(inertia.Config{})` in `bootstrap/app.go`, `r.Use(app.Inertia.Middleware, shareProps(app))` in `routes/web.go`, controllers that call `inertia.Render`, tests with `AssertInertia`, the root view `resources/views/app.html` (CSP nonce meta, `{{vite}}`, `{{.Head}}`, `{{.Inertia}}`), and TypeScript pages in `resources/js/pages` with a default layout in `resources/js/layouts`, on Vite + Tailwind. `npm run typecheck` type-checks them. `vukem install:auth` on an Inertia app adds `Auth/*` and `Dashboard` pages instead of HTMX views, shares `auth.user`, clears history on logout and throttles logins by the email of JSON submissions. SSR is off by default; enable it with `inertia.Config{SSR: &inertia.SSRConfig{Bundle: "bootstrap/ssr/ssr.js"}}`.

Typed routes for the frontend (Wayfinder/Tuyau-style): `vukem codegen:routes` builds the app in warmup mode and writes `resources/js/routes.ts` (`-o file` elsewhere, `-o -` to stdout). Warmup (`vukem.KindWarmup`) loads config and runs providers' `Register` and the route definitions, but never `Boot` or `Shutdown`, so no database or other connection is opened; register routes in `Register`/`WithRoutes`, not in `Boot`, and do not filter out `KindWarmup` in a provider that registers routes. The file holds only named routes, sorted by name:

```ts
import { route, routes, type RouteName } from "./routes";

route("posts.show", { id: 7 });                     // "/posts/7"
route("files.show", { path: "a b/c" }, { page: 2 }); // "/files/a%20b/c?page=2"
route("home");                                      // routes without params take no object
routes["posts.store"].method;                       // "POST"
```

Params are required and typed per route (`string | number`), encoded with `encodeURIComponent` (each segment of a `{name...}` wildcard, keeping `/`); the optional second object becomes the query string (`null`/`undefined` values skipped). Regenerate after changing routes and commit the file.
