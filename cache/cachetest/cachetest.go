// Package cachetest is the conformance suite every cache.Store passes
// (memory and database in core, Redis in drivers/redis). A new store
// calls [Run] from one of its tests.
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/subsystems.md#cache.
package cachetest

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/cache"
)

// Run checks store; ttl-based checks sleep briefly (keep TTLs short).
func Run(t *testing.T, store cache.Store) {
	t.Helper()

	ctx := t.Context()

	if err := store.Flush(ctx); err != nil {
		t.Fatal(err)
	}

	c := cache.New(store, "ct:")

	// Without the declaration cache.Shared reports false, and OnOneServer
	// and RATE_LIMIT_STORE=cache refuse the store even when it is shared.
	t.Run("DeclaresShared", func(t *testing.T) {
		if _, ok := store.(cache.Sharer); !ok {
			t.Errorf("%T does not implement cache.Sharer (Shared() bool)", store)
		}
	})

	t.Run("GetPutForget", func(t *testing.T) {
		type user struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		}

		if _, ok, err := cache.Get[user](ctx, c, "u1"); ok || err != nil {
			t.Fatalf("missing key = %v %v", ok, err)
		}

		if err := c.Put(ctx, "u1", user{1, "Ana"}, time.Minute); err != nil {
			t.Fatal(err)
		}

		got, ok, err := cache.Get[user](ctx, c, "u1")
		if err != nil || !ok || got != (user{1, "Ana"}) {
			t.Fatalf("Get = %+v %v %v", got, ok, err)
		}

		if _, _, err := cache.Get[int](ctx, c, "u1"); err == nil {
			t.Fatal("decoding into the wrong type succeeded")
		}

		if err := c.Forget(ctx, "u1", "absent"); err != nil {
			t.Fatal(err)
		}

		if _, ok, _ := cache.Get[user](ctx, c, "u1"); ok {
			t.Fatal("forgotten key still present")
		}
	})

	t.Run("BinaryValues", func(t *testing.T) {
		// NUL and invalid UTF-8 bytes: gob/msgpack payloads, hashes, images.
		raw := []byte{0x00, 0xff, 0xfe, 'a', 0x00, 0x80}

		if err := store.Set(ctx, "ct:bin", raw, time.Minute); err != nil {
			t.Fatalf("Set(binary) = %v", err)
		}

		got, ok, err := store.Get(ctx, "ct:bin")
		if err != nil || !ok || !bytes.Equal(got, raw) {
			t.Fatalf("Get(binary) = %x %v %v, want %x", got, ok, err, raw)
		}

		if ok, err := store.Add(ctx, "ct:bin2", raw, time.Minute); err != nil || !ok {
			t.Fatalf("Add(binary) = %v %v", ok, err)
		}

		if ok, err := store.DeleteIf(ctx, "ct:bin2", raw); err != nil || !ok {
			t.Fatalf("DeleteIf(binary) = %v %v", ok, err)
		}
	})

	t.Run("Expiry", func(t *testing.T) {
		if err := c.Put(ctx, "short", "x", 50*time.Millisecond); err != nil {
			t.Fatal(err)
		}

		if err := c.Put(ctx, "forever", "y", 0); err != nil {
			t.Fatal(err)
		}

		time.Sleep(120 * time.Millisecond) //nolint:forbidigo // real TTL expiry across stores

		if _, ok, _ := cache.Get[string](ctx, c, "short"); ok {
			t.Fatal("expired key returned")
		}

		if v, ok, _ := cache.Get[string](ctx, c, "forever"); !ok || v != "y" {
			t.Fatal("no-TTL key lost")
		}
	})

	t.Run("Increment", func(t *testing.T) {
		for want := range []int64{1, 2, 3} {
			n, err := c.Increment(ctx, "hits", 1, time.Minute)
			if err != nil || n != int64(want+1) {
				t.Fatalf("Increment = %d %v, want %d", n, err, want+1)
			}
		}

		if n, err := c.Increment(ctx, "hits", -3, time.Minute); err != nil || n != 0 {
			t.Fatalf("decrement = %d %v", n, err)
		}

		_ = c.Put(ctx, "text", "abc", time.Minute)
		if _, err := c.Increment(ctx, "text", 1, time.Minute); err == nil {
			t.Fatal("incrementing a string succeeded")
		}

		if v, ok, err := cache.Get[string](ctx, c, "text"); err != nil || !ok || v != "abc" {
			t.Fatalf("value after a failed Increment = %q %v %v, want it unchanged", v, ok, err)
		}
	})

	// Rate limits count with Increment: concurrent callers (other servers)
	// must never read the same value, or requests go uncounted.
	t.Run("IncrementConcurrent", func(t *testing.T) {
		const workers, each = 8, 10

		var (
			wg   sync.WaitGroup
			mu   sync.Mutex
			seen = map[int64]bool{}
			errs atomic.Int32
		)

		for range workers {
			wg.Go(func() {
				for range each {
					n, err := c.Increment(ctx, "concurrent", 1, time.Minute)
					if err != nil {
						errs.Add(1)
						continue
					}

					mu.Lock()
					seen[n] = true
					mu.Unlock()
				}
			})
		}

		wg.Wait()

		if errs.Load() != 0 || len(seen) != workers*each {
			t.Fatalf("concurrent Increment: %d errors, %d distinct values, want 0 and %d",
				errs.Load(), len(seen), workers*each)
		}
	})

	t.Run("IncrementRestartsExpired", func(t *testing.T) {
		if _, err := c.Increment(ctx, "window", 5, 20*time.Millisecond); err != nil {
			t.Fatal(err)
		}

		time.Sleep(40 * time.Millisecond) //nolint:forbidigo // real expiry in the store

		if n, err := c.Increment(ctx, "window", 1, time.Minute); err != nil || n != 1 {
			t.Fatalf("Increment after expiry = %d %v, want 1", n, err)
		}
	})

	t.Run("RememberSingleflight", func(t *testing.T) {
		var calls atomic.Int32

		release := make(chan struct{})
		answer := 42
		fn := func(context.Context) (int, error) {
			calls.Add(1)
			<-release

			if answer == 0 {
				return 0, errors.New("unreachable")
			}

			return answer, nil
		}

		var wg sync.WaitGroup

		results := make([]int, 10)
		for i := range results {
			wg.Go(func() {
				v, err := cache.Remember(ctx, c, "answer", time.Minute, fn)
				if err != nil {
					t.Error(err)
				}

				results[i] = v
			})
		}

		time.Sleep(50 * time.Millisecond) //nolint:forbidigo // let every goroutine reach the flight
		close(release)
		wg.Wait()

		if calls.Load() != 1 {
			t.Fatalf("fn ran %d times, want 1 (stampede)", calls.Load())
		}

		for _, r := range results {
			if r != 42 {
				t.Fatalf("results = %v", results)
			}
		}

		// Cached now: fn is not called again; errors are not cached.
		if v, _ := cache.Remember(ctx, c, "answer", time.Minute, fn); v != 42 || calls.Load() != 1 {
			t.Fatal("cached value recomputed")
		}

		boom := errors.New("boom")
		if _, err := cache.Remember(
			ctx,
			c,
			"failing",
			time.Minute,
			func(context.Context) (int, error) { return 0, boom },
		); !errors.Is(
			err,
			boom,
		) {
			t.Fatalf("error = %v", err)
		}

		if _, ok, _ := cache.Get[int](ctx, c, "failing"); ok {
			t.Fatal("error result cached")
		}
	})

	t.Run("Locks", func(t *testing.T) {
		a := c.NewLock("report", time.Minute)
		b := c.NewLock("report", time.Minute)

		if ok, err := a.TryAcquire(ctx); !ok || err != nil {
			t.Fatalf("a = %v %v", ok, err)
		}

		if ok, _ := b.TryAcquire(ctx); ok {
			t.Fatal("b acquired a held lock")
		}

		if err := b.Release(ctx); err != nil { // not the owner: no effect
			t.Fatal(err)
		}

		if ok, _ := b.TryAcquire(ctx); ok {
			t.Fatal("b released a's lock")
		}

		if err := b.Block(ctx, 30*time.Millisecond, 10*time.Millisecond); !errors.Is(err, cache.ErrNotAcquired) {
			t.Fatalf("Block = %v", err)
		}

		if err := a.Release(ctx); err != nil {
			t.Fatal(err)
		}

		if err := b.Block(ctx, time.Second, 10*time.Millisecond); err != nil {
			t.Fatalf("Block after release = %v", err)
		}

		// Expired locks can be taken.
		short := c.NewLock("short", 30*time.Millisecond)
		if ok, _ := short.TryAcquire(ctx); !ok {
			t.Fatal("short lock")
		}

		time.Sleep(80 * time.Millisecond) //nolint:forbidigo // real TTL expiry

		if ok, _ := c.NewLock("short", time.Minute).TryAcquire(ctx); !ok {
			t.Fatal("expired lock not reusable")
		}
	})

	t.Run("Flush", func(t *testing.T) {
		_ = c.Put(ctx, "k", 1, time.Minute)

		if err := c.Flush(ctx); err != nil {
			t.Fatal(err)
		}

		if _, ok, _ := cache.Get[int](ctx, c, "k"); ok {
			t.Fatal("key survived Flush")
		}
	})
}
