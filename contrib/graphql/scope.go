package graphql

import (
	"context"
	"sync"
)

// requestScope is the per-operation state shared by the executor and the
// loaders: a count of goroutines that are doing work (running), and the
// loader batches waiting to be dispatched. When every running goroutine
// is blocked on a loader (running drops to 0), the pending batches are
// dispatched together: this is the "resolution tick" that turns N sibling
// loads into one batch without timers.
type requestScope struct {
	mu      sync.Mutex
	running int
	dirty   []flusher
	loaders map[any]any // *Loader[K,V] → *loaderState[K,V]
}

// flusher is a loader state with keys waiting for a batch.
type flusher interface {
	// flushLocked starts the pending batches; called with scope.mu held.
	flushLocked(ctx context.Context, s *requestScope)
}

type scopeKey struct{}

func withScope(ctx context.Context, s *requestScope) context.Context {
	return context.WithValue(ctx, scopeKey{}, s)
}

func newScope() *requestScope {
	return &requestScope{running: 1, loaders: map[any]any{}}
}

// begin counts a goroutine about to start work.
func (s *requestScope) begin() {
	s.mu.Lock()
	s.running++
	s.mu.Unlock()
}

// end uncounts a finished (or blocking) goroutine and dispatches pending
// batches when nothing else can make progress.
func (s *requestScope) end(ctx context.Context) {
	s.mu.Lock()
	s.running--
	s.dispatchLocked(ctx)
	s.mu.Unlock()
}

// wait blocks on wg without holding back the loaders.
func (s *requestScope) wait(ctx context.Context, wg *sync.WaitGroup) {
	s.end(ctx)
	wg.Wait()
	s.begin()
}

func (s *requestScope) dispatchLocked(ctx context.Context) {
	if s.running > 0 || len(s.dirty) == 0 {
		return
	}

	d := s.dirty
	s.dirty = nil

	for _, f := range d {
		f.flushLocked(ctx, s)
	}
}
