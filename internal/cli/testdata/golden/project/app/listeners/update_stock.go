package listeners

import (
	"context"

	"example.com/blog/app/events"
)

// UpdateStock handles events.OrderShipped. Name the parameters (ctx, e) with
// the first one the body uses.
func UpdateStock(_ context.Context, _ events.OrderShipped) error {
	return nil
}
