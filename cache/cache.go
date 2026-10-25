// Package cache stores values with a TTL behind a Store (memory, database;
// Redis in drivers/redis). Values are JSON-encoded, so typed reads use the
// generic functions:
//
//	user, err := cache.Remember(ctx, app.Cache, "user:42", time.Hour, func(ctx context.Context) (User, error) {
//		return orm.QueryOf[User](db).Find(ctx, 42)
//	})
//
// [Cache] wraps a [Store] with a key prefix and adds Put, Forget,
// Increment and distributed locks ([Cache.NewLock]); [Get], [Pull], [Add]
// and [Remember] encode and decode typed values. Apps get one from app.Cache(),
// configured by CACHE_STORE.
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/subsystems.md#cache.
package cache

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Store is a cache backend. Keys arrive already prefixed.
type Store interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	// Add sets key only when it is absent (or expired) and reports whether
	// it did; locks rely on it being atomic.
	Add(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error)
	Delete(ctx context.Context, keys ...string) error
	// DeleteIf deletes key only when it holds value (lock release).
	DeleteIf(ctx context.Context, key string, value []byte) (bool, error)
	// Increment adds delta to an integer value (missing keys start at 0
	// with ttl) and returns the new value.
	Increment(ctx context.Context, key string, delta int64, ttl time.Duration) (int64, error)
	Flush(ctx context.Context) error
}

// Cache is a Store with a key prefix, JSON encoding and stampede
// protection.
type Cache struct {
	store  Store
	prefix string

	mu     sync.Mutex
	flight map[string]*call
}

// New returns a Cache; prefix namespaces keys ("app:").
func New(store Store, prefix string) *Cache {
	return &Cache{store: store, prefix: prefix, flight: map[string]*call{}}
}

// Store returns the backend. It is unprefixed and unencoded: keys must be
// passed already prefixed and values are raw bytes, not JSON.
func (c *Cache) Store() Store { return c.store }

func (c *Cache) key(k string) string { return c.prefix + k }

// Get reads key into T; ok is false when the key is missing or expired.
func Get[T any](ctx context.Context, c *Cache, key string) (v T, ok bool, err error) {
	b, ok, err := c.store.Get(ctx, c.key(key))
	if err != nil || !ok {
		return v, false, err
	}

	if err := json.Unmarshal(b, &v); err != nil {
		return v, false, fmt.Errorf("cache: decode %s: %w", key, err)
	}

	return v, true, nil
}

// Add stores v under key for ttl only when key is absent (or expired) and
// reports whether it did. It is an atomic claim: of many concurrent callers
// exactly one gets true, even across servers on a shared store. Use it for
// one-time codes and "first request wins" guards; use [Cache.NewLock] only
// when the holder must be identified and release the claim early.
func Add[T any](ctx context.Context, c *Cache, key string, v T, ttl time.Duration) (bool, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return false, fmt.Errorf("cache: encode %s: %w", key, err)
	}

	return c.store.Add(ctx, c.key(key), b, ttl)
}

// Pull reads key like [Get], then deletes it. It is not an atomic claim:
// two concurrent callers can both read the value before either deletes it.
// To make sure only one caller acts on a value, claim it with [Add].
func Pull[T any](ctx context.Context, c *Cache, key string) (v T, ok bool, err error) {
	v, ok, err = Get[T](ctx, c, key)
	if err != nil || !ok {
		return v, false, err
	}

	if err := c.Forget(ctx, key); err != nil {
		return v, false, err
	}

	return v, true, nil
}

// Put stores v under key for ttl (0 = no expiry).
func (c *Cache) Put(ctx context.Context, key string, v any, ttl time.Duration) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("cache: encode %s: %w", key, err)
	}

	return c.store.Set(ctx, c.key(key), b, ttl)
}

// Forget deletes keys.
func (c *Cache) Forget(ctx context.Context, keys ...string) error {
	full := make([]string, len(keys))
	for i, k := range keys {
		full[i] = c.key(k)
	}

	return c.store.Delete(ctx, full...)
}

