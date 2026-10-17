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
- Unbound fields read the parent: a `map[string]any` key, or a struct field by `json` name (`json:"-"` hides it), falling back to a case-insensitive Go name, tagged or not (`ID` → `id`, ``PriceCents `json:"price_cents"` `` → `priceCents`). An exact `json` name wins over another field's Go name. Folding makes a json-renamed field reachable by its Go name too (`Password json:"pw"` answers `password`) whenever the schema declares that field: what clients can read is decided by the SDL, so keep secrets out of it or hide them with `json:"-"`. A nil Go slice is an empty list.
- Built-in scalars: `Int` (32-bit, out-of-range values are errors on input and output), `Float` (finite), `String`, `Boolean`, `ID` (string or integer input, string output). Custom scalars: `graphql.Scalar("DateTime", graphql.ScalarFuncs{Serialize, Parse})`; unbound custom scalars pass values through.
- Interfaces and unions resolve through `graphql.ResolveType(name, fn)`, a `GraphQLTypename() string` method, `graphql.Object[T]("Type")` mappings or a `"__typename"` map key.
- `extend type`, descriptions, `@deprecated`, `@specifiedBy` and custom directive definitions are supported.

## Execution

`schema.Execute(ctx, graphql.Request{Query, OperationName, Variables})` parses, validates (spec §5: fields, arguments, values, fragments and cycles, directives, variables and their usage, operation names, leaf/composite selections, field merging), applies limits, coerces variables and executes.

- Query fields that have a resolver run concurrently with their siblings, and items of composite lists run concurrently; output keeps document order. Mutation root fields run one after another. `graphql.WithoutConcurrency()` resolves everything serially (for resolvers sharing one `*sql.Tx`).
- Null propagation follows the spec: an error in a non-null field nulls the nearest nullable parent; `data` is `null` when a non-null root field fails and absent for request errors.
- Panics in resolvers are recovered and reported as internal errors.
- Subscription operations run through `schema.Subscribe`, `graphql.ServeWS` or the handler's SSE mode ([Subscriptions](#subscriptions)); `Execute` and plain JSON POST reject them with `BAD_REQUEST`.

## Subscriptions

A subscription root field is bound to a source stream: a resolver that returns a channel. Each value received is the field's value for that event and is completed against the selection set (spec §6.2.3 `CreateSourceEventStream` / `ExecuteSubscriptionEvent`), so the client gets one response per event.

```go
// SDL: type Subscription { postCreated: Post!  countdown(from: Int!): Int! }
graphql.Subscription("countdown", func(ctx context.Context, a CountArgs) (<-chan int, error) {
	ch := make(chan int)
	go func() {
		defer close(ch) // closing the channel completes the subscription
		for i := a.From; i > 0; i-- {
			select {
			case ch <- i:
			case <-ctx.Done(): // the client unsubscribed or left
				return
			}
		}
	}()
	return ch, nil
}),
graphql.Subscription("postCreated", graphql.FromBroadcast[graphql.NoArgs, Post](b,
	func(context.Context, graphql.NoArgs) ([]string, error) { return []string{"posts"}, nil },
	"post.created")), // optional event-name filter
```

