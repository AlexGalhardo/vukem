# GraphQL

`contrib/graphql` (module `github.com/AlexGalhardo/vukem/contrib/graphql`) is a schema-first GraphQL server written on the standard library: its own lexer, parser, validator and executor following the GraphQL specification (October 2021 edition), no gqlgen or graphql-go. It imports core (`errs`, `orm`, `z`, `cache`, `router`); core never imports it.

## Schema and resolvers

The schema is SDL; resolvers are typed Go functions bound to its fields. Bindings are checked against the SDL at boot.

```go
const sdl = `
type Query { post(id: ID!): Post  posts(first: Int = 20): [Post!]! }
type Mutation { createPost(input: NewPost!): Post! }
type Post { id: ID! title: String! author: User! }
type User { id: ID! name: String! posts: [Post!]! }
input NewPost { title: String! }
`

type PostArgs struct {
	ID int64 `json:"id,string"`
}

type NewPost struct {
	Input struct {
		Title string `json:"title" validate:"required,min=3"`
	} `json:"input"`
}

schema, err := graphql.New(sdl,
	graphql.Query("post", func(ctx context.Context, a PostArgs) (Post, error) {
		return orm.QueryOf[Post](app.DB()).Find(ctx, a.ID)
	}),
	graphql.Mutation("createPost", func(ctx context.Context, a NewPost) (*Post, error) { … }),
	graphql.Field("Post", "author", func(ctx context.Context, p *Post, _ graphql.NoArgs) (*User, error) {
		return users.Load(ctx, p.UserID)
	}, graphql.Cost(2)),
)
```

- `Field[P, A, O](type, field, fn, opts...)`, `Query[A, O]`, `Mutation[A, O]`. `P` is the parent value (T and *T convert both ways); `A` is decoded from the coerced arguments through `json` tags, then checked with `validate` tags (package [`z`](validation.md)) and a `Validate() error` method; `O` is serialized per the selection set. A Go argument the field does not declare, a binding to an unknown field and a root field without a resolver are boot errors.
- Unbound fields read the parent: a `map[string]any` key, or a struct field by `json` name (`json:"-"` hides it), falling back to a case-insensitive Go name (`ID` → `id`). A nil Go slice is an empty list.
- Built-in scalars: `Int` (32-bit, out-of-range values are errors on input and output), `Float` (finite), `String`, `Boolean`, `ID` (string or integer input, string output). Custom scalars: `graphql.Scalar("DateTime", graphql.ScalarFuncs{Serialize, Parse})`; unbound custom scalars pass values through.
- Interfaces and unions resolve through `graphql.ResolveType(name, fn)`, a `GraphQLTypename() string` method, `graphql.Object[T]("Type")` mappings or a `"__typename"` map key.
- `extend type`, descriptions, `@deprecated`, `@specifiedBy` and custom directive definitions are supported.

## Execution

`schema.Execute(ctx, graphql.Request{Query, OperationName, Variables})` parses, validates (spec §5: fields, arguments, values, fragments and cycles, directives, variables and their usage, operation names, leaf/composite selections, field merging), applies limits, coerces variables and executes.

- Query fields that have a resolver run concurrently with their siblings, and items of composite lists run concurrently; output keeps document order. Mutation root fields run one after another. `graphql.WithoutConcurrency()` resolves everything serially (for resolvers sharing one `*sql.Tx`).
- Null propagation follows the spec: an error in a non-null field nulls the nearest nullable parent; `data` is `null` when a non-null root field fails and absent for request errors.
- Panics in resolvers are recovered and reported as internal errors.
- Subscriptions validate but do not execute over this transport yet (B.6.4).

## Errors

Every error carries `message`, `locations`, `path` and `extensions.code`:

| Resolver error | `code` | Message |
| --- | --- | --- |
| `errs.HTTPError` 401 / 403 / 404 / 409 / 429 | `UNAUTHENTICATED` / `FORBIDDEN` / `NOT_FOUND` / `CONFLICT` / `TOO_MANY_REQUESTS` | the client-safe message |
| `errs.HTTPError` 400 / 422, `*errs.ValidationError`, `z.Issues` | `BAD_USER_INPUT` (+ `fields`) | client-safe |
| anything else, any 5xx, leaf serialization failures | `INTERNAL_SERVER_ERROR` | `Internal server error` (the real text only with `graphql.WithDebug(true)`; logged with `graphql.WithLogger`) |
| `*graphql.Error` (`graphql.NewError(msg, ext)`) | as given | as given |

