// Package graphql is a schema-first GraphQL server for Vukem, written on
// the standard library (spec: October 2021 edition and later drafts).
//
// The schema is SDL; resolvers are typed Go functions bound to its fields:
//
//	schema, err := graphql.New(sdl,
//		graphql.Query("user", func(ctx context.Context, args struct{ ID string `json:"id"` }) (*User, error) {
//			return users.Find(ctx, args.ID)
//		}),
//		graphql.Field("User", "posts", func(ctx context.Context, u *User, _ graphql.NoArgs) ([]*Post, error) {
//			return postsByUser.Load(ctx, u.ID)
//		}),
//	)
//
// Documents are parsed, validated and limited (depth, complexity, aliases,
// tokens) before execution. Query fields with resolvers run concurrently,
// mutation root fields run serially, and [Loader] batches lookups made by
// sibling resolvers into one call (one SQL query for N parents).
//
// [Handler] serves GraphQL over HTTP (POST application/json, GET for
// queries) with automatic persisted queries or an allowlist; [Serve]
// mounts it on a Vukem router with an explorer page that is off in
// production.
//
// Subscriptions bind a root field to a channel of events ([Subscription],
// [FromBroadcast] for broadcast channels) and are served by [ServeWS]
// (graphql-ws protocol over any WebSocket connection, e.g.
// drivers/websocket) or by [Schema.Handler] as server-sent events.
package graphql
