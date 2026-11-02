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
//
// Set MaxKeys to bound memory against a flood of unique keys (one per
// slug, say): adding a new key to a full buffer drops the oldest pending
// key first and passes it with its count to OnDrop. Set both fields before
// the first Add. K may be a struct ({Slug, Kind}) to keep several counts
// per item.
type Buffer[K comparable] struct {
	// MaxKeys caps the pending keys; 0 means no cap.
	MaxKeys int
	// OnDrop, when set, is called with each key MaxKeys drops (outside the
	// lock), for a metric or an alert.
	OnDrop func(key K, count int64)

	mu    sync.Mutex
	m     map[K]int64
	order []K // insertion order of m's keys, kept only with MaxKeys
}

// Add adds delta to key. A zero delta does nothing.
func (b *Buffer[K]) Add(key K, delta int64) {
	if delta == 0 {
		return
	}

	b.mu.Lock()

	if b.m == nil {
		b.m = map[K]int64{}
	}

	var (
		dropKey   K
		dropCount int64
		dropped   bool
	)

	if _, ok := b.m[key]; !ok && b.MaxKeys > 0 {
		if len(b.m) >= b.MaxKeys && len(b.order) > 0 {
			dropKey, b.order = b.order[0], b.order[1:]
			dropCount, dropped = b.m[dropKey], true
			delete(b.m, dropKey)
		}

		b.order = append(b.order, key)
	}

	b.m[key] += delta
	b.mu.Unlock()

	if dropped && b.OnDrop != nil {
		b.OnDrop(dropKey, dropCount)
	}
}

// Swap returns the pending counts and starts a new, empty set; nil when
// nothing is pending. The caller owns the returned map.
func (b *Buffer[K]) Swap() map[K]int64 {
	b.mu.Lock()
	defer b.mu.Unlock()

	m := b.m
	b.m, b.order = nil, nil

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
