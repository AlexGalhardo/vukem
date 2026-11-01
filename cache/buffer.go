package cache

import (
	"context"
	"sync"
)

// Buffer adds counts in memory, such as page views, so a hot path costs no
// store write; a scheduled task writes them in one batch with Flush. The
// zero value is ready to use and it is safe for concurrent use. Counts not
// yet flushed are lost if the process dies, so flush at shutdown too
// (app.OnShutdown).
type Buffer[K comparable] struct {
	mu sync.Mutex
	m  map[K]int64
}

// Add adds delta to key. A zero delta does nothing.
func (b *Buffer[K]) Add(key K, delta int64) {
	if delta == 0 {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.m == nil {
		b.m = map[K]int64{}
	}

	b.m[key] += delta
}

// Swap returns the pending counts and starts a new, empty set; nil when
// nothing is pending. The caller owns the returned map.
func (b *Buffer[K]) Swap() map[K]int64 {
	b.mu.Lock()
	defer b.mu.Unlock()

	m := b.m
	b.m = nil

	return m
}

// Restore adds counts back, typically the ones a failed write took with
// Swap, so the next flush retries them.
func (b *Buffer[K]) Restore(counts map[K]int64) {
	for k, v := range counts {
		b.Add(k, v)
	}
}

// Flush swaps the pending counts and passes them to write, which stores
// them (one upsert per key). When write fails the counts are restored and
// its error returned. With nothing pending, write is not called.
func (b *Buffer[K]) Flush(ctx context.Context, write func(context.Context, map[K]int64) error) error {
	m := b.Swap()
	if m == nil {
		return nil
	}

	if err := write(ctx, m); err != nil {
		b.Restore(m)

		return err
	}

	return nil
}
