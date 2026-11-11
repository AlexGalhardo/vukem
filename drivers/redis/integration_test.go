//go:build integration

package redis_test

import (
	"crypto/rand"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/broadcast/presencetest"
	"github.com/AlexGalhardo/vukem/cache/cachetest"
	"github.com/AlexGalhardo/vukem/drivers/redis"
	"github.com/AlexGalhardo/vukem/queue/queuetest"
	"github.com/AlexGalhardo/vukem/session/sessiontest"
	"github.com/AlexGalhardo/vukem/sse/ssetest"
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

func TestSessionStoreConformance(t *testing.T) {
	sessiontest.Run(t, open(t).SessionStore(unique("session")))
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

func TestFlushWithoutPrefixIsRefused(t *testing.T) {
	c := open(t)

	if err := c.CacheStore("").Flush(t.Context()); err == nil {
		t.Fatal("flush without a prefix must be refused")
	}
}

func TestOpenLazyConnectsOnFirstUse(t *testing.T) {
	url := os.Getenv("REDIS_URL")
	if url == "" {
		url = "redis://localhost:56379/0"
	}

	c, err := redis.OpenLazy(url)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = c.Close() })

	if err := c.Ping(t.Context()); err != nil {
		t.Fatalf("Ping = %v", err)
	}
}

func TestIncrementKeepsTTLOfExistingCounter(t *testing.T) {
	ctx := t.Context()
	c := open(t)
	prefix := unique("incr")
	s := c.CacheStore(prefix)

	// The counter returns to 0, then to the delta: it still existed, so the
	// first ttl must stand.
	for _, step := range []struct {
		delta int64
		ttl   time.Duration
	}{{3, 10 * time.Second}, {-3, time.Hour}, {5, time.Hour}} {
		if _, err := s.Increment(ctx, "k", step.delta, step.ttl); err != nil {
			t.Fatal(err)
		}
	}

	ttl, err := c.Redis().PTTL(ctx, prefix+"k").Result()
	if err != nil {
		t.Fatal(err)
	}

	if ttl <= 0 || ttl > 10*time.Second {
		t.Errorf("PTTL = %v, want within (0, 10s]", ttl)
	}
}

func TestPresenceConformance(t *testing.T) {
	const ttl = 2 * time.Second

	presencetest.Run(t, open(t).PresenceStore(unique("presence"), ttl), presencetest.Options{TTL: ttl})
}

func TestBrokerConformance(t *testing.T) {
	ssetest.Run(t, open(t), ssetest.Options{})
}
