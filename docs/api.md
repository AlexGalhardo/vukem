# REST APIs

Vukem is equally a framework for **JSON/REST APIs** and for server-rendered apps. An API-only project (`vukem new shop --api`) has no views, sessions or CSRF, and ships token auth, JSON errors, pagination, rate limiting and CORS. Decision `H6` in [brainstorming.md](brainstorming.md).

## Starter

```sh
vukem new shop --api        # JSON-only skeleton (no views, no htmx, no sessions or CSRF)
vukem install:api           # add token auth and /api/v1 to an existing project (web or API)
```

`vukem new shop --api` produces:

| Area | What you get |
| --- | --- |
| Middleware | `vukem.APIMiddleware`: recover, request id, logging, security headers, body limit; no sessions or CSRF (`vukem.APIMiddlewareWith` edits the security headers); with `WithTranslations`, the locale resolver (`?lang=`, `Accept-Language`) |
| Auth | users, `accounts` (shared with `install:auth`), opaque access tokens: `POST /api/v1/tokens` (email, password, device_name → 201 `{token, type, expires_at}`, 5 per minute per IP; 403 for users whose email is not verified), `GET /api/v1/me`, `DELETE /api/v1/tokens/current` |
| Example resource | `/api/v1/posts`: public reads with `?filter[title]`, `?sort=title,-id`, `?page`/`?per_page` envelopes; token-protected writes |
| Cross-cutting | CORS from `CORS_ALLOWED_ORIGINS` (deny by default), 60 requests per minute per token/IP, problem+json errors |
| Wire format | `app/resources` transformers (`vukem make:resource`) |
| Tests | `tests/api_tokens_test.go`, `tests/posts_test.go` |

`install:api` adds the token routes to a web app; it reuses the authentication core when `install:auth` already ran. A web app can serve both: `routes.All()` lists each route file.

## Routing

```go
func API(r *router.Router, app *vukem.App) error {
	cors, err := middleware.CORS(middleware.CORSConfig{AllowedOrigins: []string{"https://app.example.com"}})
	if err != nil {
		return err
	}

	r.Group("/api/v1", func(g *router.Group) {
		g.Use(middleware.JSONOnly(), cors, ratelimit.Middleware(ratelimit.NewMemory(120, time.Minute), ratelimit.ByIP))

		g.Post("/tokens", auth.IssueToken).Name("api.tokens.store")

		g.Group("", func(p *router.Group) {
			// Tokens are a rate-limit key only once auth.Required verified them.
			p.Use(auth.Required("api"), ratelimit.Middleware(ratelimit.NewMemory(60, time.Minute), ratelimit.ByToken))
			p.APIResource("products", controllers.NewProduct(app)) // index, store, show, update, destroy
			p.APIResource("products.reviews", controllers.NewReview(app)).Shallow()
		})
	}).Name("api.v1.")

	return nil
}
```

- `APIResource` registers only JSON actions (no `create`/`edit` form routes).
- Nested resources: `"products.reviews"` → `/products/{product}/reviews`; `.Shallow()` puts `show/update/destroy` at `/reviews/{review}`.
- `PUT` and `PATCH` both map to `Update`; `HEAD` is answered for every `GET`; `OPTIONS` handled by CORS.

## Requests

- `web.Bind[T](c)` decodes `application/json` (rejects unknown fields by default; configurable), runs validation, and on failure returns `422` problem+json.
- Body size limited per group (`middleware.BodyLimit(1 << 20)`); oversize → `413`.
- `middleware.JSONOnly()` rejects non-JSON bodies on write methods with `415` and makes a missing or `*/*` `Accept` negotiate JSON (errors render as problem+json).
- Query helpers: `c.QueryInt`, `c.QueryBool`, `c.QueryTime`, `c.QuerySlice`.

### Payloads whose shape is not fixed

A body whose shape varies by a discriminator field — one `POST /conversions` taking `{"tool": "...", "options": {...}}` instead of one route per tool — cannot be described field by field. Declare the varying part as `map[string]any` (an object) or `any` (any JSON value) and `z.For` describes it as an open object; `z.JSONObject()` and `z.JSON()` are the same thing in a hand-built `z.Shape`. The document gets `type: object` with `additionalProperties`, or the empty schema for any value, and the generated clients get `Record<string, unknown>` / `unknown` next to a fully typed discriminator.

Use `Describe` (or a `doc:"..."` tag): nothing else in the document says what belongs in the value. Validate the decoded payload in the handler, or with `Refine` once the discriminator has decided the shape.

`json.RawMessage` is refused with that advice, not described: `Parse` receives decoded JSON, so the bytes the client sent are already gone and re-encoding them changes key order and number formatting. Until this was refused it was silently taken for the `[]byte` it is built on — the document claimed an array of integers and the validator rejected every object body.

A real discriminated union (OpenAPI `oneOf` plus `discriminator`, which generators turn into a TypeScript discriminated union) is the typed version of this and is not built yet; an open object is the floor it would stand on.

## Responses

```go
func (p *ProductController) Show(c *web.Ctx) error {
	id, err := c.ParamInt("product") // not a number → 404
	if err != nil {
		return err
	}

	product, err := orm.QueryOf[models.Product](p.db).Find(c.Context(), id)
	if err != nil {
		return err // orm.ErrNotFound → 404 problem+json
	}
	return c.JSON(http.StatusOK, resources.Product(product))
}

func (p *ProductController) Store(c *web.Ctx) error {
	in, err := web.Bind[requests.StoreProduct](c)
	if err != nil { return err }
	product := models.Product{Name: in.Name, Price: in.Price}
	if err := orm.Create(c.Context(), p.db, &product); err != nil { return err }
	location, err := c.URL("api.v1.products.show", "product", product.ID)
	if err != nil { return err }
	return c.Created(location, resources.Product(product)) // 201 + Location
}
```

