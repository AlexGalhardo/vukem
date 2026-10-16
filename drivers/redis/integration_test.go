//go:build integration

package redis_test

import (
	"context"
	"crypto/rand"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/cache/cachetest"
	"github.com/AlexGalhardo/vukem/drivers/redis"
	"github.com/AlexGalhardo/vukem/queue/queuetest"
)

// open connects to REDIS_URL (task services:up starts one on :56379).
func open(t *testing.T) *redis.Client {
	t.Helper()

	url := os.Getenv("REDIS_URL")
	if url == "" {
		url = "redis://localhost:56379/0"
	}

	c, err := redis.Open(t.Context(), url)
	if err != nil {
		t.Fatalf("redis: %v (start it with task services:up)", err)
	}

	t.Cleanup(func() { _ = c.Close() })

	return c
}

// unique isolates a test run's keys.
func unique(name string) string { return "vukemtest:" + name + ":" + rand.Text()[:8] + ":" }

func TestCacheConformance(t *testing.T) {
	c := open(t)
	cachetest.Run(t, c.CacheStore(unique("cache")))
}

func TestSessionStore(t *testing.T) {
	ctx := t.Context()
	s := open(t).SessionStore(unique("session"))

	if err := s.Save(ctx, "abc", []byte("data"), time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	if b, ok, err := s.Load(ctx, "abc"); err != nil || !ok || string(b) != "data" {
		t.Fatalf("load = %q %v %v", b, ok, err)
	}

	if err := s.Save(ctx, "abc", []byte("data"), time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}

	if _, ok, _ := s.Load(ctx, "abc"); ok {
		t.Fatal("expired session still loads")
	}
}

func TestQueueConformance(t *testing.T) {
	var offset atomic.Int64

	q := open(t).Queue(redis.QueueOptions{
		Prefix: "{" + unique("queue") + "}:",
		Grace:  time.Second,
		Now:    func() time.Time { return time.Now().Add(time.Duration(offset.Load())) },
	})

	queuetest.Run(t, q, queuetest.Options{Pending: q.Pending, Advance: func(d time.Duration) { offset.Add(int64(d)) }})
}

func TestRateLimiter(t *testing.T) {
	ctx := t.Context()
	l := open(t).RateLimiter(unique("rl"), 3, time.Minute)

	for i := range 3 {
		r, err := l.Allow(ctx, "ip")
		if err != nil || !r.Allowed || r.Remaining != 2-i {
			t.Fatalf("request %d = %+v %v", i, r, err)
		}
	}

	r, err := l.Allow(ctx, "ip")
	if err != nil || r.Allowed || r.RetryAfter <= 0 || r.RetryAfter > 20*time.Second {
		t.Fatalf("4th request = %+v %v", r, err)
	}

	if r, _ := l.Allow(ctx, "other"); !r.Allowed {
		t.Fatal("keys share a budget")
	}
}

func TestPubSubAndFlushSafety(t *testing.T) {
	c := open(t)
	ctx, cancel := context.WithCancel(t.Context())
	channel := unique("chan")

	msgs, err := c.Subscribe(ctx, channel)
	if err != nil {
		t.Fatal(err)
	}

	if err := c.Publish(ctx, channel, []byte("hello")); err != nil {
		t.Fatal(err)
	}

	select {
	case m := <-msgs:
		if string(m) != "hello" {
			t.Fatalf("message = %q", m)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no message")
	}

	cancel()

	for range msgs { // drain until closed
	}

	if err := c.CacheStore("").Flush(t.Context()); err == nil {
		t.Fatal("flush without a prefix must be refused")
	}
}
