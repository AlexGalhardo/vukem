# HTTP layer

Routing, the request context, middleware, validation, sessions, CSRF and error handling. Decisions `A1`, `A3`, `A4`, `H1`–`H3` in [brainstorming.md](brainstorming.md).

## Routing

A thin layer over `http.ServeMux` (Go 1.22+ patterns). The router collects route definitions, compiles groups/prefixes into ServeMux patterns at boot, and keeps a name → pattern table.

```go
// routes/web.go
func Web(r *router.Router, app *vukem.App) {
	home := controllers.NewHome(app)
	posts := controllers.NewPost(app)

	r.Get("/", home.Index).Name("home")

	r.Group("/admin", func(g *router.Group) {
		g.Use(auth.Required())
		g.Resource("posts", posts)            // posts.index, posts.store, posts.show…
	}).Name("admin.")

	r.Handle("GET /metrics", promhttp.Handler()) // any http.Handler
}
```

| Feature | API |
| --- | --- |
| Verbs | `Get`, `Post`, `Put`, `Patch`, `Delete`, `Options`, `Match([]string, …)`, `Any` |
| Params | `{id}`, `{path...}`, `{$}` (ServeMux syntax); `c.Param("id")`, `c.ParamInt("id")` |
| Names | `.Name("posts.show")`; resources auto-named `<name>.<action>` |
| URL generation | `r.URLFor("posts.show", "id", 42)` → `/posts/42`; missing/extra params return an error; templates: `{{ route "posts.show" "id" .ID }}` |
| Groups | `Group(prefix, fn)` with `.Use(mw...)`, `.Name(prefix)`, nesting |
| Resources | `Resource(name, ctrl)`, `APIResource(name, ctrl)` (no `create`/`edit`), `.Only(...)`, `.Except(...)` |
| Listing | `vukem route:list` prints method, pattern, name, handler, middleware |

Conflicting patterns panic at boot (ServeMux behaviour) and the router wraps the panic into an error naming both routes.

### Resource controllers

`Resource` registers only the actions the controller implements, detected through one-method interfaces:

| Interface | Method | Route |
| --- | --- | --- |
| `Indexer` | `Index(c *Ctx) error` | `GET /posts` |
| `Creator` | `Create` | `GET /posts/create` |
| `Storer` | `Store` | `POST /posts` |
| `Shower` | `Show` | `GET /posts/{id}` |
| `Editor` | `Edit` | `GET /posts/{id}/edit` |
| `Updater` | `Update` | `PUT/PATCH /posts/{id}` |
| `Destroyer` | `Destroy` | `DELETE /posts/{id}` |

HTML forms can use `_method` (hidden field) for `PUT/PATCH/DELETE`; the method-override middleware applies only to `POST` with form content types.

## `*web.Ctx`

```go
type HandlerFunc func(c *Ctx) error
```

