package listeners

import (
	"context"

	"example.com/blog/app/events"
)

// NotifyCustomer handles events.OrderShipped. Name the parameters (ctx, e) with
// the first one the body uses.
func NotifyCustomer(_ context.Context, _ events.OrderShipped) error {
	return nil
}
