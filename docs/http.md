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
| Groups | `Group(prefix, fn)` with `.Use(mw...)`, `.Name(prefix)`, nesting; `Host(host, fn)` restricts a group to one host (ServeMux host pattern, port ignored; `URL` returns `//host/path`; `RouteInfo.Host`; invalid host = compile error) |
| Resources | `Resource(name, ctrl)`, `APIResource(name, ctrl)` (no `create`/`edit`), `.Only(...)`, `.Except(...)` |
| Listing | `vukem route:list` prints method, pattern, name, handler, middleware |

Conflicting patterns panic at boot (ServeMux behaviour) and the router wraps the panic into an error naming both routes. A duplicate route **name** is a build error too, naming both routes with their methods and paths.

### Reserved route names

Every route the framework registers itself is named under the `vukem.` prefix (`vukem.health` for `GET /up`), so an app can use any name it likes. It used to take the bare name `health`, and an app registering its own `GET /health` under that obvious name made the router refuse to boot — with the error pointing at the app's route, the one that looked wrong, and nothing in the docs or the type system saying the name was taken. Do not prefix your own names with `vukem.` and the two sets can never meet.

### Health and readiness

| Probe | Where | What it touches |
| --- | --- | --- |
| Liveness | `GET /up` (`vukem.health`) | the process only; answers `{"status":"ok"}` with `Cache-Control: no-store`, plus `"version"` when the app sets `vukem.WithVersion` (also in the readiness body; off by default so the probe does not fingerprint the build) |
| Readiness | `GET /up?checks=1`, or `app.ReadyHandler()` mounted on a path of your own | every registered health check, in parallel, each bounded by its timeout |

Register checks with `vukem.WithHealthCheck(name, check)` at build time, or `app.AddHealthCheck(name, check)` / `app.AddHealthCheckTimeout(name, timeout, check)` from a provider. `app.CheckHealth(ctx)` runs them and returns each error by name.

Three things decide whether this helps or hurts:

- **Point liveness at `/up` and nothing else.** A liveness probe that runs the checks turns one slow dependency into a restart loop, and a load balancer pointed at them turns a degraded service into zero healthy instances.
- **Make each check prove the dependency does what the app needs.** The database provider registers a `Ping`, and a pool hands out a connection to a database whose migrations never ran — the ping passes while no query the app makes can succeed. Add a check that reads a table the app owns (`SELECT count(*) FROM <table> WHERE 1 = 0` is cheap and index-free, and still fails when the table is absent). For a disk use [`storage.Check`](storage.md), because an existence check passes with credentials that cannot write.
- **The errors stay server-side.** `ReadyHandler` answers only `"ok"` or `"fail"` per check and logs the detail, because a driver error carries hosts and sometimes credentials. `CheckHealth` returns them for the log, never for a response body.

A check that hangs is abandoned at its timeout rather than accumulating goroutines, and while an abandoned run is still going the check fails immediately instead of starting another.

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

HTML forms can use `_method` (hidden field) for `PUT/PATCH/DELETE`; the method-override middleware applies only to `POST` with form content types. Multipart forms put it in the action's query string (`action="/posts/1?_method=PUT"`), so the body is never read before the handler streams it ([uploads](subsystems.md#uploads)).

## `*web.Ctx`

```go
type HandlerFunc func(c *Ctx) error
```

| Area | Methods |
| --- | --- |
| Request | `Request()`, `Context()`, `Param`, `Query`, `Header`, `Cookie`, `IP()`, `IsHTTPS()`, `IsJSON()`, `WantsJSON()` |
| Binding | `web.Bind[T](c) (T, error)` (a package function, [web/parse.go](../web/parse.go)): JSON, form, multipart, query; runs validation; ignores unknown keys (use a `.Strict()` schema with `web.Parse`, or `BindJSON`, to reject them); `BindJSON`, `FormFile(name)` |
| Responses | `Status`, `String`, `HTML`, `JSON`, `Created`, `Accepted`, `NoContent`, `Blob`, `Problem`, `Redirect`, `RedirectRoute`, `Back`, `Stream`, `File`, `Download` |
| Conditional | `NotModified(etag, lastModified)`, `CheckIfMatch(current)` |
| Views | `Render(name, data)`, `Fragment(name, block, data)` (HTMX), `View(v view.View)` (templ) |
| Session | `Session()`, `Flash(key, msg)`, `Old(field)`, `Errors()` |
| Auth (ALPHA) | `Authorize(ability, subject)`; the current user comes from `auth.User[U](c, guard)`, a package function |
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