| Helper | Status | Notes |
| --- | --- | --- |
| `c.JSON(status, v)` | any | `encoding/json` (json/v2 under Go 1.27) |
| `c.Created(location, v)` | 201 | sets `Location` |
| `c.Accepted(v)` | 202 | async work |
| `c.NoContent()` | 204 | |
| `c.Problem(status, title, detail)` | 4xx/5xx | RFC 9457 |

### Resources (transformers)

Separate the wire format from models (Laravel API Resources / AdonisJS v7 Transformers):

```go
// app/resources/product.go — generated by `vukem make:resource Product`
type ProductJSON struct {
	ID        int64        `json:"id"`
	Name      string       `json:"name"`
	Price     string       `json:"price"`
	Reviews   []ReviewJSON `json:"reviews,omitzero"` // only when eager-loaded
	CreatedAt time.Time    `json:"created_at"`
}

func Product(p models.Product) ProductJSON { ... }
func ProductList(ps []models.Product) []ProductJSON { return api.Map(ps, Product) }
```

Plain functions and structs: typed, testable, no reflection. Relations are included only when loaded (strict mode guarantees no hidden queries).

### Envelopes

Default: **no envelope** for single resources; collections and pages use:

```json
{
  "data": [ { "id": 1, "name": "Keyboard" } ],
  "meta": { "page": 1, "per_page": 20, "total": 134, "last_page": 7 },
  "links": { "self": "/api/v1/products?page=1", "next": "/api/v1/products?page=2", "prev": null }
}
```

`api.Page(c, page, resources.Product)` builds it from `orm.Page[T]` (offset); `api.Cursor(c, page, resources.Product)` from `orm.CursorPage[T]` (keyset on the primary key, `meta.next_cursor`, `links.next` is `null` on the last page; a tampered cursor is a 400); `api.Collection(c, items, fn)` answers `{"data": [...]}` without meta.

```go
page, err := orm.QueryOf[models.Product](db).CursorPaginate(ctx, c.Query("cursor"), 50, orm.Asc)
```

Envelope and problem keys are snake_case by design (`per_page`, `last_page`, `next_cursor`, `request_id`); RFC 9457 fixes the problem members (`type`, `title`, `status`, `detail`, `instance`) anyway. There is no key-case switch: it would desynchronise the generated OpenAPI document. An app with a camelCase contract builds its own envelope with `api.CollectionOf(items, fn, build)`, `api.PageOf(c, page, fn, build)` or `api.CursorOf(c, page, fn, build)`: `build func(data []R, m *api.Meta, l *api.Links) E` receives what `Collection`/`Page`/`Cursor` would write (nil meta and links for a collection) and returns the app's `E`. They return `E` instead of writing it, so a typed handler returns it and the OpenAPI document describes `E` with the app's keys. A generic `E` gets a long component name (`MyPage_<pkg path>.ProductJSON`); declare a named type (`type ProductPage MyPage[ProductJSON]`) for a short one.

## Query conventions

Opt-in, allowlisted per endpoint (never free-form columns):

```go
req, err := api.Query(c, orm.QueryOf[models.Product](p.db),
	api.Filters("category", "in_stock"),        // ?filter[category]=books,toys&filter[in_stock]=true
	api.Sorts("name", "price", "-created_at"), // ?sort=-price,name  (default: last listed)
	api.Includes("reviews"),                   // ?include=reviews → eager-loads Product.Reviews
	api.Fields("id", "name", "price"),         // ?fields=id,name
	api.PerPage(20, 100),                      // ?page=2&per_page=50 (capped at 100)
)
if err != nil {
	return err // 400 problem+json
}

page, err := req.Paginate(c.Context()) // or req.CursorPaginate(c.Context(), orm.Asc)
```

- An allowlisted include that names a relation of the model (its Go field name, snake_case form or `json` tag: `reviews` matches `Reviews`) is eager-loaded on `req.Query` with `With`; other allowlisted names only appear in `req.Includes` for the handler. An include loads **every** related row of each item on the page (a has-many is not paginated) and related models are serialized with their own `json` tags when the response embeds them, so allowlist only bounded relations whose fields are safe to expose, or map them through a resource.
- `req.CursorPaginate(ctx, dir)` pages by primary key from `?cursor` and drops the default sort of `api.Sorts`; a `?sort` sent by the client cannot be honoured with a cursor and answers 400. Calling `req.Query.CursorPaginate` directly pages by the query's `OrderBy` (client sort included).

Unknown filter/sort/field/include → `400` problem+json listing the allowed values (`TestSecurity_A05_QueryConventionsRejectUnknownColumns`). Filter values are always bound parameters; `true`/`false` become booleans. `?page` above `api.MaxPage(n)` (default 1000) and a filter with more values than `api.MaxFilterValues(n)` (default 100) are 400 too (`n <= 0` keeps the default), so a request cannot make the database skip millions of rows or bind thousands of parameters; use `?cursor` for deep lists (`TestSecurity_A04_QueryBoundsPageAndFilterLists`).

## Errors

All API errors are `application/problem+json` (RFC 9457). The `type` defaults to `about:blank`; with a base URI (`APP_PROBLEM_BASE_URL`) validation errors use `<base>/validation`:

```json
{
  "type": "about:blank",
  "title": "Validation failed",
  "status": 422,
  "detail": "2 fields are invalid.",
  "instance": "/api/v1/products",
  "request_id": "01JA…",
  "errors": { "name": ["The name field is required."], "price": ["The price must be at least 0."] }
}
```

