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
- Funcs: `route`, `asset`, `csrfField`, `csrfToken`, `cspNonce`, `old`, `error`, `errors`, `flash`, `json` (safe in `<script>`), `dict` (data for partials), `htmxScript`, `can` (authorization), `vite`, `viteReactRefresh` (see [Vite](#vite)), `t` and `locale` (see [i18n](subsystems.md#i18n)). Request-scoped funcs read the session, CSP nonce and locale from the request context.

### templ (optional)

```go
return c.View(components.PostList(posts)) // templ.Component satisfies view.View
```

No templ import in the core; templ users add the dependency and `go tool templ generate`.

Context helpers for components (they only get the request `ctx`), all in the core and templ-free:

| Helper | Meaning |
| --- | --- |
| `c.View(page, web.Partial(fragment))` | `web.Block` for components: render `fragment` for htmx partial requests, `page` otherwise; sets `Vary: HX-Request` |
| `web.FlashFrom(ctx, key)`, `web.OldInput(ctx, field)`, `web.ErrorsFrom(ctx)` | `c.FlashValue`, `c.Old`, `c.Errors` from the context |
| `csrf.Token(ctx)`, `csrf.FieldName`, `middleware.CSPNonce(ctx)` | CSRF field and CSP nonce |
| `web.ServicesFrom(ctx).URLs.URL(name, params...)` | Named route URLs |
| `vite.FromContext(ctx).Tags(nonce, entries...)` | Vite tags; `r.Use(app.Vite.Middleware())` puts `app.Vite` in the context |

### templ starter

`vukem new app --templ` is the HTMX starter with the same pages and tests (layout, home, posts CRUD with the htmx search fragment, flash messages, validation errors and old input, CSRF field, CSP nonces, Vite tags) written as templ components in `resources/views` (package `views`) instead of `html/template` views:

- templ is pinned as a Go 1.24+ tool (`go get -tool github.com/a-h/templ/cmd/templ@v0.3.1070`, a `tool` line in `go.mod`); run it with `go tool templ generate`. The version lives in `internal/cli/templ.go`.
- The generated `*_templ.go` files are committed, so `go build` and `go test` work without running templ; regenerating an untouched project produces no diff (the e2e test checks it).
- `vukem serve` runs `go tool templ generate` before every build when the project has `.templ` files, and watches them; `vukem build` generates before `vite build`.
- `resources/views/helpers.go` wraps the context helpers above (`route`, `asset`, `viteTags`, `script`, `flash`, `old`, `fieldError`, `csrfField`). Scripts go through `script`/`viteTags`, which add the nonce, so the components contain no `<script>` tags; `TestSecurity_A02_StarterViewsRespectCSP` checks `.templ` files like `.html` views.
- `bootstrap/app.go` has no `vukem.WithViews`; `install:auth` still writes `html/template` pages and refuses templ projects for now.

## HTMX

The starter vendors **htmx 4.0.0** at `resources/public/js/htmx.min.js`, served by the static middleware, so no Node or CDN is needed. Its SHA-256 is pinned in `internal/cli/new.go` and checked by a test: upgrading means replacing the file and the hash together. The layout loads it with the request's CSP nonce:

```html
{{htmxScript (asset "js/htmx.min.js")}}
{{vite "resources/css/app.css" "resources/js/app.ts"}}
<!-- <script src="/js/htmx.min.js" defer nonce="…"></script> -->
```

Keep `htmxScript` before `vite`: the htmx tag is `defer` and the Vite entry is a module script, and both run in document order, so this order makes `window.htmx` defined when `resources/js/app.ts` runs. The starter checks it in `tests/home_test.go` (`TestLayoutLoadsHTMXBeforeVite`).

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

### SSE

`c.SSE(func(s *sse.Stream) error { return s.Send("message", html) })` (or `sse.Serve(w, r, fn)` in plain handlers) streams `text/event-stream`: every event is flushed through `http.ResponseController`, the server's `WriteTimeout` is lifted for the stream, a `: ping` comment goes out every 15s (`sse.Heartbeat(d)`), and `Stream.Context()` ends on client disconnect, when `fn` returns, or when the app shuts down (the HTTP server signals open streams so shutdown never waits on them). `s.SendJSON`, `s.SendEvent(sse.Event{ID, Event, Data, Retry})` and `s.LastEventID()` cover reconnects; field values lose CR/LF so input cannot forge fields.

`sse.NewHub(broker)` fans events out by topic: `hub.Publish(ctx, "posts", ev)` and `hub.Stream(s, "posts")`. With a nil broker it works within one process; pass the `drivers/redis` client to reach every instance. Slow subscribers that fall 64 events behind are dropped (the browser reconnects). Pairs with htmx `hx-sse`.

### WebSockets

`go get github.com/AlexGalhardo/vukem/drivers/websocket` (on `github.com/coder/websocket` v1.8). Core cannot import a driver, so the planned `c.WebSocket(fn)` is a function taking the context:

```go
r.Get("/chat", func(c *web.Ctx) error {
	return websocket.Handle(c, func(conn *websocket.Conn) error {
		for {
			var msg Message
			if err := conn.ReadJSON(conn.Context(), &msg); err != nil {
				return nil // client left or the app is shutting down
			}
			if err := conn.WriteJSON(conn.Context(), reply(msg)); err != nil {
				return err
			}
		}
	})
})
r.Get("/feed", websocket.Handler(feed, websocket.AllowedOrigins("app.example.com")))
```

- `websocket.Handle(c, fn, opts...)`, `websocket.Handler(fn, opts...)` (a `web.HandlerFunc`) and `websocket.Serve(w, r, fn, opts...)` for plain handlers. A refused upgrade returns an HTTP error whose status was already sent.
- `Conn`: `ReadText`, `ReadJSON`, `WriteText`, `WriteJSON` (each takes a context), `Close(code, reason)`, `CloseRead(ctx)` for write-only handlers, `Subprotocol()`, `Unwrap()` for binary or streaming I/O. `Conn.Context()` ends when the client goes away, the peer stops answering pings, `fn` returns or the app shuts down. One reader and one writer may run at the same time.
- Origin: same-origin only by default; `AllowedOrigins(patterns...)` (`path.Match` on the host, or on `scheme://host` when the pattern has `://`) opens specific origins. `"*"` or a malformed pattern refuses every upgrade; `InsecureSkipOriginCheck()` is the only way to accept any origin. Requests without an `Origin` header (non-browser clients) are accepted.
- Limits: `ReadLimit(n)` (default 32 KiB; larger messages close with 1009), `WriteTimeout(d)` per message (default 10s), `PingInterval(d)` / `PingTimeout(d)` (30s / 10s; zero interval disables). Pongs only arrive while something reads, so handlers that only write call `conn.CloseRead(ctx)`.
- Closing: `fn` returning nil closes with 1000, an error with 1011. When the app shuts down, the same signal that ends SSE streams (`sse.ShutdownSignal`) closes every socket with 1001 "going away", so shutdown never waits on open sockets. The server's read/write timeouts are lifted for the socket, which enforces its own.
- `Subprotocols(...)` negotiates subprotocols. For channels with authorization and fan-out across instances, use `websocket.Broadcast` (below).

### Broadcasting

Package `broadcast` (core, stdlib only) pushes server events to browsers over named channels, like Laravel Broadcasting. Channels are public (`posts`), private (`private-user.42`) or presence (`presence-room.7`), chosen by the name prefix. Every channel kind needs a rule; decisions **fail closed**: no matching rule, a rule error, a missing `Config.User` or no user all deny.

```go
b := broadcast.New(broadcast.Config{
	Broker:     redisClient, // sse.Broker; nil = this process only
	User:       func(ctx context.Context) (any, error) { return auth.UserFrom[*models.User](ctx, "web") },
	Authorizer: gate, // for PrivateGate (web.Authorizer, e.g. *authz.Gate)
})
defer b.Close()

err := b.Public("posts")                     // undeclared public channels are denied
err = b.Private("user.{id}", func(ctx context.Context, u any, p map[string]string) (bool, error) {
	return u.(*models.User).ID == p["id"], nil
})
err = b.PrivateGate("post.{id}", "view", func(ctx context.Context, p map[string]string) (any, error) {
	return models.Posts.Find(ctx, db, p["id"]) // c.Authorize("view", post) semantics
})
err = b.Presence("room.{id}", func(ctx context.Context, u any, p map[string]string) (broadcast.Member, bool, error) {
	user := u.(*models.User)
	return broadcast.Member{ID: strconv.FormatInt(user.ID, 10), Info: map[string]string{"name": user.Name}}, true, nil
})
```

- Patterns omit the prefix and split on `.`; `{param}` matches one segment. Invalid patterns are a boot error (the registration returns it). Names are 1–128 characters of `A-Z a-z 0-9 _ - = @ . :`; anything else is a 400, so commas, spaces, quotes and CR/LF never reach a list, a topic or a frame.
- Events implement `Channels() []string`, optionally `BroadcastAs() string` (default: the Go type name) and `BroadcastWith() any` (default: the event as JSON). `b.Broadcast(ctx, event)` publishes on each channel through the hub (memory or `Broker`), so every instance delivers it; `b.Publish(ctx, channel, name, data)` skips the type. Event names starting with `presence:` are reserved.
- From the events bus: `bus.Register(broadcast.Listener[PostPublished](b))` (sync; `ListenerAsync` in the background) or `broadcast.Queued[PostPublished](b, "broadcast.post_published")` to broadcast from a queue job with retries (register `bus.Jobs()` on the queue; the event must round-trip through JSON). Any job can call `b.Broadcast(ctx, ev)` directly.
- `b.Subscribe(ctx, channels...)` authorizes every channel (all or nothing, at most `MaxChannels`, default 32) and returns a `*Subscription` (`Messages()`, `Members(channel)`, `Close()`); transports are built on it.

Transports:

- **SSE (core):** `r.Get("/broadcast/sse", b.SSE)` (or `b.SSEHandler()` for a plain mux) serves `GET /broadcast/sse?channels=posts,private-user.42`. Every channel is authorized before streaming; a refusal answers 400/403 and no stream starts. Each event is an SSE event named after the broadcast event whose data is `{"channel","event","data"}`; presence channels first get `presence:here` with the member list, then `presence:joining` / `presence:leaving`.
- **WebSocket (`drivers/websocket`):** `r.Get("/broadcast/ws", websocket.Broadcast(b, opts...))`, same origin checks as `websocket.Handler`. Each subscribe is authorized in the context of the upgrade request, so mount both transports behind the auth middleware.

WebSocket protocol (JSON text messages):

| Direction | Message |
| --- | --- |
| client → server | `{"type":"subscribe","channel":"private-user.42"}`, `{"type":"unsubscribe","channel":"…"}` |
| server → client | `{"type":"subscribed","channel":"…"}`, `{"type":"unsubscribed","channel":"…"}` |
| server → client | `{"type":"event","channel":"…","event":"PostPublished","data":{…}}` |
| server → client | `{"type":"here","channel":"presence-…","data":[{"id":"1","info":{…}}]}`, `{"type":"joining","channel":"…","data":{"id":"2"}}`, `{"type":"leaving",…}` |
| server → client | `{"type":"error","channel":"…","status":403,"message":"This channel is unauthorized."}` (400 for invalid names, unknown types, too many channels; an invalid name is not echoed) |

A subscription the server drops (64 events behind, or `b.Close()`) ends the SSE stream or closes the socket with 1001; the client reconnects and subscribes again.

```js
const ws = new WebSocket(`wss://${location.host}/broadcast/ws`)
ws.onopen = () => ws.send(JSON.stringify({ type: 'subscribe', channel: 'private-user.42' }))
ws.onmessage = (e) => { const m = JSON.parse(e.data); if (m.type === 'event') console.log(m.event, m.data) }
// SSE: new EventSource('/broadcast/sse?channels=posts').addEventListener('PostPublished', (e) => JSON.parse(e.data))
```

GraphQL subscriptions can be fed by broadcast channels (`graphql.FromBroadcast`, same authorization rules) and served over the `graphql-transport-ws` protocol or SSE: see [GraphQL subscriptions](graphql.md#subscriptions).

Presence is tracked per instance: a user's several connections count as one member, and `joining`/`leaving` reach every instance through the broker, but `here` lists only the members connected to the instance serving the client. A cluster-wide member list needs a shared store (planned: Redis sets).

## Vite

Every starter ships Vite 8 + Tailwind CSS v4 (`@tailwindcss/vite`) preconfigured (decision F2): `package.json`, `vite.config.ts`, `resources/css/app.css` (`@import "tailwindcss"` plus the starter styles in `@layer base`), `resources/js/app.ts` and the hot-file plugin `resources/vite/vukem.js`, which builds into `resources/public/build` (embedded with `//go:embed all:public`). `vukem new` runs `npm install` when npm is available (`--no-install` skips it); `vukem build` refuses to run without `node_modules`. Outside production the CSP allows the running dev server (`SecureHeadersConfig.DevServer`), and the layout carries `<meta property="csp-nonce">` so Vite's injected styles get the nonce. Without a dev server or a build, `{{vite}}` renders an HTML comment in development (tests need no Node) and fails in production.

- `vukem.WithVite(os.DirFS("public"))` (or an `embed.FS`) enables the `vite` package (`vite.New(vite.Config{FS, Dev})`) behind the view funcs. `Dev` is on outside production.
- A tiny Vite plugin shipped with the starter (`resources/vite/vukem.js`) writes `public/hot` with the dev server URL on start and deletes it on exit.
- `{{ vite "resources/css/app.css" "resources/js/app.ts" }}`:
  - `public/hot` present and `Dev` on → `<script type="module" src="{hot}/@vite/client">` + each entry (CSS entries as `<link rel="stylesheet">`); `{{ viteReactRefresh }}` emits the React Refresh preamble (empty otherwise). The hot file must hold an `http(s)` URL; it is ignored in production.
  - otherwise → reads `public/build/.vite/manifest.json` (cached in production, re-read in development) and emits `<link rel="modulepreload">` for recursive static imports, `<link rel="stylesheet">` for entry and import CSS, then `<script type="module">`, each file once across entries.
- `{{ asset "resources/img/logo.png" }}` resolves through the dev server or the manifest when Vite knows the file, else falls back to the public path. Files that exist in the public directory (a vendored `js/htmx.min.js`) always use the public path, also while the dev server runs, because the app serves them and the dev server does not.
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

`vukem new app --inertia react|vue|svelte` (or the stack prompt) and `vukem install:inertia --force [--react|--vue|--svelte]` on an HTMX starter build an Inertia app: `vukem.WithInertia(inertia.Config{})` in `bootstrap/app.go`, `r.Use(app.Inertia.Middleware, shareProps(app))` in `routes/web.go`, controllers that call `inertia.Render`, tests with `AssertInertia`, the root view `resources/views/app.html` (CSP nonce meta, `{{vite}}`, `{{.Head}}`, `{{.Inertia}}`), and TypeScript pages in `resources/js/pages` with a default layout in `resources/js/layouts`, on Vite + Tailwind. `npm run typecheck` type-checks them. `vukem install:auth` on an Inertia app adds `Auth/*` and `Dashboard` pages instead of HTMX views, shares `auth.user`, clears history on logout and throttles logins by the email of JSON submissions. `npm run typecheck` runs `tsc` (React, TypeScript 7), `vue-tsc` or `svelte-check` (Vue and Svelte stay on TypeScript 6.0 until their checkers support 7). SSR is off by default; enable it with `inertia.Config{SSR: &inertia.SSRConfig{Bundle: "bootstrap/ssr/ssr.js"}}`.

Typed routes for the frontend (Wayfinder/Tuyau-style): `vukem codegen:routes` builds the app in warmup mode and writes `resources/js/routes.ts` (`-o file` elsewhere, `-o -` to stdout). Warmup (`vukem.KindWarmup`) loads config and runs providers' `Register` and the route definitions, but never `Boot` or `Shutdown`, so no database or other connection is opened; register routes in `Register`/`WithRoutes`, not in `Boot`, and do not filter out `KindWarmup` in a provider that registers routes. The file holds only named routes, sorted by name:

```ts
import { route, routes, type RouteName } from "./routes";

route("posts.show", { id: 7 });                     // "/posts/7"
route("files.show", { path: "a b/c" }, { page: 2 }); // "/files/a%20b/c?page=2"
route("home");                                      // routes without params take no object
routes["posts.store"].method;                       // "POST"
```

Params are required and typed per route (`string | number`), encoded with `encodeURIComponent` (each segment of a `{name...}` wildcard, keeping `/`); the optional second object becomes the query string (`null`/`undefined` values skipped). Regenerate after changing routes and commit the file.
