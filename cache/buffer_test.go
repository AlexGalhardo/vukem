package cache_test

import (
	"context"
	"errors"
	"maps"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/cache"
)

func TestBuffer(t *testing.T) {
	t.Parallel()

	var b cache.Buffer[int64]

	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			b.Add(1, 1)
			b.Add(2, 2)
		})
	}

	wg.Wait()
	b.Add(3, 0) // a zero delta is not a pending count

	if diff := cmp.Diff(map[int64]int64{1: 50, 2: 100}, b.Swap()); diff != "" {
		t.Errorf("Swap() diff (-want +got):\n%s", diff)
	}

	if got := b.Swap(); got != nil {
		t.Errorf("second Swap() = %v, want nil", got)
	}

	b.Add(1, 5)
	b.Restore(map[int64]int64{1: 50, 2: 100})

	if diff := cmp.Diff(map[int64]int64{1: 55, 2: 100}, b.Swap()); diff != "" {
		t.Errorf("Restore diff (-want +got):\n%s", diff)
	}
}

func TestBufferFlush(t *testing.T) {
	t.Parallel()

	var b cache.Buffer[string]

	b.Add("a", 3)

	boom := errors.New("db down")
	fail := func(context.Context, map[string]int64) error { return boom }
	if err := b.Flush(t.Context(), fail); !errors.Is(err, boom) {
		t.Fatalf("Flush() = %v, want %v", err, boom)
	}

	b.Add("a", 1)

	var got map[string]int64
	if err := b.Flush(t.Context(), func(_ context.Context, m map[string]int64) error {
		got = maps.Clone(m)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(map[string]int64{"a": 4}, got); diff != "" {
		t.Errorf("flushed diff (-want +got):\n%s", diff)
	}

	called := false
	spy := func(context.Context, map[string]int64) error { called = true; return nil }
	if err := b.Flush(t.Context(), spy); err != nil || called {
		t.Errorf("empty Flush() = %v, called = %v", err, called)
	}
}

func TestBufferMaxKeys(t *testing.T) {
	t.Parallel()

	var dropped []string

	b := cache.Buffer[string]{MaxKeys: 2, OnDrop: func(k string, n int64) {
		dropped = append(dropped, k)

		if k == "a" && n != 3 {
			t.Errorf("OnDrop(a) count = %d, want 3", n)
		}
	}}

	b.Add("a", 1)
	b.Add("b", 1)
	b.Add("a", 2) // an existing key never drops
	b.Add("c", 1) // drops the oldest, a
	b.Add("d", 1) // then b

	if diff := cmp.Diff([]string{"a", "b"}, dropped); diff != "" {
		t.Errorf("dropped (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(map[string]int64{"c": 1, "d": 1}, b.Swap()); diff != "" {
		t.Errorf("Swap (-want +got):\n%s", diff)
	}

	// A new set starts empty: the cap counts from zero again.
	b.Add("e", 1)
	b.Add("f", 1)

	if len(dropped) != 2 {
		t.Errorf("dropped after Swap = %v, want no new drops", dropped)
	}

	// Restore respects the cap too.
	b.Restore(map[string]int64{"g": 1})

	if got := len(b.Swap()); got != 2 {
		t.Errorf("len after Restore = %d, want 2", got)
	}
}
