package redis

import (
	"testing"

	goredis "github.com/redis/go-redis/v9"

	"github.com/AlexGalhardo/vukem/session"
)

var _ session.ListStore = (*SessionStore)(nil)

func TestOfflineBehaviour(t *testing.T) {
	t.Parallel()

	if got := escapeGlob(`a*b?[c]\`); got != `a\*b\?\[c\]\\` {
		t.Errorf("escapeGlob = %q", got)
	}

	if _, err := Open(t.Context(), "not-a-url"); err == nil {
		t.Error("bad URL accepted")
	}

	// Without a server the ping fails fast.
	if _, err := Open(t.Context(), "redis://127.0.0.1:1/0?dial_timeout=1s"); err == nil {
		t.Error("Open without a server succeeded")
	}

	c := New(goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"}))
	defer func() { _ = c.Close() }()

	if c.Redis() == nil || c.CacheStore("").Flush(t.Context()) == nil {
		t.Error("flush without a prefix must be refused")
	}

	if s := c.SessionStore(""); s.prefix != "session:" {
		t.Errorf("session prefix = %q", s.prefix)
	}

	if q := c.Queue(QueueOptions{}); q.prefix != "{queue}:" || q.grace <= 0 {
		t.Errorf("queue defaults = %+v", q)
	}

	if l := c.RateLimiter("", 0, 0); l.limit != 1 || l.prefix != "ratelimit:" {
		t.Errorf("limiter defaults = %+v", l)
	}
}

func TestOpenLazy(t *testing.T) {
	t.Parallel()

	if _, err := OpenLazy("not-a-url"); err == nil {
		t.Error("OpenLazy(bad URL) = nil error, want an error")
	}

	// No server listens on port 1: OpenLazy must not contact it, and Ping
	// reports the outage for a readiness check.
	c, err := OpenLazy("redis://127.0.0.1:1/3?dial_timeout=300ms&max_retries=-1")
	if err != nil {
		t.Fatalf("OpenLazy without a server = %v, want nil", err)
	}

	t.Cleanup(func() { _ = c.Close() })

	if c.Redis() == nil {
		t.Fatal("OpenLazy returned a client without a pool")
	}

	if err := c.Ping(t.Context()); err == nil {
		t.Error("Ping without a server = nil, want an error")
	}
}
