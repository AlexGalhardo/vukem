# API starter

A JSON-only skeleton: no views, htmx, sessions or CSRF; opaque access tokens, `/api/v1` routes, CORS, rate limiting and problem+json errors, on SQLite. Owning docs: [api.md](../api.md).

## Create

```sh
vukem new shop --api               # or: vukem new shop --api --module github.com/you/shop
cd shop
vukem migrate
```

The API starter has no frontend, so `vukem new` skips `npm install`. It still runs `go mod tidy`, generates `APP_KEY` and runs `git init`.

## Run

```sh
vukem serve        # http://localhost:3000
```

`vukem serve` builds and runs `cmd/app` and restarts it when a `.go` file or `.env` changes. Try it:

```sh
curl -s localhost:3000/api/v1/posts
curl -s -X POST localhost:3000/api/v1/tokens -H 'Content-Type: application/json' \
  -d '{"email":"you@example.com","password":"correct horse battery","device_name":"cli"}'
curl -s localhost:3000/api/v1/me -H "Authorization: Bearer $TOKEN"
```

Create the first user from a seeder (`vukem make:seeder Users`, then `vukem db:seed`) or from a test helper like the starter's `apiUser`.

## Project layout

```text
cmd/app/main.go                    the app binary
bootstrap/app.go                   vukem.New with options (no views, no Vite)
routes/base.go                     vukem.APIMiddleware: recover, request id, logging, security headers, body limit
routes/api.go                      POST /api/v1/tokens, GET /api/v1/me, DELETE /api/v1/tokens/current, CORS
routes/posts.go                    the example /api/v1/posts resource
routes/registry.go                 the route files, in order (Base, API, Posts)
app/accounts/                      guards, user lookup, token issuance (shared with install:auth)
app/controllers/apiv1/             tokens.go, posts.go
app/models/                        user.go, post.go
app/resources/                     JSON transformers (wire format of the models)
app/requests/                      z schemas for input
database/migrations/               users, auth tables (tokens), posts
tests/                             api_tokens_test.go, posts_test.go
```

## Add a route and a resource

```sh
vukem make:model Product -m -f       # model, create_products_table migration, factory
vukem make:controller Product --api  # Index, Store, Show, Update, Destroy returning JSON
vukem make:resource Product          # app/resources/product.go: the JSON wire format
vukem migrate
```

Register it in a route file (add the function to `routes/registry.go`). Behind a token, all five actions at once:

```go
r.Group("/api/v1", func(g *router.Group) {
	g.Use(middleware.JSONOnly(), auth.Required(acc.API))
	g.APIResource("products", controllers.NewProductController()) // index, store, show, update, destroy
}).Name("api.v1.")
```

For public reads and token-protected writes, register the routes one by one as `routes/posts.go` does.

- List endpoints follow the query conventions: `?filter[title]=`, `?sort=title,-id`, `?page`/`?per_page`, with allowlisted fields ([api.md](../api.md#query-conventions)).
- Typed handlers registered with `api.Register` validate input from struct tags and describe themselves in the OpenAPI document ([api.md](../api.md#openapi)).
- Migrations work as in every starter: `vukem make:migration add_sku_to_products_table`, `vukem migrate:status`, `migrate:rollback` ([database.md](../database.md#migrations)).

## Authentication

The API starter already includes the authentication core (users, `accounts`, password hashing) and opaque access tokens: `POST /api/v1/tokens` takes email, password and device name and answers `201 {token, type, expires_at}` (5 attempts per minute per IP); `auth.Required(acc.API)` protects routes; `DELETE /api/v1/tokens/current` revokes. See [authentication.md](../authentication.md#token-guard-api) and [api.md](../api.md#authentication).

- `vukem install:auth` (with `--2fa`, `--social`) generates browser flows (HTML pages, sessions); it targets the web starters, not this one. Use the [HTMX](htmx.md#authentication) or [Inertia](inertia.md#authentication) starter, with `vukem install:api` for its JSON side, when the same app needs both.
- For stateless tokens, the `auth/jwt` package provides a JWT guard with rotating refresh tokens ([authentication.md](../authentication.md#jwt-guard-authjwt)).
- Browser clients on another origin need `CORS_ALLOWED_ORIGINS` (deny by default).

## OpenAPI and clients

```sh
vukem openapi:export > openapi.json        # OpenAPI 3.1 document of the routes
vukem codegen:client -lang ts              # typed TypeScript client (resources/js/api.ts)
vukem codegen:client -lang go              # stdlib Go client (internal/apiclient/client.go)
```

`vukem.ServeOpenAPI(app, r, "/openapi.json")` and `apidocs.Serve(app, r, "/docs", apidocs.Config{})` serve the document and a Scalar API reference outside production ([api.md](../api.md#api-reference-ui)).

## Tests

```sh
go test ./...
```

```go
app := vukemtest.New(t, bootstrap.New)
token := apiUser(t, app, "writer@example.com")

app.Client(t).WithToken(token).
	PostJSON("/api/v1/posts", map[string]any{"title": "Hello API", "body": "First."}).
	AssertCreated().
	AssertJSONPath("title", "Hello API")
app.Client(t).GetJSON("/api/v1/posts/99").AssertProblem(404)
app.Client(t).PostJSON("/api/v1/posts", map[string]any{"title": "Nope"}).AssertUnauthorized()
```

`AssertMatchesOpenAPI()` checks a response against the generated document ([api.md](../api.md#testing)).

## Build and deploy

```sh
vukem build        # go build -trimpath -ldflags="-s -w" -o bin/app ./cmd/app (no Vite step)
```

```sh
APP_ENV=production APP_DEBUG=false APP_URL=https://api.example.com ./bin/app migrate
APP_ENV=production APP_DEBUG=false APP_URL=https://api.example.com ./bin/app
```

Set a unique `APP_KEY`, `DATABASE_URL` and `CORS_ALLOWED_ORIGINS`; terminate TLS at a reverse proxy. The OpenAPI document and API reference are not served in production unless `OPENAPI_IN_PRODUCTION=true`, or per route with `vukem.ServeOpenAPIWith(..., vukem.OpenAPIOptions{InProduction: true})` and `apidocs.Config{InProduction: true}` for a public API. More in the [HTMX guide](htmx.md#build-and-deploy) and [security.md](../security.md).
