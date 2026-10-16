package devtools

import "sync"

// ring is a bounded, concurrency-safe buffer that keeps the newest values.
type ring[T any] struct {
	mu   sync.Mutex
	buf  []T
	next int
	n    int
}

func newRing[T any](size int) *ring[T] { return &ring[T]{buf: make([]T, size)} }

func (r *ring[T]) push(v T) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.buf[r.next] = v
	r.next = (r.next + 1) % len(r.buf)
	r.n = min(r.n+1, len(r.buf))
}

// items returns a copy, newest first.
func (r *ring[T]) items() []T {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]T, 0, r.n)
	for i := range r.n {
		out = append(out, r.buf[(r.next-1-i+len(r.buf))%len(r.buf)])
	}

	return out
}

func (r *ring[T]) len() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.n
}