- `Subscription[A, O](field, fn, opts...)` decodes and validates `A` like `Query`. The resolver owns the channel: it closes it to complete and must stop sending once `ctx` is done. An error from `fn` is a field error of the single response (`next` with `errors`, then `complete`). Every field of the subscription root needs a `Subscription` binding (boot error otherwise).
- `FromBroadcast[A, O](b, channels, events...)` subscribes to [broadcast](frontend.md#broadcasting) channels with `b.Subscribe` in the operation's context, so the public / private / presence rules apply and **fail closed**: a denied channel fails the subscription with `FORBIDDEN`. Message data is decoded from JSON into `O` (`broadcast.Message` passes messages through); presence notifications are skipped unless named in `events`; data that does not decode is dropped. A mutation that calls `b.Publish` / `b.Broadcast` reaches every subscriber, across instances with a broker.
- `schema.Subscribe(ctx, req)` returns an `iter.Seq[*Response]` (or the error response when the request fails before the stream starts); queries and mutations yield one response. Cancel `ctx` to stop it.

### WebSocket transport (graphql-ws)

`graphql.ServeWS(schema, conn, graphql.WSConfig{…})` speaks the [`graphql-transport-ws`](https://github.com/enisdenjo/graphql-ws/blob/master/PROTOCOL.md) protocol, compatible with the `graphql-ws` client (Apollo Client `GraphQLWsLink`, urql, Relay):

```go
r.Get("/graphql/ws", websocket.Handler(func(c *websocket.Conn) error {
	return graphql.ServeWS(schema, c, graphql.WSConfig{
		OnConnect: func(ctx context.Context, payload map[string]any) (context.Context, error) {
			return auth.FromToken(ctx, payload["token"]) // error → close 4403
		},
		Persisted: persisted, // the HandlerConfig value, so the allowlist covers both
	})
}, websocket.Subprotocols(graphql.WSProtocol)))
```

`contrib/graphql` does not depend on a WebSocket library: `ServeWS` takes a small `WSConn[C]` interface (`Context`, `Subprotocol`, `ReadText`, `WriteText`, `Close(code C, reason)`), generic over the close-code type so [`drivers/websocket`](frontend.md#websockets)'s `*Conn` satisfies it without an adapter and `C` is inferred. The driver keeps doing the upgrade work: same-origin check, 32 KiB read limit, ping/pong keepalive, write timeouts and shutdown.

| Message / event | Behaviour |
| --- | --- |
| `connection_init` | must arrive within `InitTimeout` (10s) or the socket closes with 4408; `OnConnect(ctx, payload)` returns the context operations run in (nil hook: the upgrade request's context); an error closes with 4403; a second init closes with 4429 |
| `ping` / `pong` | `ping` is answered with `pong` at any time; `pong` is ignored |
| `subscribe` | before `connection_ack` closes with 4401; an id already running closes with 4409; parse, validation, variable, limit and persisted-query errors answer `error` with the GraphQL errors; more than `MaxSubscriptions` (32) running operations answer `error` with `TOO_MANY_REQUESTS` |
| `next` / `complete` | one `next` per event, `complete` when the source ends; queries and mutations get one `next` and `complete` |
| client `complete` | stops the operation (its context is cancelled; nothing more is sent for the id, which can be reused) |
| invalid JSON, unknown type, missing id | close 4400; wrong subprotocol closes with 4406; an unencodable response closes with 4500 |

Introspection is rejected over the socket unless `WSConfig.Introspection` is set.

### SSE transport (graphql-sse)

`schema.Handler` (and `graphql.Serve`) answer a POST that accepts `text/event-stream` in the graphql-sse "distinct connections" mode: one `next` event per result (`data` is the response JSON), then a `complete` event. Request errors are a JSON response with 400 before the stream opens. Subscriptions over GET and the "single connection" mode are not supported.

```js
import { createClient } from 'graphql-ws'
const client = createClient({ url: `wss://${location.host}/graphql/ws`, connectionParams: { token } })
client.subscribe({ query: 'subscription { postCreated { id title } }' }, { next: console.log, error: console.error, complete: () => {} })
```

## Errors

Every error carries `message`, `locations`, `path` and `extensions.code`:

| Resolver error | `code` | Message |
| --- | --- | --- |
| `errs.HTTPError` 401 / 403 / 404 / 409 / 429 | `UNAUTHENTICATED` / `FORBIDDEN` / `NOT_FOUND` / `CONFLICT` / `TOO_MANY_REQUESTS` | the client-safe message; keyed errors (`errs.NewKey`) are translated by the request ctx's `errs.Translator` (`HTTPError.LocalizedText`) |
| `errs.HTTPError` 400 / 422, `*errs.ValidationError`, `z.Issues` | `BAD_USER_INPUT` (+ `fields`) | client-safe |
| anything else, any 5xx, leaf serialization failures | `INTERNAL_SERVER_ERROR` | `Internal server error` (the real text only with `graphql.WithDebug(true)`; logged with `graphql.WithLogger`) |
| `*graphql.Error` (`graphql.NewError(msg, ext)`) | as given | as given |

Errors implementing `Extensions() map[string]any` add their own keys. A `*graphql.Error` and an `Extender`'s keys reach clients as written, whatever their status: they are the app's explicit choice, so never put internals in them. Request errors use `GRAPHQL_PARSE_FAILED`, `GRAPHQL_VALIDATION_FAILED`, `BAD_USER_INPUT` (variables) and `OPERATION_RESOLUTION_FAILURE`.

## Batching (N+1)

`graphql.NewLoader(fetch)` is a dataloader with a per-operation cache. Loads made by sibling resolvers are queued until every resolver of the operation is waiting, then sent as one batch (the "resolution tick"), so batching does not depend on a timer:

```go
users := graphql.NewLoader(graphql.ByKey(orm.QueryOf[User](db), "id", func(u *User) int64 { return u.ID }))
postsByUser := graphql.NewLoader(graphql.GroupByKey(orm.QueryOf[Post](db), "user_id", func(p *Post) int64 { return p.UserID }))
```

`ByKey` (belongs-to) and `GroupByKey` (has-many) run one `WhereIn` query per batch: 20 posts with their authors and the authors' posts cost 3 queries instead of 41 (asserted by `TestLoaderAvoidsNPlusOne`). `MaxBatch(n)` splits batches; `BatchWait(d)` (default 10ms) is the fallback when resolvers block on something other than a loader (a mutex, a channel): a pending batch is sent once the operation made no progress for `d` (or at the latest `10×d` after its first key), so slow siblings that keep reaching their loads on a busy machine still share one batch; `BatchWait(0)` disables it. `LoadMany` batches its keys; outside `Execute` a load calls `fetch` directly.

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

- Incremental delivery (`@defer` / `@stream`) is not implemented; subscriptions support the graphql-ws protocol and graphql-sse distinct connections only.
- Field merging validation compares fields that share a response key in one selection set; it does not recurse into the merged sub-selections of fields from different branches.
- Complexity is an upper bound: `@skip` / `@include` are not evaluated when measuring.
- The explorer is a small built-in page, not the GraphiQL bundle; any GraphQL IDE can point at the endpoint in development.
