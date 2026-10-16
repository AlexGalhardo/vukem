package redis

import (
	"testing"

	goredis "github.com/redis/go-redis/v9"
)

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
