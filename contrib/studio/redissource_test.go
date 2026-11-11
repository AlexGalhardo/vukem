package studio

import (
	"context"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// recorder is a go-redis hook that answers every command from canned values
// and records the commands, so no server is needed.
type recorder struct {
	mu   sync.Mutex
	cmds []string
	typ  string
}

func (r *recorder) names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Clone(r.cmds)
}

func (r *recorder) DialHook(next goredis.DialHook) goredis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) { return next(ctx, network, addr) }
}

func (r *recorder) answer(cmd goredis.Cmder) {
	r.mu.Lock()
	r.cmds = append(r.cmds, strings.ToUpper(cmd.Name()))
	r.mu.Unlock()

	switch c := cmd.(type) {
	case *goredis.ScanCmd:
		c.SetVal([]string{"a", "b", "a"}, 0)
	case *goredis.StatusCmd:
		c.SetVal(r.typ)
	case *goredis.DurationCmd:
		c.SetVal(time.Minute)
	case *goredis.StringCmd:
		c.SetVal("v")
	case *goredis.IntCmd:
		c.SetVal(3)
	case *goredis.StringSliceCmd:
		c.SetVal([]string{"x", "y", "z"})
	case *goredis.ZSliceCmd:
		c.SetVal([]goredis.Z{{Score: 1.5, Member: "m"}})
	case *goredis.XMessageSliceCmd:
		c.SetVal([]goredis.XMessage{{ID: "1-0", Values: map[string]any{"k": "v"}}})
	case *goredis.Cmd:
		c.SetVal("OK")
	}
}

func (r *recorder) ProcessHook(_ goredis.ProcessHook) goredis.ProcessHook {
	return func(_ context.Context, cmd goredis.Cmder) error {
		r.answer(cmd)
		return nil
	}
}

func (r *recorder) ProcessPipelineHook(_ goredis.ProcessPipelineHook) goredis.ProcessPipelineHook {
	return func(_ context.Context, cmds []goredis.Cmder) error {
		for _, c := range cmds {
			r.answer(c)
		}

		return nil
	}
}

func fakeRedis(t *testing.T, typ string) (*RedisDB, *recorder) {
	t.Helper()

	c := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"})
	rec := &recorder{typ: typ}
	c.AddHook(rec)

	t.Cleanup(func() { _ = c.Close() })

	return newRedisDB("redis", "redis  localhost:6379/0", c), rec
}

func TestSecurity_A04_StudioRedisNeverUsesKEYS(t *testing.T) {
	t.Parallel()

	for _, typ := range []string{"string", "hash", "list", "set", "zset", "stream"} {
		db, rec := fakeRedis(t, typ)

		if _, err := db.Keys(t.Context(), KeyQuery{Match: "session:*"}); err != nil {
			t.Fatal(err)
		}

		if _, err := db.Key(t.Context(), "k", 1); err != nil {
			t.Fatalf("Key(%s): %v", typ, err)
		}

		for _, c := range rec.names() {
			if c == "KEYS" {
				t.Errorf("a KEYS command was sent (%s browsing): %v", typ, rec.names())
			}
		}

		if !slices.Contains(rec.names(), "SCAN") {
			t.Errorf("Keys did not use SCAN: %v", rec.names())
		}
	}
}

func TestRedisKeysDedupeAndDescribe(t *testing.T) {
	t.Parallel()

	db, _ := fakeRedis(t, "string")

	page, err := db.Keys(t.Context(), KeyQuery{})
	if err != nil {
		t.Fatal(err)
	}

	if len(page.Keys) != 2 || page.Keys[0].Key != "a" || page.Keys[0].Type != "string" ||
		page.Keys[0].TTL != time.Minute ||
		page.Cursor != 0 {
		t.Errorf("page = %+v, want the duplicate dropped, type and ttl filled", page)
	}
}

func TestRedisCommandRunsOnlyAllowedLines(t *testing.T) {
	t.Parallel()

	db, rec := fakeRedis(t, "string")

	if out, err := db.Command(t.Context(), "GET k", false); err != nil || out != "OK" {
		t.Errorf("GET = %q, %v", out, err)
	}

	for _, line := range []string{"KEYS *", "FLUSHALL", "SET k v", "CONFIG SET save x", `EVAL "return 1" 0`} {
		before := len(rec.names())

		if _, err := db.Command(t.Context(), line, false); err == nil {
			t.Errorf("%q accepted", line)
		}

		if len(rec.names()) != before {
			t.Errorf("%q reached the server", line)
		}
	}
}

func TestRedisDisplayEscapesBinary(t *testing.T) {
	t.Parallel()

	if got := display("plain"); got != "plain" {
		t.Errorf("display(plain) = %q", got)
	}

	if got := display("a\xff\x00b"); got != `a\xff\x00b` {
		t.Errorf("display(binary) = %q", got)
	}

	long := strings.Repeat("x", maxValueBytes+10)
	if got := display(long); len(got) > maxValueBytes+len("…") || !strings.HasSuffix(got, "…") {
		t.Errorf("display(long) has %d bytes", len(got))
	}
}

func TestRedisWritesUseTheDocumentedCommands(t *testing.T) {
	t.Parallel()

	db, rec := fakeRedis(t, "string")
	ctx := t.Context()

	for _, err := range []error{
		db.SetString(ctx, "k", "v"), db.SetField(ctx, "h", "f", "v"), db.SetTTL(ctx, "k", time.Minute),
		db.SetTTL(ctx, "k", 0), db.Delete(ctx, "k"),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}

	got := rec.names()
	want := []string{"SET", "HSET", "EXPIRE", "PERSIST", "DEL"}

	if !slices.Equal(got, want) {
		t.Errorf("commands = %v, want %v", got, want)
	}
}