| Condition | Status |
| --- | --- |
| Malformed JSON / unknown query param | 400 |
| Missing/invalid token | 401 (`WWW-Authenticate: Bearer`) |
| Policy denial | 403 (or 404 with `DenyAsNotFound`) |
| Not found / `orm.ErrNotFound` | 404 |
| Method not allowed | 405 (`Allow` header) |
| Conflict (unique violation, stale `If-Match`) | 409 / 412 |
| Unsupported media type | 415 |
| Validation | 422 |
| Rate limited | 429 (`Retry-After`) |
| Unhandled | 500 (no internals unless debug) |

Database unique-constraint violations are mapped to `409` by the dialect.

## Authentication

- **Access tokens** (opaque, hashed, abilities, expiry) — see [authentication.md](authentication.md#access_token-api). `Authorization: Bearer <token>`.
- `POST /api/v1/tokens` (email + password → token) generated by `install:api`; `DELETE /api/v1/tokens/current` revokes.
- The request states the key's abilities (`"abilities": ["read"]`), out of the generated `apiv1.Abilities()`; an unknown one is a 422 and omitting the field grants **none**, so a key never carries more authority than was asked for. The generated `GET /api/v1/me` is gated on `apiv1.AbilityRead`.
- Abilities: `auth.Ability("products:write")` middleware.
- SPA on the same site can use the session guard + `CrossOriginProtection` instead of tokens.
- JWT guard available later behind the same `Guard` interface (post-1.0).

## Cross-cutting

| Concern | Support |
| --- | --- |
| CORS | `middleware.CORS(cfg)`: allowlisted origins, methods, headers, credentials, max-age; deny by default; `*` with credentials is a boot error |
| Rate limiting | `ratelimit.Middleware(limiter, key)`: GCRA (`ratelimit.NewMemory(limit, window)`), keyed by `ratelimit.ByIP` (the default) or `ratelimit.ByToken` (hashed bearer token, else IP; use it after `auth.Required`, behind an IP limit, since unverified tokens would each get a bucket); `RateLimit-Limit/Remaining/Reset` on every response, `429` + `Retry-After` when empty; a failing store denies (503) |
| Versioning | URL prefix (`/api/v1`) by default; route groups make `/api/v2` side-by-side trivial; `api.Deprecate(since, sunset, link)` sets `Deprecation`, `Sunset` and `Link: rel="deprecation"` |
| Caching | `web.ETagOf(v)` builds a strong ETag; `c.NotModified(etag, lastModified)` sets `ETag`/`Last-Modified` and answers 304 to a matching `If-None-Match` / `If-Modified-Since` |
| Concurrency control | `c.CheckIfMatch(etag)` returns a 412 error when `If-Match` does not match |
| Idempotency | `api.Idempotency(store, api.IdempotencyOptions{})` on any `cache.Store` (memory, database, Redis): the first `POST`/`PATCH` with an `Idempotency-Key` runs and its response (below 500, without `Set-Cookie`, `Authorization`, `Proxy-Authorization`, `Proxy-Authenticate` or `WWW-Authenticate`, which are never replayed even when listed) is stored for 24h; repeats replay it with `Idempotent-Replayed: true`, an in-flight duplicate gets 409, the same key with a different request 422. A replay sends the stored status, body, `Content-Type` and the representation headers in `ReplayHeaders` (default `Content-Language`, `Content-Disposition`, `Content-Location`, `Location`, `ETag`, `Last-Modified`, `Link`); every other header (`X-Request-ID`, `RateLimit-*`, security headers) comes from the repeat request's own middleware. A keyed body is read whole to fingerprint it, so `MaxBody` (default 1 MiB) is the body limit of keyed requests: a larger one is a 413 before the handler runs, whatever the route's own body limit allows; raise `MaxBody` on upload routes that take keys. Keys are scoped to the caller: by default the authenticated user (`auth.Principal`, when `auth.Required`/`auth.Optional` ran first, which is where it belongs), else a SHA-256 of `Authorization`; set `Scope` for other identities (`TestSecurity_A01_IdempotencyScopedToPrincipal`). Store errors answer 503. |
| Compression | gzip middleware (stdlib `compress/gzip`); the browser starters (`vukem new`, `-inertia`, `-templ`) register `r.UseServer(middleware.Gzip())` before `StaticFS`, so assets and pages are compressed without a compressing proxy. No Brotli: the stdlib has no encoder and a dependency is not worth it while gzip covers the budget |
| Health | `/up` (liveness), `/up?checks=1` or `app.ReadyHandler()` (readiness) |
| Request ID | `X-Request-ID` propagated and returned |

## OpenAPI

Typed handlers produce an OpenAPI 3.1 document without annotations (Huma/Fuego approach):

```go
api.Register(g, "POST /products", api.Op{
	Name:    "products.store",
	Summary: "Create a product",
	Tags:    []string{"products"},
}, func(c *web.Ctx, in requests.StoreProduct) (resources.ProductJSON, error) { ... })
```

- Input: query string, JSON/form body (not for GET, HEAD, DELETE) and path parameters, which win over body fields; validated with the input struct's `validate` tags (422 with field errors, localized with the request locale like `web.Parse`). `struct{}` means no input.
- `api.RegisterWith(g, pattern, op, schema, fn)` validates the input with a `z.Schema[In]` of your own instead of the input struct's `validate` tags, for the rules tags cannot express: a bound computed from a value (`z.Slice(z.String()).Max(len(abilities.All()))`) or a cross-field `z.Refine`. Everything else is `api.Register` (binding order, 422 with field errors, output handling); a nil schema panics at boot. The OpenAPI document is still generated from the input and output types, so the custom schema is invisible to it and the document is byte-identical to the one `api.Register` would produce (`TestRegisterWithKeepsTagDerivedOpenAPI`): keep the static rules in the `validate` tags and let the schema add only what they cannot say, otherwise the document under-describes what the route enforces.

```go
schema := z.Struct[requests.CreateKey](z.Shape{
	"name":      z.String().Min(1).Max(20),
	"abilities": z.Slice(z.String().OneOf(abilities.All()...)).Min(1).Max(len(abilities.All())),
}).Refine(requests.CheckKey)

api.RegisterWith(g, "POST /keys", api.Op{Name: "keys.store"}, schema,
	func(c *web.Ctx, in requests.CreateKey) (resources.KeyJSON, error) { ... })
```

- Output: JSON with `Op.Status`, or 201 for POST, 204 when the output type is `struct{}`, 200 otherwise; an output of `struct{}` never writes a body, also with an explicit `Op.Status` such as 202 (`TestNoOutputWithStatusSendsNoBody`). Errors returned by the handler go through the normal problem+json path.
- `Op.ValidateResponse: true` checks the output against its `validate` tags (`z.For[Out]`, Out must be a struct) before writing it, when `web.Services.CheckResponses` is on, like `web.JSONWith`. The app turns it on whenever `APP_ENV` is not `production` (`development` or `test`), so a staging deployment that runs with `APP_ENV=development` also checks, and answers 500 on a mismatch; only `APP_ENV=production` skips it. A mismatch is logged and answered with a generic 500, so contract drift fails loudly.
- Each typed route carries an `api.Operation` (`router.RouteInfo.Doc`) with its method, full path, params and input/output types for the generators below.
- Request schema from the input struct (`json` + `validate` tags), response schema from the output type, errors from the problem catalogue.
- `vukem.ServeOpenAPI(app, r, "/openapi.json")` serves the document (not registered in production unless `OPENAPI_IN_PRODUCTION=true`); `vukem openapi:export > openapi.json` prints it (warmup mode, like `codegen:types`: no database, `APP_KEY` optional). Title is `APP_NAME`, version `API_VERSION`, and `OPENAPI_PREFIX=/api` keeps only API routes; `openapi:export -prefix /api`, `codegen:types -prefix /api` and `codegen:client -prefix /api` do the same per run and override the variable. `vukem.WithOpenAPI(openapi.Info{...})` supplies the rest of the document the app owns (`Description`, `SecuritySchemes`): a `Title`, `Version` or `Servers` set there wins over the configuration, an empty one keeps it, and the prefix of `ServeOpenAPIWith` wins over `Info.PathPrefix`. `vukem.ServeOpenAPIWith(app, r, path, vukem.OpenAPIOptions{PathPrefix, InProduction})` serves a document per prefix (whole path segments: `/api/v1` excludes `/api/v10`; one per API version) with its own production opt-in; its route is unnamed.
- Schemas come from `json` tags (names, `-`) and `validate` rules (`required`, `min`/`max`/`len`, `email`, `url`, `uuid`, `oneof`, `regex`, `positive`, `unique`, `dive`...; the full table is in [validation](validation.md)); a `doc:"..."` tag becomes the description and an `example:"..."` tag becomes the example (decoded to the field's JSON type: `example:"7"` on an `int` emits `7`, on a composite field the tag is JSON, and anything that does not decode stays a string). `required` marks the key as required; on a string it also emits `minLength: 1` (z rejects an empty string), on a list it does not emit `minItems` (an empty list passes; use `min=1`). Named structs become `components/schemas`, `orm` relations and `NullTime` map to their JSON shape, errors reference the shared `Problem` and `ValidationError` responses, and `Op.Security` names become security requirements: `bearer` is predefined, other schemes go in `openapi.Info.SecuritySchemes` (`vukem.WithOpenAPI` for an app), and an undeclared name is an error (`openapi.ErrUnknownSecurityScheme`) instead of silently becoming bearer. The document is built when it is asked for, not at boot: the first `GET /openapi.json` (a `500` problem, the cause in the log), `vukem openapi:export`, `codegen:types`, `codegen:client` or `AssertMatchesOpenAPI` reports these errors. Add a test that calls `app.OpenAPI()`, or run `vukem openapi:export` in CI, to catch them before deploying.
- Lint-friendly output (VF-053): `openapi.Info.License` (`&openapi.License{Name, Identifier}` or `{Name, URL}`, not both; a license without a name is a build error) fills `info.license`; `openapi.Info.Security` names the schemes every plain route (registered without `api.Register`) requires, and `.Doc(openapi.Public{})` exempts one (`"security": []`); plain routes carry a `4XX` response referencing `Problem`, since every vukem error is problem+json; the `Problem` and `ValidationError` components are emitted only when an operation references them (`TestBuildLicenseSecurityAndErrorsForPlainRoutes`).
- The `operationId` of every route is the route's own name without the group name prefix (`products.store` inside `.Name("api.v1.")`; `Op.Name` for a typed route), for typed and plain routes alike. Route names are unique per router, but two groups may reuse one local name (`v1` and `v2` both naming `products.store`); building the document then fails with `openapi.ErrDuplicateOperationID` naming both routes, so give each version its own document (`OpenAPIOptions.PathPrefix`) or rename one route.
- `openapi.Build(routes, openapi.Info{...})` builds the document directly and returns its error; `openapi.Generate` panics on it. `.Doc(openapi.Skip{})` hides a route. CI validates a golden document with Redocly.
- A `PathPrefix` that keeps no route is an error (`openapi.ErrEmptyDocument`) naming the prefix and how many documentable routes were skipped, because an empty `paths` object looks exactly like a broken app; a router with no documentable route at all still builds an empty document. A common cause is Git Bash on Windows rewriting `OPENAPI_PREFIX=/api/v1` into `C:/Program Files/Git/api/v1`.
- Examples beyond the `example` tag are set on the document: `Example` (one value) and `Examples` (named `openapi.Example` alternatives) on `openapi.MediaType` and `openapi.Parameter`, so two responses that share one component schema can still show different payloads.
- Plain `HandlerFunc` routes still work; they appear in the document with their path parameters, an optional JSON body of any shape on `POST`/`PUT`/`PATCH` (generated clients take a `body` argument) and a default response of any content (so `AssertMatchesOpenAPI` accepts it). Use `api.Register` to describe their input and output.

## Testing

```go
app.Client(t).
	WithToken(token).
	PostJSON("/api/v1/products", map[string]any{"name": "Pen", "price": "2.50"}).
	AssertCreated().
	AssertHeader("Location", "/api/v1/products/1").
	AssertJSONPath("name", "Pen")

app.Client(t).GetJSON("/api/v1/products?sort=-price").
	AssertOK().
	AssertJSONCount("data", 20).
	AssertJSONPath("meta.total", 134)

app.Client(t).PostJSON("/api/v1/products", map[string]any{}).
	AssertProblem(422).
	AssertJSONValidationErrors("name", "price")
```

### API reference UI

```go
vukem.ServeOpenAPI(app, r, "/openapi.json")
apidocs.Serve(app, r, "/docs", apidocs.Config{
	Theme:          "purple",
	Authentication: map[string]any{"preferredSecurityScheme": "bearer"},
})
```

- The Scalar bundle (`@scalar/api-reference` 1.72.3, MIT) is vendored in `apidocs/scalar`, embedded and pinned by SHA-256 (`apidocs.ScalarSHA256`, checked in tests): no CDN, telemetry, remote fonts or AI agent.
- The page runs under the app's nonce-only CSP (no `'unsafe-inline'`): it passes the request nonce to Scalar (a `csp-nonce` meta and a small `page.js` shim that nonces the `<style>` elements Scalar creates and applies the plain size/overflow style attributes it sets through the CSSOM; any other style attribute is left to CSP, which blocks it). HTML from the OpenAPI document (descriptions) never gets the nonce: its `<style>` elements and style attributes stay blocked. Scalar's "Generate MCP" link and developer-tools header (both scalar.com services) are off. Its policy is `apidocs.CSP`, the default one plus `data:` fonts; `connect-src 'self'` keeps "try it" requests on your origin. Inline styles inside HTML that Scalar injects (code highlighting) stay blocked, except an attribute that is exactly the numeric line-number variables (`--line-count`/`--line-digits`): the shim moves it out of the markup before parsing and applies it through the CSSOM, so CSP never sees it. The shim assumes the page's CSP has no `require-trusted-types-for` (the rewritten markup is a plain string; `apidocs.CSP` has none). The shim also sets Zod's `jitless` flag, so Scalar's bundled Zod never probes `eval` (a `script-src` violation). The page loads with zero CSP console errors (`TestBrowser_APIDocsNoCSPViolations`, headless Chromium, skipped without one or with `-short`; `VUKEM_CHROME` points at a browser).
- Accessibility: the default theme gets AA text colors (muted text, HTTP method labels) and the shim makes the scrollable endpoint list keyboard-focusable and puts the visible text into the "Show More" buttons' accessible names, so the page passes axe serious checks in light and dark mode (checked by hand with axe-core 4.10; `TestDefaultThemeColorsMeetAA` keeps the colors at 4.5:1). The shim also fixes two findings of Scalar's own markup: the intro cards caption their value with a `<label>` that has no `for` and wraps no control (the "Server" card), which DevTools reports as "No label associated with a form field" — a caption is a `<span>`, so the shim retags it and keeps its attributes and children; and the filter field of a dropdown is a `role="combobox"` input that Scalar only names when the component opening it passes `inputLabel` (the client picker and the search modal do, the auth-scheme selector does not), so the shim names an unnamed one "Search". Both are checked by `TestBrowser_APIDocsNoCSPViolations` on the rendered DOM and by `testdata/shim_test.mjs`. Other themes keep their own colors.
- Like `/openapi.json`, it is not registered in production unless `OPENAPI_IN_PRODUCTION=true` or `Config.InProduction` (one page only). For a public, versioned API serve its document and page explicitly:

```go
vukem.ServeOpenAPIWith(app, r, "/api/v1/openapi.json",
	vukem.OpenAPIOptions{PathPrefix: "/api/v1", InProduction: true})
apidocs.Serve(app, r, "/api/docs", apidocs.Config{SpecURL: "/api/v1/openapi.json", InProduction: true})
```

- The `apidocs` package (about 4 MB) is linked only when imported.
- `Config.Name` (default `apidocs`) names the page route; the bundle and shim are `<Name>.script` and `<Name>.page`. Give each reference its own name to serve several on one router.
- `apidocs.Command()` adds `vukem openapi:serve [-addr localhost:8081]` to preview the reference locally.

### Types for clients

- `z.JSONSchema(schema)` exports any `z` schema as JSON Schema 2020-12 (types, required keys, lengths, ranges, formats, patterns, enums, defaults, descriptions; `Refine`/`Custom` logic cannot be expressed).
- `vukem codegen:types -o resources/js/api.ts` (missing directories are created) writes TypeScript types for every component schema and operation; `-zod` adds Zod 4 validators (`zod` 4.0+): `<Name>Schema` for every component schema and `<Operation>BodySchema` for every JSON request body, including inline ones. It takes `-prefix` like `codegen:client`. It runs in warmup mode too: no provider `Boot`, so no database connection, and `APP_KEY` may be unset (routes that derive a key at build time still fail with `config.ErrKeyMissing`), so CI can run it without secrets. A component whose name would shadow a TypeScript or DOM global, or a name the generated client declares (`Number`, `String`, `Object`, `Date`, `Error`, `Promise`, `Record`, `Array`, `Response`, `Request`, `URL`, `Client`, ...), gets a `Type` suffix: a Go type `Number` becomes `NumberType` (Zod: `NumberTypeSchema`). If two generated names still collide (a `Number` and a `NumberType` schema, a component named like an operation type such as `<Op>Body` or `<Op>Params`, or two operation names that map to the same identifier), `codegen:types` and `codegen:client` fail with an error naming the duplicate instead of emitting code that does not compile. CI type-checks the golden output with `tsc --strict`.

`AssertMatchesOpenAPI()` validates the response against the generated document: it finds the operation for the request path, then checks the body against the schema of the response status (or the default problem response). It supports the JSON Schema subset the generator emits (types, `$ref`, `anyOf`/`allOf`, `required`, lengths, ranges, `pattern`, `enum`, `date-time`). `doc.ValidateResponse(method, path, status, body)` does the same outside tests. OpenAPI cannot express a multi-segment `{path...}` wildcard, so it is documented as one segment.

### Client generation

`vukem codegen:client` turns the OpenAPI document of the typed routes (`api.Register`, described from their `z` types) into a typed client. It runs in warmup mode, so no connection is opened.

```sh
vukem codegen:client                                  # TypeScript → resources/js/api.ts
vukem codegen:client -lang go -package apiclient      # Go → internal/apiclient/client.go
vukem codegen:client -lang go -o -                    # print to stdout
vukem codegen:client -prefix /api                     # only routes under /api
```

`-prefix` scopes generation to one path prefix, the flag form of `OPENAPI_PREFIX` and of [`OpenAPIOptions.PathPrefix`](#openapi); `openapi:export` and `codegen:types` take it too. An explicit `-prefix` overrides `OPENAPI_PREFIX`; without the flag the environment variable still applies. Use it in an app that also serves server-rendered pages, so the client does not grow a method per page (`dashboard()`, `login()`).

TypeScript (`openapi.TypeScriptClient(doc)`) emits the `codegen:types` types (no Zod), `<Op>Params` for path parameters and `createClient`. It has no dependencies and runs in browsers and Node 18+:

```ts
import { ApiError, createClient } from "./api";

const api = createClient({ baseUrl: "", headers: () => ({ Authorization: `Bearer ${token}` }) });
const product = await api.productsStore({ params: { shop: 1 }, body: { name: "Lamp" } });

try {
  await api.productsStore({ params: { shop: 1 }, body: { name: "x" } });
} catch (err) {
  if (err instanceof ApiError && err.status === 422) console.log(err.errors.name);
}
```

- One async method per operation with an operationId, camelCase (`products.store` → `productsStore`). Its single argument `{ params, query, body }` holds only what the operation takes, and each part (and the argument) is required only when the operation requires it.
- Path parameters are URL-encoded; query values skip `undefined`/`null` and repeat arrays (`?tags=a&tags=b`); bodies are JSON.
- Options: `baseUrl` (default `""`, the page's origin), `fetch` (default `globalThis.fetch`), `headers` (object or per-request function), `credentials` (default `"same-origin"`).
- Any non-2xx response throws `ApiError` with `status`, the parsed RFC 9457 `problem` and `errors` (field → messages on 422).

Go (`openapi.GoClient(doc, pkg)`) emits gofmt'd, stdlib-only source:

```go
c := apiclient.New("https://shop.example")
c.Header = func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }

p, err := c.ProductsStore(ctx, 1, apiclient.ProductsStoreBody{Name: "Lamp"})
if apiErr, ok := errors.AsType[*apiclient.Error](err); ok && apiErr.Status == 422 {
	fmt.Println(apiErr.Problem.Errors["name"])
}
```

- Component schemas become Go types: objects are structs with `json` tags (`omitempty` on optional fields), optional or nullable scalars and structs are pointers, `additionalProperties` maps, string enums named types with constants, `date-time` `time.Time`, integers `int64`, numbers `float64`, unions `json.RawMessage`. Inline schemas get `<Op>Body`, `<Op>Query` and `<Op>Response`; the untyped body of a plain route is `body any` (JSON-encoded, `nil` sends none).
- `Client{BaseURL, HTTP, Header}`: `HTTP` defaults to `http.DefaultClient`; `Header` is called on every request. Each method takes `ctx`, the path parameters as typed arguments, then the query struct and the body; operations without a response body return only `error`.
- Any non-2xx response returns `*apiclient.Error{Status, Problem}`.

Golden outputs live in `openapi/testdata/shop.client.ts` and `shop.client.go`; a round-trip test runs the generated Go client against a live app, and CI type-checks the TypeScript client with `tsc --strict`.

## gRPC

`contrib/grpc` (module `github.com/AlexGalhardo/vukem/contrib/grpc`, package `grpc`) serves [connect-go](https://connectrpc.com) services from the app router. connect-go handlers are plain `net/http`, so **gRPC, gRPC-Web and Connect** share one port, the router's middleware (request id, logging, rate limiting, auth guards) and the app container; no separate `google.golang.org/grpc` server.

```go
func routes(r *router.Router, app *vukem.App) error {
	opts := grpc.HandlerOptions(grpc.Options{
		Rules: []grpc.Rule{grpc.Validate(z.Struct[greetv1.GreetRequest](z.Shape{"name": z.String().Min(1)}))},
	})

	path, h := greetv1connect.NewGreetServiceHandler(&greeter{}, opts...)
	grpc.Mount(r, path, h).Use(grpc.Required(apiGuard), ratelimit.Middleware(limiter, ratelimit.ByToken))

	grpc.Health(r, grpchealth.NewStaticChecker(greetv1connect.GreetServiceName))
	grpc.Reflection(app, r, greetv1connect.GreetServiceName) // not in production

	return nil
}

func (g *greeter) Greet(ctx context.Context, req *connect.Request[greetv1.GreetRequest]) (*connect.Response[greetv1.GreetResponse], error) {
	user, err := auth.UserFrom[models.User](ctx, "api")
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&greetv1.GreetResponse{Greeting: "Hello, " + user.Name}), nil
}
```

- `grpc.Mount(r, path, h)` registers the generated handler on a router or group and returns the `*router.Route`. Middleware added with `.Use` runs inside the protocol error bridge (`grpc.Errors()`): anything it rejects through the error handler (401, 403, 429 with `Retry-After` metadata, 413, 503) reaches the client as an RPC error with the matching code. Router and group middleware run outside the bridge; add `grpc.Errors()` to a group to get the same mapping there.
- `grpc.Required(guards...)` is `auth.Required` for RPCs: `Unauthenticated` instead of a login redirect, even in apps with sessions. Methods read the user with `auth.UserFrom(ctx, guard)`; `auth.Optional` works as is.
- `grpc.HandlerOptions(grpc.Options{...})` bundles the connect-go options: request validation (`Rules`, plus messages with a `Validate() error` method), error mapping, one `rpc` log line per call (`procedure`, `protocol`, `code`, `duration`, `request_id`), panic recovery and a 4 MiB message limit (`MaxMessageBytes`, `ResourceExhausted` beyond). `Interceptors` run inside these. Append your own `connect.HandlerOption`s to the slice.
- `grpc.Error(err)` is the mapping: `*connect.Error` as is; `context.Canceled`/`DeadlineExceeded`; `z.Issues` and `*errs.ValidationError` → `InvalidArgument` with a `google.rpc.BadRequest` detail (one field violation per message); `*errs.HTTPError` → the code of its status (`grpc.CodeOf`: 400/422 `InvalidArgument`, 401 `Unauthenticated`, 403 `PermissionDenied`, 404 `NotFound`, 409 `AlreadyExists`, 412 `FailedPrecondition`, 429 `ResourceExhausted`, 501 `Unimplemented`, 503 `Unavailable`, 504 `DeadlineExceeded`, other 4xx `FailedPrecondition`, 5xx `Internal`) with its client-safe message below 500 and "internal error" from 500 up (the cause is logged; a 5xx wins over the `z.Issues` it wraps); anything else → `Internal` "internal error", the cause only in the log. A returned `*connect.Error` with a code other than Internal, Unknown, DataLoss or Unavailable keeps its text: that text is the app's explicit choice, so never put internals in it. A message's `Validate() error` yields `InvalidArgument` only for `z.Issues`, `*errs.ValidationError` and per-field errors with `Field()`/`Reason()` (protoc-gen-validate); any other error (a failed uniqueness query, a 5xx) is mapped like a handler error, so it is masked and logged — return `connect.NewError(connect.CodeInvalidArgument, err)` yourself for other validators such as protovalidate. For a 5xx `*errs.HTTPError` only `Retry-After` becomes metadata. `grpc.ErrorContext(ctx, err)` also translates keyed errors (`errs.NewKey`) with the `errs.Translator` of ctx (`HTTPError.LocalizedText`); `Mount`, `Errors()` and `HandlerOptions` use it with the request ctx, so RPC errors follow the request locale.
- `grpc.Validate(z.Struct[pkg.Msg](shape))` checks every `*pkg.Msg` request, unary or streamed; keys are the message's proto field names (its Go JSON tags).
- **Health**: `grpc.Health(r, checker)` mounts `grpc.health.v1.Health` ([grpchealth](https://pkg.go.dev/connectrpc.com/grpchealth)) for load balancers and probes.
- **Reflection**: `grpc.Reflection(app, r, services...)` mounts `grpc.reflection.v1` and `v1alpha` ([grpcreflect](https://pkg.go.dev/connectrpc.com/grpcreflect)) for `grpcurl` and `buf curl`. It publishes the whole API, so in production it registers nothing and returns false unless `GRPC_INSECURE_REFLECTION_IN_PRODUCTION=true`.
- **HTTP/2**: native gRPC needs HTTP/2. Outside production the app server also speaks HTTP/2 cleartext (h2c, stdlib `http.Protocols`), so `grpcurl -plaintext localhost:3000 list` works with `vukem serve`. Production expects TLS: terminate it at a proxy that speaks HTTP/2 to the app (h2c with `HTTP_H2C=true`), or serve TLS yourself. gRPC-Web and Connect also work over HTTP/1.1, from browsers too (same-origin, or CORS on the route).
- **Own port**: `grpc.Serve(app, addr, h, grpc.ServerOptions{TLS: cfg})` adds a `grpc` runner that serves `h` on its own address next to the HTTP server and stops with the app (graceful drain within `SHUTDOWN_TIMEOUT`). Give it a router that holds only the RPC services, so RPC paths stay off the HTTP port; `grpc.Server(&app.Config, addr, h, opts)` returns the bare `vukem.Runner` and applies the same production rule (a nil config counts as production). With `TLS` set it speaks HTTP/2 and HTTP/1.1 over TLS (`NextProtos` defaults to `h2`, `http/1.1`), otherwise h2c and HTTP/1.1. In production a cleartext server is refused (`grpc.ErrCleartextInProduction`) unless `GRPC_INSECURE_CLEARTEXT=true` (or `ServerOptions.InsecureCleartext`), for a private network or a proxy that terminates TLS and speaks h2c to the port; any other value than `true`/`false` fails boot (`grpc.ErrInvalidInsecureCleartext`). `HTTP_H2C` concerns only the main HTTP server and does not open the gRPC port. It uses the secure timeouts of `vukem.NewHTTPServer` (60s write timeout): long-lived streams need a server of their own.
- **Header cap**: the port caps request headers (gRPC metadata included) at `vukem.DefaultMaxHeaderBytes` (64 KiB); `HTTP_MAX_HEADER_BYTES` (read by `grpc.Serve`) or `ServerOptions.MaxHeaderBytes` sets 4096–1048576, anything else fails with `grpc.ErrInvalidMaxHeaderBytes`.
- **Railway**: the public HTTP domain terminates TLS and talks HTTP/1.1 to the app, so only Connect and gRPC-Web reach the HTTP port that way. For native gRPC, put the port behind a TCP proxy and serve TLS yourself (`ServerOptions.TLS`): the TCP proxy forwards raw bytes and terminates no TLS, so a cleartext port there is open to the internet. Between services of one project, private networking (`<service>.railway.internal:<port>`) carries cleartext gRPC with `GRPC_INSECURE_CLEARTEXT=true` and no public exposure.
- The separate port does **not** inherit the HTTP router's middleware: rate limiting, security headers, body limits and guards apply only if `h` installs them. Wrap it as below: `vukem.APIMiddleware` (recover, request id, logging, headers, body limit), `grpc.Required` and a limiter on the services, and a message cap through `HandlerOptions` (`connect.WithReadMaxBytes`, 4 MiB unless `MaxMessageBytes` says otherwise); the server itself bounds headers and timeouts, not bodies.

```go
func (p *RPCProvider) Boot(_ context.Context, app *vukem.App) error {
	rpc := router.New(app.Router.Services())
	if err := vukem.APIMiddleware(app, rpc); err != nil {
		return err
	}

	// MaxMessageBytes sets connect.WithReadMaxBytes (4 MiB by default).
	opts := grpc.HandlerOptions(grpc.Options{Log: app.Log, MaxMessageBytes: 1 << 20})
	path, h := greetv1connect.NewGreetServiceHandler(svc, opts...)
	grpc.Mount(rpc, path, h).Use(grpc.Required(apiGuard), ratelimit.Middleware(limiter, ratelimit.ByToken))
	grpc.Health(rpc, grpchealth.NewStaticChecker(greetv1connect.GreetServiceName))

	return grpc.Serve(app, ":"+app.Source.Get("GRPC_PORT"), rpc, grpc.ServerOptions{})
}
```

- Route middleware that wraps the writer stays compatible with streams: `Mount` flushes through the whole writer chain. Avoid `middleware.Timeout` and `Gzip` on RPC routes (use deadlines and connect-go compression instead).

### Code generation

The framework never needs `protoc` itself; your app runs the generators as [Go tools](https://go.dev/doc/modules/managing-dependencies#tools) pinned in its `go.mod`:

```sh
vukem make:proto Invoice      # proto/invoice/v1/invoice.proto, buf.yaml, buf.gen.yaml
go get -tool google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12
go get -tool connectrpc.com/connect/cmd/protoc-gen-connect-go@v1.21.1
go get github.com/AlexGalhardo/vukem/contrib/grpc
vukem codegen:rpc             # gen/invoice/v1/*.pb.go and invoicev1connect/*.connect.go
```

- **buf** (recommended): `codegen:rpc` runs `buf generate` with the `buf.gen.yaml` from `make:proto`, whose plugins are `local: ["go", "tool", "protoc-gen-go"]` and `["go", "tool", "protoc-gen-connect-go"]`. Pin buf too with `go get -tool github.com/bufbuild/buf/cmd/buf@v1.74.0` (then `go tool buf` is used), or install it on `PATH`. `buf lint` and `buf breaking` use the generated `buf.yaml` (STANDARD, FILE).
- **protoc**: without `buf.gen.yaml` or buf (or with `--protoc`), `codegen:rpc` calls `protoc -I proto` on every `.proto` under `-proto` with the two plugins built by `go tool -n`, writing `paths=source_relative` output to `-out` (`gen`).
- Generated code is committed like any source; regenerate after editing a `.proto`.

### Testing RPC services

`contrib/grpc/grpctest` runs the app handler on an in-process HTTP/2 server and lists the client options of each protocol, so one test covers them all:

```go
h, _ := app.Handler()
srv := grpctest.NewServer(t, h) // TLS + HTTP/2; NewH2CServer for cleartext

for _, p := range grpctest.Protocols() { // grpc, grpc-web, connect, connect+json
	client := greetv1connect.NewGreetServiceClient(srv.Client, srv.URL, p.Options...)
	res, err := client.Greet(t.Context(), connect.NewRequest(&greetv1.GreetRequest{Name: "Ada"}))
	// ...
}
```

## Beyond REST

- **SSE**, WebSockets and broadcasting for server push — [frontend.md](frontend.md#sse).
- **GraphQL**: `contrib/graphql`, a stdlib schema-first server with typed resolvers, a batching loader over the ORM, limits and persisted queries — [graphql.md](graphql.md).
- **gRPC**, gRPC-Web and Connect on connect-go: [gRPC](#grpc).
- **API reference UI**: `apidocs.Serve(app, r, "/docs", apidocs.Config{Theme: "purple"})` renders the document with [Scalar](https://scalar.com) ([details](#api-reference-ui)).
