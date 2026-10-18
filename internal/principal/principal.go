// Package principal carries the identity a guard authenticated for one
// request. It exists so packages below auth can read that identity without
// importing it: ratelimit.ByUser needs the account id and nothing else,
// and auth pulls in orm, schema and database, which a rate limiter has no
// business requiring.
//
// The context key lives here, so auth remains the only writer.
package principal

import "context"

type key struct{}

// value is the identity, kept as plain strings: whatever the guard
// authenticated, this is all a keyer or an audit line needs.
type value struct{ guard, id string }

// With records the identity guard authenticated for this request. An empty
// id clears it, which is what a logout does.
func With(ctx context.Context, guard, id string) context.Context {
	return context.WithValue(ctx, key{}, value{guard: guard, id: id})
}

// From returns the guard that authenticated this request and the user's
// id. ok is false when nothing authenticated it.
func From(ctx context.Context) (guard, id string, ok bool) {
	v, _ := ctx.Value(key{}).(value)

	return v.guard, v.id, v.id != ""
}
