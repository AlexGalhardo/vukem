// Package events dispatches typed events to listeners registered by
// type. Listeners are plain functions; registration is explicit code (no
// discovery):
//
//	type UserRegistered struct{ User models.User }
//
//	bus.Register(events.On(listeners.SendWelcome)) // func(ctx, UserRegistered) error
//	err := bus.Dispatch(ctx, UserRegistered{User: u})
//
// Async listeners run on a bounded goroutine pool that Shutdown drains; a
// queue-backed mode arrives with the queue (BETA).
package events

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"slices"
	"sync"
)

// Dispatcher dispatches events (Bus and Fake implement it).
type Dispatcher interface {
	Dispatch(ctx context.Context, event any) error
}

type listener struct {
	async bool
	fn    func(ctx context.Context, event any) error
	name  string
}

// Registration binds a listener to its event type; create it with On or
// OnAsync.
type Registration struct {
	typ reflect.Type
	l   listener
}

// On registers fn for events of type E, run synchronously by Dispatch (its
// error is returned to the caller).
func On[E any](fn func(ctx context.Context, e E) error) Registration {
	return Registration{typ: reflect.TypeFor[E](), l: listener{fn: wrap(fn), name: fmt.Sprintf("%T", fn)}}
}

// OnAsync registers fn for events of type E, run in the background; its
// error is logged.
func OnAsync[E any](fn func(ctx context.Context, e E) error) Registration {
	r := On(fn)
	r.l.async = true

	return r
}

func wrap[E any](fn func(context.Context, E) error) func(context.Context, any) error {
	return func(ctx context.Context, e any) error {
		v, ok := e.(E)
		if !ok {
			return fmt.Errorf("events: listener for %T got %T", v, e)
		}

		return fn(ctx, v)
	}
}

// Bus holds listeners and runs them.
type Bus struct {
	log *slog.Logger
	sem chan struct{}
	wg  sync.WaitGroup

	mu        sync.RWMutex
	listeners map[reflect.Type][]listener
	closed    bool
}

// New returns a bus running at most workers async listeners at once
// (0 = 16).
func New(log *slog.Logger, workers int) *Bus {
	if workers <= 0 {
		workers = 16
	}

	if log == nil {
		log = slog.Default()
	}

	return &Bus{log: log, sem: make(chan struct{}, workers), listeners: map[reflect.Type][]listener{}}
}

// Register adds listeners in order.
func (b *Bus) Register(regs ...Registration) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for _, r := range regs {
		b.listeners[r.typ] = append(b.listeners[r.typ], r.l)
	}
}

// Listen registers a synchronous listener for E.
func Listen[E any](b *Bus, fn func(ctx context.Context, e E) error) { b.Register(On(fn)) }

// ListenAsync registers an async listener for E.
func ListenAsync[E any](b *Bus, fn func(ctx context.Context, e E) error) { b.Register(OnAsync(fn)) }

// ErrClosed is returned by Dispatch after Shutdown.
var ErrClosed = errors.New("events: bus is shut down")

// Dispatch runs the synchronous listeners of event's type in registration
// order (stopping at the first error) and schedules the async ones.
// Events without listeners are fine.
func (b *Bus) Dispatch(ctx context.Context, event any) error {
	b.mu.RLock()
	ls := slices.Clone(b.listeners[reflect.TypeOf(event)])
	closed := b.closed
	b.mu.RUnlock()

	if closed {
		return ErrClosed
	}

	for _, l := range ls {
		if l.async {
			b.goAsync(ctx, l, event)
			continue
		}

		if err := l.fn(ctx, event); err != nil {
			return fmt.Errorf("events: %T listener %s: %w", event, l.name, err)
		}
	}

	return nil
}

func (b *Bus) goAsync(ctx context.Context, l listener, event any) {
	ctx = context.WithoutCancel(ctx) // the request may end before the listener runs

	b.wg.Go(func() {
		b.sem <- struct{}{}
		defer func() { <-b.sem }()

		defer func() {
			if r := recover(); r != nil {
				b.log.ErrorContext(
					ctx,
					"async listener panicked",
					"event",
					fmt.Sprintf("%T", event),
					"listener",
					l.name,
					"panic",
					r,
				)
			}
		}()

		if err := l.fn(ctx, event); err != nil {
			b.log.ErrorContext(
				ctx,
				"async listener failed",
				"event",
				fmt.Sprintf("%T", event),
				"listener",
				l.name,
				"err",
				err,
			)
		}
	})
}

// Shutdown stops accepting events and waits for async listeners until ctx
// is done.
func (b *Bus) Shutdown(ctx context.Context) error {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()

	done := make(chan struct{})

	go func() {
		b.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("events: async listeners still running: %w", ctx.Err())
	}
}

// Fake records dispatched events instead of running listeners (tests).
type Fake struct {
	mu     sync.Mutex
	events []any
}

// NewFake returns an empty Fake.
func NewFake() *Fake { return &Fake{} }

// Dispatch implements Dispatcher.
func (f *Fake) Dispatch(_ context.Context, event any) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.events = append(f.events, event)

	return nil
}

// Dispatched returns the recorded events of type E.
func Dispatched[E any](f *Fake) []E {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []E

	for _, e := range f.events {
		if v, ok := e.(E); ok {
			out = append(out, v)
		}
	}

	return out
}
