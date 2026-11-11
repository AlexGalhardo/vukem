package graphql

import (
	"context"
	"sync"
	"time"
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
	// lastProgress is when a goroutine last started, finished or called
	// a loader; the loaders' fallback timer uses it to tell a stalled
	// operation from a slow one.
	lastProgress time.Time
	dirty        []flusher
	// ctx is the operation's context (scope attached). Batches run with it,
	// never with the context of the resolver that happened to trigger them.
	ctx     context.Context //nolint:containedctx // request-scoped: the scope lives exactly as long as the operation context
	loaders map[any]any     // *Loader[K,V] → *loaderState[K,V]
}

// flusher is a loader state with keys waiting for a batch.
type flusher interface {
	// flushLocked starts the pending batches; called with scope.mu held.
	flushLocked(s *requestScope)
}

type scopeKey struct{}

func withScope(ctx context.Context, s *requestScope) context.Context {
	return context.WithValue(ctx, scopeKey{}, s)
}

func scopeFrom(ctx context.Context) *requestScope {
	s, _ := ctx.Value(scopeKey{}).(*requestScope)
	return s
}

// newScope returns the scope of an operation running under ctx; the scope's
// own context carries the scope, so loader batches can find it.
func newScope(ctx context.Context) *requestScope {
	s := &requestScope{running: 1, loaders: map[any]any{}}
	s.ctx = withScope(ctx, s)

	return s
}

// begin counts a goroutine about to start work.
func (s *requestScope) begin() {
	s.mu.Lock()
	s.running++
	s.lastProgress = time.Now()
	s.mu.Unlock()
}

// end uncounts a finished (or blocking) goroutine and dispatches pending
// batches when nothing else can make progress.
func (s *requestScope) end() {
	s.mu.Lock()
	s.running--
	s.dispatchLocked()
	s.mu.Unlock()
}

// wait blocks on wg without holding back the loaders.
func (s *requestScope) wait(wg *sync.WaitGroup) {
	s.end()
	wg.Wait()
	s.begin()
}

func (s *requestScope) dispatchLocked() {
	s.lastProgress = time.Now()

	if s.running > 0 || len(s.dirty) == 0 {
		return
	}

	d := s.dirty
	s.dirty = nil

	for _, f := range d {
		f.flushLocked(s)
	}
}