`vukem.DefaultMiddleware(app, r)` installs the default stacks; `vukem.DefaultMiddlewareWith(app, r, vukem.MiddlewareOptions{...})` adjusts them without rebuilding the stack by hand: `SecureHeaders` edits the `SecureHeadersConfig` (`CSPSources`, `ReportURI`; validated at boot), `Stateless` drops sessions (CSRF then relies on `Origin`/`Sec-Fetch-Site` only; `csrfToken` renders empty), and `SessionBypass` / `CSRFBypass` take `ServeMux` patterns (a trailing `/` covers the subtree) served without sessions or CSRF checks (CSRF bypasses are logged at boot), and `I18n` configures the locale resolver (`*i18n.MiddlewareConfig`: sources, order, selectable locales; validated at boot, needs `WithTranslations`). The zero options equal `DefaultMiddleware`. `vukem.APIMiddlewareWith(app, r, vukem.APIMiddlewareOptions{SecureHeaders: ..., I18n: ...})` is the same for `APIMiddleware` (validated at boot; no session or CSRF options since that stack has neither). With `WithTranslations`, both stacks install the locale resolver; the API stack reads only `?lang=` and `Accept-Language` by default, so its 422 messages and `Content-Language` follow the request.

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

Features: `Get/Put/Forget/Pull`, `Flash` (next request), `Reflash`, old input, `Regenerate()` (new id, keep data — call on login), `Invalidate()`, idle and absolute timeouts, `session.CreatedAt` (creation time, kept by login), lazy loading (no store hit unless used), write only when dirty.

## CSRF

- `http.CrossOriginProtection` (Go 1.25) on the router stack: rejects cross-origin unsafe requests using `Sec-Fetch-Site`/`Origin`.
- Session cookie `SameSite=Lax`.
- Trusted origins and bypass patterns (webhooks) configured in `config/http.go`.
- Token fallback (`{{ csrfField }}`, `X-CSRF-Token`, `hx-headers:inherited`) kept available for non-browser clients and old browsers; enabled by config.

## Errors

```go
var ErrNotFound = errs.New(http.StatusNotFound, "not found")

type HTTPError struct { Status int; Message string; Err error; Headers http.Header; Key string; Params map[string]any }
type ValidationError struct { Fields map[string][]string }
```

- `errs.NewKey(status, key, fallback, params)` gives an `*HTTPError` a message key: the error handler shows `HTTPError.LocalizedText(ctx)`, translated by the request's `errs.Translator` (installed by `i18n.Middleware`, see [i18n](subsystems.md#i18n)), with `Message` as the English fallback. `errs.Translate(ctx, key, fallback, params)` does the same for any message built with a context.
- `NewValidation().Add(field, msg)` returns the `*ValidationError` for chaining (`return errs.NewValidation().Add(...)`); `Append(field, msg)` has no result, for statements that errcheck would otherwise flag.

