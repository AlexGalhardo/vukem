package listeners

import (
	"context"

	"example.com/blog/app/events"
)

// UpdateStock handles events.OrderShipped.
func UpdateStock(ctx context.Context, e events.OrderShipped) error {
	return nil
}
