package graphql

import (
	"context"
	"fmt"
	"time"

	"github.com/AlexGalhardo/vukem/orm"
)

// BatchFunc loads many keys in one call. Keys missing from the result
// resolve to the zero V (nil for pointers) without an error; an error
// fails every key of the batch.
type BatchFunc[K comparable, V any] func(ctx context.Context, keys []K) (map[K]V, error)

// Loader batches and caches lookups within one operation (the dataloader
// pattern). Define it once, next to the schema; each Execute gets its own
// cache. Loads made by sibling resolvers are collected until every
// resolver of the operation is waiting, then sent as one batch, so a
// list of N posts asking for their author costs one query, not N.
//
// Outside an Execute (no request scope in ctx) Load calls fetch directly.
type Loader[K comparable, V any] struct {
	fetch    BatchFunc[K, V]
	maxBatch int
	wait     time.Duration
}

// LoaderOption tunes a Loader.
type LoaderOption func(*loaderConfig)

type loaderConfig struct {
	maxBatch int
	wait     time.Duration
}

// MaxBatch splits batches larger than n keys (default: unlimited).
func MaxBatch(n int) LoaderOption { return func(c *loaderConfig) { c.maxBatch = n } }

// BatchWait is the fallback for when the resolution tick never comes:
// a pending batch is sent anyway once the operation has made no progress
// (no resolver started, finished or called a loader) for d (default 10ms).
// It only matters when resolvers block on something other than a loader
// (a mutex, a channel, their own goroutines); slow siblings that keep
// making progress, as on a loaded machine, do not split the batch.
// 0 disables the fallback.
func BatchWait(d time.Duration) LoaderOption { return func(c *loaderConfig) { c.wait = d } }

// DefaultBatchWait is the default BatchWait.
const DefaultBatchWait = 10 * time.Millisecond

// NewLoader returns a loader over fetch.
func NewLoader[K comparable, V any](fetch BatchFunc[K, V], opts ...LoaderOption) *Loader[K, V] {
	c := loaderConfig{wait: DefaultBatchWait}
	for _, o := range opts {
		o(&c)
	}

	return &Loader[K, V]{fetch: fetch, maxBatch: c.maxBatch, wait: c.wait}
}

type entry[V any] struct {
	v       V
	err     error
	done    bool
	waiters int
	ch      chan struct{}
}

// loaderState is a Loader's cache and pending keys for one operation.
type loaderState[K comparable, V any] struct {
	l       *Loader[K, V]
	cache   map[K]*entry[V]
	pending []K
	dirty   bool
	timer   *time.Timer
}

// Load returns the value for key, batching it with sibling loads.
func (l *Loader[K, V]) Load(ctx context.Context, key K) (V, error) {
	sc := scopeFrom(ctx)
	if sc == nil {
		m, err := l.call(ctx, []K{key})
		return m[key], err
	}

	sc.mu.Lock()

	st := l.state(sc)

	en := st.cache[key]
	if en == nil {
		en = &entry[V]{ch: make(chan struct{})}
		st.cache[key] = en
		st.add(ctx, sc, key)
	}

	if en.done {
		sc.mu.Unlock()
		return en.v, en.err
	}

	en.waiters++
	sc.running--
	sc.dispatchLocked(ctx)
	sc.mu.Unlock()

	<-en.ch

	return en.v, en.err
}

// LoadMany loads keys in one batch; values follow the order of keys.
func (l *Loader[K, V]) LoadMany(ctx context.Context, keys []K) ([]V, error) {
	sc := scopeFrom(ctx)
	if sc == nil {
		m, err := l.call(ctx, keys)
		if err != nil {
			return nil, err
		}

		out := make([]V, len(keys))
		for i, k := range keys {
			out[i] = m[k]
		}

		return out, nil
	}

	// Queue every key before blocking so they share one batch.
	sc.mu.Lock()

	st := l.state(sc)
	for _, k := range keys {
		if st.cache[k] == nil {
			st.cache[k] = &entry[V]{ch: make(chan struct{})}
			st.add(ctx, sc, k)
		}
	}

	sc.mu.Unlock()

	out := make([]V, len(keys))

	for i, k := range keys {
		v, err := l.Load(ctx, k)
		if err != nil {
			return nil, err
		}

		out[i] = v
	}

	return out, nil
}

func (l *Loader[K, V]) state(sc *requestScope) *loaderState[K, V] {
	if st, ok := sc.loaders[l].(*loaderState[K, V]); ok {
		return st
	}

	st := &loaderState[K, V]{l: l, cache: map[K]*entry[V]{}}
	sc.loaders[l] = st

	return st
}