// Flush deletes everything in the store.
func (c *Cache) Flush(ctx context.Context) error { return c.store.Flush(ctx) }

// Increment adds delta to the integer at key (counters, rate limits).
func (c *Cache) Increment(ctx context.Context, key string, delta int64, ttl time.Duration) (int64, error) {
	return c.store.Increment(ctx, c.key(key), delta, ttl)
}

type call struct {
	done chan struct{}
	val  []byte
	err  error
}

// Remember returns the cached value or computes it with fn and stores it
// for ttl. Concurrent misses for the same key in this process run fn once
// (singleflight), so an expired hot key does not stampede the database.
func Remember[T any](ctx context.Context, c *Cache, key string, ttl time.Duration,
	fn func(ctx context.Context) (T, error),
) (T, error) {
	if v, ok, err := Get[T](ctx, c, key); err == nil && ok {
		return v, nil
	}

	c.mu.Lock()

	if cl, ok := c.flight[key]; ok {
		c.mu.Unlock()

		var zero T

		select {
		case <-cl.done:
		case <-ctx.Done():
			return zero, ctx.Err()
		}

		if cl.err != nil {
			return zero, cl.err
		}

		var v T

		err := json.Unmarshal(cl.val, &v)

		return v, err
	}

	cl := &call{done: make(chan struct{})}
	c.flight[key] = cl
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.flight, key)
		c.mu.Unlock()
		close(cl.done)
	}()

	v, err := fn(ctx)
	if err != nil {
		cl.err = err
		return v, err
	}

	if cl.val, cl.err = json.Marshal(v); cl.err != nil {
		return v, cl.err
	}

	if err := c.store.Set(ctx, c.key(key), cl.val, ttl); err != nil {
		return v, err // the value is still good; report the cache failure
	}

	return v, nil
}

// ErrNotAcquired is returned by Lock.Block when the wait expired.
var ErrNotAcquired = errors.New("cache: lock not acquired")

// Lock is a distributed (store-wide) mutual exclusion lock.
type Lock struct {
	c     *Cache
	key   string
	owner []byte
	ttl   time.Duration
}

// NewLock prepares a lock on name that expires after ttl if never released.
// The lock is only as strong as the store: a [Memory] store keeps a held
// lock out of its LRU eviction and, once held locks fill half of its
// entries, refuses new locks (TryAcquire reports false) rather than evict
// one; it is per process, so servers that must exclude each other need a
// shared store (Database, Redis). Do not derive lock names from untrusted
// input: on Memory, a flood of names fills the lock budget and new locks
// fail until held ones expire, so give every lock a TTL. A
// wrapper around Memory hides it from the lock, which then uses Add and
// can be evicted like any entry.
func (c *Cache) NewLock(name string, ttl time.Duration) *Lock {
	return &Lock{c: c, key: c.key("lock:" + name), owner: []byte(rand.Text()), ttl: ttl}
}

// TryAcquire takes the lock if it is free.
func (l *Lock) TryAcquire(ctx context.Context) (bool, error) {
	if m, ok := l.c.store.(*Memory); ok {
		return m.addLock(l.key, l.owner, l.ttl), nil
	}

	return l.c.store.Add(ctx, l.key, l.owner, l.ttl)
}

// Block waits up to wait for the lock, polling every interval.
func (l *Lock) Block(ctx context.Context, wait, interval time.Duration) error {
	deadline := time.Now().Add(wait)

	for {
		ok, err := l.TryAcquire(ctx)
		if err != nil || ok {
			return err
		}

		if time.Now().After(deadline) {
			return ErrNotAcquired
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

// Release frees the lock if this Lock still owns it.
func (l *Lock) Release(ctx context.Context) error {
	_, err := l.c.store.DeleteIf(ctx, l.key, l.owner)
	return err
}