| Area | Methods |
| --- | --- |
| Request | `Request()`, `Context()`, `Param`, `Query`, `Header`, `Cookie`, `IP()`, `Accepts(...)`, `IsJSON()` |
| Binding | `Bind[T](c) (T, error)` — JSON, form, multipart, query; runs validation; `BindJSON`, `BindForm`, `File(name)` |
| Responses | `String`, `HTML`, `JSON`, `NoContent`, `Redirect`, `RedirectRoute`, `Back`, `Stream`, `File`, `Download` |
| Views | `Render(name, data)`, `Fragment(name, block, data)` (HTMX), `View(v view.View)` (templ) |
| Session | `Session()`, `Flash(key, msg)`, `Old(field)`, `Errors()` |
| Auth (ALPHA) | `User()`, `Auth()`, `Authorize(ability, subject)` |
| HTMX | `HTMX()` returns request/response helpers ([frontend.md](frontend.md#htmx)) |

`*web.Ctx` wraps `http.ResponseWriter` and `*http.Request`; `c.Writer()` and `c.Request()` give raw access. A `*web.Ctx` must not escape its handler goroutine.

Adapters: `router.Adapt(HandlerFunc) http.Handler` and `router.Wrap(http.Handler) HandlerFunc`.

## Middleware

Signature: `func(http.Handler) http.Handler`. Three stacks:

| Stack | Runs | Defaults |
| --- | --- | --- |
| Server | every request, even unmatched | `Recover`, `RequestID`, `Logger` (slog), `Static` |
| Router | only when a route matches | `MethodOverride`, `Session`, `CSRF`, `Flash`, `Auth` init (ALPHA) |
| Named | per route/group, may take options | `auth.Required(guards...)`, `auth.Guest()`, `ratelimit.Limit(name)`, `Timeout(d)`, `CORS(cfg)` |

`vukem make:middleware` generates the function and registers it as named middleware in `bootstrap/middleware.go`.

## Validation

Validation is done by the `z` package — Zod-style typed schemas. Full reference: [validation.md](validation.md).

```go
in, err := web.Parse(c, requests.StorePostSchema) // typed requests.StorePost or 422
```

Failure behaviour (centralised in the error handler):

| Request | Response |
| --- | --- |
| HTML form | `303` back, errors + old input flashed |
| HTMX | `422` re-rendering the form fragment (htmx 4 swaps 4xx) |
| Inertia (BETA) | `303` back; errors shared as `props.errors` |
| JSON / API | `422` `application/problem+json` with `errors` map |

## Sessions

Own `session/` package.

| Store | Package | Phase |
| --- | --- | --- |
| Cookie (AES-256-GCM, key via HKDF from `APP_KEY`, 4 KB limit) | core | Prototype |
| Memory (tests/dev) | core | Prototype |
| Database (`sessions` table, SQLite/Postgres) | core | Prototype |
| Redis | `drivers/redis` | BETA |

Features: `Get/Put/Forget/Pull`, `Flash` (next request), `Reflash`, old input, `Regenerate()` (new id, keep data — call on login), `Invalidate()`, idle and absolute timeouts, lazy loading (no store hit unless used), write only when dirty.

## CSRF

- `http.CrossOriginProtection` (Go 1.25) on the router stack: rejects cross-origin unsafe requests using `Sec-Fetch-Site`/`Origin`.
- Session cookie `SameSite=Lax`.
- Trusted origins and bypass patterns (webhooks) configured in `config/http.go`.
- Token fallback (`{{ csrfField }}`, `X-CSRF-Token`, `hx-headers:inherited`) kept available for non-browser clients and old browsers; enabled by config.

## Errors

```go
var ErrNotFound = errs.New(http.StatusNotFound, "not found")

type HTTPError struct { Status int; Message string; Err error }
type ValidationError struct { Fields map[string][]string }
```

- Handlers return errors; `errs.Handler` maps them:
  - `*ValidationError` → see the validation table above.
  - `*HTTPError` / sentinels → status page (`resources/views/errors/<status>.html`) or problem+json.
  - `orm.ErrNoRows` → 404.
  - anything else → 500, logged with request id; debug page in development.
- Content negotiation: `Accept: application/json` or `/api` routes → RFC 9457 problem details; HTMX → fragment/status; otherwise HTML.
- Custom renderers per error type: `h.Render[*MyErr](func(c *Ctx, e *MyErr) error {...})`.
- Panics are recovered by `middleware.Recover` and converted to 500.

### Debug error page

With `APP_DEBUG=true` outside production (`Services.Debug`), 5xx errors in a browser render a youch-style page (decision F7): the error message and root type, the `Unwrap` chain, the stack with source snippets (app frames open, framework and standard library frames collapsed), the request (method, URL, route, request id, headers with `Authorization`, `Cookie` and token headers redacted) and the Go version. The page carries the CSP nonce. JSON clients keep getting problem+json (with the error text as `detail`), htmx gets the fragment. In `vukem serve` the same report is printed in color on stderr (`Services.DebugOut`).

Stacks come from `errs.New`, `errs.Wrap`, `errs.WithStack(err)` (for errors from other packages) and recovered panics; `errs.Stack(err)` returns the one closest to the origin. Capturing is cheap and happens only on error paths; sentinel errors declared as package variables carry no stack.

## Static files and uploads

- `public/` served by `middleware.Static` with `http.FileServerFS` over an `os.Root`; hashed assets get `Cache-Control: public, max-age=31536000, immutable`.
- Request bodies limited by `http.MaxBytesReader` (configurable per route).
- Uploads stored through `storage/` (ALPHA) using `os.Root` to prevent path traversal; filenames randomised by default.