Errors implementing `Extensions() map[string]any` add their own keys. Request errors use `GRAPHQL_PARSE_FAILED`, `GRAPHQL_VALIDATION_FAILED`, `BAD_USER_INPUT` (variables) and `OPERATION_RESOLUTION_FAILURE`.

## Batching (N+1)

`graphql.NewLoader(fetch)` is a dataloader with a per-operation cache. Loads made by sibling resolvers are queued until every resolver of the operation is waiting, then sent as one batch (the "resolution tick"), so batching does not depend on a timer:

```go
users := graphql.NewLoader(graphql.ByKey(orm.QueryOf[User](db), "id", func(u *User) int64 { return u.ID }))
postsByUser := graphql.NewLoader(graphql.GroupByKey(orm.QueryOf[Post](db), "user_id", func(p *Post) int64 { return p.UserID }))
```

`ByKey` (belongs-to) and `GroupByKey` (has-many) run one `WhereIn` query per batch: 20 posts with their authors and the authors' posts cost 3 queries instead of 41 (asserted by `TestLoaderAvoidsNPlusOne`). `MaxBatch(n)` splits batches; `BatchWait(d)` (default 10ms) only bounds the wait when resolvers block on something other than a loader. `LoadMany` batches its keys; outside `Execute` a load calls `fetch` directly.

## Limits

| Limit | Default | Option |
| --- | --- | --- |
| Depth (introspection fields excluded) | 12 | `Limits.MaxDepth` |
| Complexity: field cost (`graphql.Cost(n)`, default 1); `first` / `last` / `limit` arguments multiply the cost of the selection | 1000 | `Limits.MaxComplexity` |
| Aliases | 30 | `Limits.MaxAliases` |
| Lexer tokens | 10000 | `Limits.MaxTokens` |
| POST body | 1 MiB | `HandlerConfig.MaxBodyBytes` |

`graphql.WithLimits(graphql.Limits{…})` changes them; a negative value disables one. Exceeding a limit returns `QUERY_LIMIT_EXCEEDED` before any resolver runs.

## HTTP

```go
graphql.Serve(app, r, "/graphql", schema, graphql.Config{
	HandlerConfig: graphql.HandlerConfig{Persisted: &graphql.PersistedQueries{
		Store: graphql.NewAllowlist(clientQueries...), AllowlistOnly: app.Config.IsProduction(),
	}},
})
```

- `schema.Handler(cfg)` is a plain `http.Handler`: POST `application/json` and GET (`query`, `variables`, `operationName`, `extensions` parameters) for queries only; a mutation over GET gets 405 with `Allow: POST`. Batched (array) bodies are rejected.
- Responses are `application/graphql-response+json` when the client accepts it (request errors then use 400), otherwise `application/json` with 200, per GraphQL over HTTP. Responses are `Cache-Control: no-store`.
- `graphql.Serve` mounts the endpoint (skipped by the OpenAPI document) and, outside production, an explorer page at `/graphql/explorer` (embedded, no CDN, runs under its own CSP with the request nonce). In production the explorer is not registered and `__schema` / `__type` are rejected unless `InsecureExplorerInProduction` / `InsecureIntrospectionInProduction` opt in; `__typename` always works. `DisableExplorer` turns it off everywhere.

### Persisted queries

- **APQ** (`PersistedQueries{}`): a request with `extensions.persistedQuery {version: 1, sha256Hash}` and no query gets `PERSISTED_QUERY_NOT_FOUND`; the client resends with the query, whose SHA-256 is verified before it is stored. Stores: `NewMemoryStore(n)` (bounded, default) or `NewCacheStore(app.Cache, ttl)` (shared across instances).
- **Allowlist** (`AllowlistOnly: true` with `NewAllowlist(docs...)`): only listed hashes or documents run; nothing is registered; a missing store rejects everything (fail closed).

## Known gaps

- Subscriptions (B.6.4) and incremental delivery (`@defer` / `@stream`) are not implemented.
- Field merging validation compares fields that share a response key in one selection set; it does not recurse into the merged sub-selections of fields from different branches.
- Complexity is an upper bound: `@skip` / `@include` are not evaluated when measuring.
- The explorer is a small built-in page, not the GraphiQL bundle; any GraphQL IDE can point at the endpoint in development.
