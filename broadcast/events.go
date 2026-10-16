package broadcast

import (
	"context"

	"github.com/AlexGalhardo/vukem/events"
	"github.com/AlexGalhardo/vukem/queue"
)

// Listener broadcasts every dispatched E synchronously: Dispatch returns
// the publish error.
//
//	bus.Register(broadcast.Listener[PostPublished](b))
func Listener[E Event](s *Server) events.Registration {
	return events.On(func(ctx context.Context, e E) error { return s.Broadcast(ctx, e) })
}

// ListenerAsync broadcasts every dispatched E in the background; failures
// are logged by the bus.
func ListenerAsync[E Event](s *Server) events.Registration {
	return events.OnAsync(func(ctx context.Context, e E) error { return s.Broadcast(ctx, e) })
}

// Queued broadcasts every dispatched E from a queue job of kind, so the
// publish survives restarts and is retried (Laravel's ShouldBroadcast). E
// must round-trip through JSON; register the bus's Jobs on the queue.
func Queued[E Event](s *Server, kind string, opts ...queue.Option) events.Registration {
	return events.OnQueued(kind, func(ctx context.Context, e E) error { return s.Broadcast(ctx, e) }, opts...)
}
