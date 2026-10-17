package cache_test

import (
	"context"
	"fmt"
	"time"

	"github.com/AlexGalhardo/vukem/cache"
)

type user struct {
	ID   int64
	Name string
}

func ExampleRemember() {
	ctx := context.Background()
	c := cache.New(cache.NewMemory(1000), "app:")

	loads := 0
	load := func(context.Context) (user, error) {
		loads++
		return user{ID: 42, Name: "Ada"}, nil
	}

	first, _ := cache.Remember(ctx, c, "user:42", time.Hour, load)
	second, _ := cache.Remember(ctx, c, "user:42", time.Hour, load)
	fmt.Println(first.Name, second.Name, "loads:", loads)
	// Output: Ada Ada loads: 1
}

func ExampleGet() {
	ctx := context.Background()
	c := cache.New(cache.NewMemory(1000), "")

	_ = c.Put(ctx, "greeting", "hello", time.Minute)

	v, ok, err := cache.Get[string](ctx, c, "greeting")
	fmt.Println(v, ok, err)

	_, ok, _ = cache.Get[string](ctx, c, "missing")
	fmt.Println(ok)
	// Output:
	// hello true <nil>
	// false
}

func ExampleCache_NewLock() {
	ctx := context.Background()
	c := cache.New(cache.NewMemory(1000), "")

	lock := c.NewLock("reports", time.Minute)
	got, _ := lock.TryAcquire(ctx)
	again, _ := c.NewLock("reports", time.Minute).TryAcquire(ctx)
	fmt.Println(got, again)

	_ = lock.Release(ctx)
	// Output: true false
}