- Handlers return errors; `web.DefaultErrorHandler` (or `Services.Errors`, a `web.ErrorHandler`, when set) maps them:
  - `*ValidationError` → see the validation table above.
  - `*HTTPError` / sentinels → status page (`resources/views/errors/<status>.html`, which receives `.Nonce` for a nonce'd `<style>`) or problem+json. The built-in status pages have no inline style attributes and nonce their `<style>`, so they render under the default CSP.
  - `orm.ErrNotFound` (an `*errs.HTTPError`, returned by `First`/`Find` for no row) → 404.
  - anything else → 500, logged with request id; debug page in development.
- Content negotiation: `Accept: application/json` or `/api` routes → RFC 9457 problem details (4xx `*HTTPError` extension members from `e.WithExtension(key, value)` / `HTTPError.Extensions` are added next to the standard members, never replacing one; 5xx never carry them); HTMX → fragment/status; `Services.ErrorPage` (a `web.ErrorPageFunc` taking a client-safe `web.ErrorPage{Status, Title, Detail, Errors}`; Inertia requests with `vukem.WithInertia`: the `Error` page component); otherwise HTML.
- Custom handling per error type: set `Services.Errors` to a `web.ErrorHandler`, match your types with `errors.AsType[*MyErr](err)`, and call `web.DefaultErrorHandler(c, err)` for the rest.
- Per group: `g.ErrorHandler(func(next web.ErrorHandler) web.ErrorHandler)` replaces the handler for the group's routes and nested groups, for errors returned by handlers and for middleware failures (`web.Fail`); `next` is the outer group's, the router's or the default handler. Unmatched paths (404/405) keep the router's handler. Route handlers read it from a private context key, not from the services in the context, so `auth.ProblemJSON` still only affects middleware errors (`TestGroupErrorHandler`).
- Panics are recovered by `middleware.Recover` and converted to 500.

### Debug error page

With `APP_DEBUG=true` outside production (`Services.Debug`), 5xx errors in a browser render a youch-style page (decision F7): the error message and root type, the `Unwrap` chain, the stack with source snippets (app frames open, framework and standard library frames collapsed), the request (method, URL, route, request id, headers with `Authorization`, `Cookie` and token headers redacted) and the Go version. The page carries the CSP nonce. JSON clients keep getting problem+json (with the error text as `detail`), htmx gets the fragment. In `vukem serve` the same report is printed in color on stderr (`Services.DebugOut`).

Stacks come from `errs.New`, `errs.Wrap`, `errs.WithStack(err)` (for errors from other packages) and recovered panics; `errs.Stack(err)` returns the one closest to the origin. Capturing is cheap and happens only on error paths; sentinel errors declared as package variables carry no stack.

## Static files and uploads

- `public/` served by `middleware.Static` with `http.FileServerFS` over an `os.Root`; `/build/` and fingerprinted names (a hex or Vite-style hash segment, not any long word) get `Cache-Control: public, max-age=31536000, immutable`; other files revalidate through `Last-Modified`, or a content-hash `ETag` for embedded files.
- Request bodies limited by `http.MaxBytesReader` (configurable per route: a route's `middleware.BodyLimit` or `upload.Rules.MaxSize` replaces the router-wide 10 MiB default, raising or lowering it; the limit is checked when the body is read, against the innermost limit).
- Uploads stored through `storage/` (ALPHA) using `os.Root` to prevent path traversal; filenames randomised by default. Streaming, resumable and direct uploads: [subsystems.md#uploads](subsystems.md#uploads).

## HTTPS and HTTP/2

The app server speaks HTTP/1.1 and, outside production (or with `HTTP_H2C=true`), HTTP/2 cleartext with prior knowledge. With `TLS_CERT_FILE` and `TLS_KEY_FILE` (PEM), or `vukem.WithTLSConfig(*tls.Config)` (for `autocert` or a custom `GetCertificate`), it serves HTTPS on `PORT` instead: ALPN `h2` and `http/1.1` through stdlib `http.Protocols`, TLS 1.2 minimum (a lower `MinVersion` is a boot error), Go's default cipher suites. The file pair is checked at most once a second on handshakes and reloaded when a modification time moves; a pair that fails to load keeps the previous certificate and logs an error. `HTTP_H2C` with TLS is a boot error. There is no plaintext listener unless `TLS_REDIRECT_ADDR` (e.g. `:80`) is set: it redirects every request with 308 to the `https` `APP_URL` (never to the request's Host). HSTS stays with the security headers middleware. Site: [HTTP/2 and HTTP/3](https://vukem.dev/docs/deployment/http2-and-http3).
