package listeners

import (
	"context"

	"example.com/blog/app/events"
)

// NotifyCustomer handles events.OrderShipped.
func NotifyCustomer(ctx context.Context, e events.OrderShipped) error {
	return nil
}