func (l *Loader[K, V]) call(ctx context.Context, keys []K) (m map[K]V, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("graphql: loader batch panicked: %v", r)
		}
	}()

	return l.fetch(ctx, keys)
}

// add queues key; called with sc.mu held.
func (st *loaderState[K, V]) add(ctx context.Context, sc *requestScope, key K) {
	st.pending = append(st.pending, key)

	if !st.dirty {
		st.dirty = true
		sc.dirty = append(sc.dirty, st)

		if st.l.wait > 0 {
			st.armLocked(ctx, sc, st.l.wait)
		}
	}

	if st.l.maxBatch > 0 && len(st.pending) >= st.l.maxBatch {
		st.flushLocked(ctx, sc)
	}
}

// armLocked starts the fallback timer; called with sc.mu held. When it
// fires it flushes only if the scope made no progress for a whole wait,
// otherwise it re-arms for the rest of it: a timer measured from the
// first key would split batches whenever siblings are merely slow to be
// scheduled.
func (st *loaderState[K, V]) armLocked(ctx context.Context, sc *requestScope, d time.Duration) {
	var t *time.Timer

	t = time.AfterFunc(d, func() {
		sc.mu.Lock()
		defer sc.mu.Unlock()

		if st.timer != t { // flushed (and maybe re-armed) meanwhile
			return
		}

		if idle := time.Since(sc.lastProgress); idle < st.l.wait {
			st.armLocked(ctx, sc, st.l.wait-idle)
			return
		}

		st.flushLocked(ctx, sc)
	})
	st.timer = t
}

// flushLocked starts batches for the pending keys; idempotent.
func (st *loaderState[K, V]) flushLocked(ctx context.Context, sc *requestScope) {
	if !st.dirty {
		return
	}

	keys := st.pending
	st.pending = nil
	st.dirty = false

	if st.timer != nil {
		st.timer.Stop()
		st.timer = nil
	}

	size := len(keys)
	if st.l.maxBatch > 0 {
		size = st.l.maxBatch
	}

	for len(keys) > 0 {
		chunk := keys[:min(size, len(keys))]
		keys = keys[len(chunk):]

		sc.running++

		go st.run(ctx, sc, chunk)
	}
}

func (st *loaderState[K, V]) run(ctx context.Context, sc *requestScope, keys []K) {
	m, err := st.l.call(ctx, keys)

	sc.mu.Lock()
	defer sc.mu.Unlock()

	for _, k := range keys {
		en := st.cache[k]
		en.v, en.err, en.done = m[k], err, true
		sc.running += en.waiters
		en.waiters = 0
		close(en.ch)
	}

	sc.running--
	sc.dispatchLocked(ctx)
}

// ByKey is a BatchFunc that loads one T per key with a single
// q.WhereIn(col, keys...) query (belongs-to / has-one). key extracts the
// column value from a loaded row.
//
//	users := graphql.NewLoader(graphql.ByKey(orm.QueryOf[User](db), "id", func(u *User) int64 { return u.ID }))
func ByKey[T any, K comparable](q orm.Query[T], col string, key func(*T) K) BatchFunc[K, *T] {
	return func(ctx context.Context, keys []K) (map[K]*T, error) {
		rows, err := q.WhereIn(col, anySlice(keys)...).Get(ctx)
		if err != nil {
			return nil, fmt.Errorf("graphql: load %s by %s: %w", orm.TableOf[T](), col, err)
		}

		out := make(map[K]*T, len(rows))
		for i := range rows {
			out[key(&rows[i])] = &rows[i]
		}

		return out, nil
	}
}

// GroupByKey is a BatchFunc that loads every T per key with a single
// q.WhereIn(col, keys...) query (has-many). Keys without rows get an
// empty slice.
//
//	postsByUser := graphql.NewLoader(graphql.GroupByKey(orm.QueryOf[Post](db), "user_id", func(p *Post) int64 { return p.UserID }))
func GroupByKey[T any, K comparable](q orm.Query[T], col string, key func(*T) K) BatchFunc[K, []*T] {
	return func(ctx context.Context, keys []K) (map[K][]*T, error) {
		rows, err := q.WhereIn(col, anySlice(keys)...).Get(ctx)
		if err != nil {
			return nil, fmt.Errorf("graphql: load %s by %s: %w", orm.TableOf[T](), col, err)
		}

		out := make(map[K][]*T, len(keys))
		for _, k := range keys {
			out[k] = []*T{}
		}

		for i := range rows {
			k := key(&rows[i])
			out[k] = append(out[k], &rows[i])
		}

		return out, nil
	}
}

func anySlice[K any](keys []K) []any {
	out := make([]any, len(keys))
	for i, k := range keys {
		out[i] = k
	}

	return out
}
