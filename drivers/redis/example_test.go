package redis_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/AlexGalhardo/vukem/cache"
	"github.com/AlexGalhardo/vukem/drivers/redis"
	"github.com/AlexGalhardo/vukem/queue"
	"github.com/AlexGalhardo/vukem/session"
)

// One client backs the cache, sessions and the queue; key prefixes keep
// them apart in the same database.
func Example() {
	ctx := context.Background()

	c, err := redis.Open(ctx, os.Getenv("REDIS_URL")) // e.g. Redis on localhost:6379, database 0
	if err != nil {
		panic(err)
	}
	defer c.Close()

	store := cache.New(c.CacheStore("cache:"), "")
	if err := store.Put(ctx, "greeting", "hello", time.Hour); err != nil {
		panic(err)
	}

	sessions, err := session.New(session.Config{Store: c.SessionStore("session:"), Secure: true})
	if err != nil {
		panic(err)
	}

	jobs := queue.New(c.Queue(redis.QueueOptions{}), slog.Default())

	_, _ = sessions, jobs
}

// The limiter is shared by every app instance, unlike ratelimit's memory
// store.
func ExampleClient_RateLimiter() {
	ctx := context.Background()

	c, err := redis.Open(ctx, os.Getenv("REDIS_URL"))
	if err != nil {
		panic(err)
	}
	defer c.Close()

	limiter := c.RateLimiter("rl:login:", 5, time.Minute)

	res, err := limiter.Allow(ctx, "203.0.113.7")
	if err != nil {
		panic(err)
	}

	fmt.Println(res.Allowed, res.Remaining)
}

func ExampleClient_Subscribe() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c, err := redis.Open(ctx, os.Getenv("REDIS_URL"))
	if err != nil {
		panic(err)
	}
	defer c.Close()

	msgs, err := c.Subscribe(ctx, "posts")
	if err != nil {
		panic(err)
	}

	if err := c.Publish(ctx, "posts", []byte("published")); err != nil {
		panic(err)
	}

	fmt.Println(string(<-msgs))
}
