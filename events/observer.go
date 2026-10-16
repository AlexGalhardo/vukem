package events

import (
	"context"
	"time"
)

// Event describes one Bus.Dispatch for an Observer. It carries the event's
// Go type only, never its fields (they may hold user data).
type Event struct {
	// Type is the event's Go type ("main.UserRegistered").
	Type string
	// Listeners is the number of listeners (sync, async and queued).
	Listeners int
	// Duration covers the synchronous listeners and enqueueing.
	Duration time.Duration
	// Err is the error Dispatch returned.
	Err error
}

// Observer is notified after every Bus.Dispatch. It runs on the caller's
// goroutine, so it must be fast and safe for concurrent use.
type Observer interface {
	ObserveEvent(ctx context.Context, e Event)
}

// SetObserver reports dispatches to obs (development tooling such as
// devtools).
func (b *Bus) SetObserver(obs Observer) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.obs = obs
}
