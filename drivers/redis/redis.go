// Package redis connects Vukem to Redis (or Valkey): a cache store, a
// session store, a queue driver, a GCRA rate limiter and pub/sub.
//
//	c, err := redis.Open(ctx, os.Getenv("REDIS_URL")) // redis://localhost:6379/0
//	cache.New(c.CacheStore("cache:"), "")
//	queue.New(c.Queue(redis.QueueOptions{}), log)
//
// Locks for the scheduler and cache.Cache.NewLock work on the cache store.
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/subsystems.md.
package redis

import (
	"context"
	"errors"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// Client is a Redis connection pool.
type Client struct {
	rdb goredis.UniversalClient
}

// Open connects to url (redis://[user:pass@]host:port/db or rediss:// for
// TLS) and pings the server.
func Open(ctx context.Context, url string) (*Client, error) {
	opts, err := goredis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("redis: parse url: %w", err)
	}

	c := New(goredis.NewClient(opts))
	if err := c.Ping(ctx); err != nil {
		_ = c.Close()
		return nil, err
	}

	return c, nil
}

// New wraps an existing go-redis client (cluster, sentinel, custom
// options).
func New(rdb goredis.UniversalClient) *Client { return &Client{rdb: rdb} }

// Redis returns the underlying go-redis client.
func (c *Client) Redis() goredis.UniversalClient { return c.rdb }

// Ping checks connectivity (health checks).
func (c *Client) Ping(ctx context.Context) error {
	if err := c.rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis: ping: %w", err)
	}

	return nil
}

// Close closes the pool.
func (c *Client) Close() error { return c.rdb.Close() }

// ---------------------------------------------------------------- cache

// Store implements cache.Store with keys under a prefix.
type Store struct {
	rdb    goredis.UniversalClient
	prefix string
}

// CacheStore returns a cache store whose keys start with prefix (Flush
// deletes only those keys, never the whole database).
func (c *Client) CacheStore(prefix string) *Store { return &Store{rdb: c.rdb, prefix: prefix} }

func (s *Store) k(key string) string { return s.prefix + key }

// Get implements cache.Store.
func (s *Store) Get(ctx context.Context, key string) ([]byte, bool, error) {
	b, err := s.rdb.Get(ctx, s.k(key)).Bytes()
	if errors.Is(err, goredis.Nil) {
		return nil, false, nil
	}

	return b, err == nil, err
}

// Set implements cache.Store.
func (s *Store) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return s.rdb.Set(ctx, s.k(key), value, max(ttl, 0)).Err()
}

// Add implements cache.Store (SET NX).
func (s *Store) Add(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	err := s.rdb.SetArgs(ctx, s.k(key), value, goredis.SetArgs{Mode: "NX", TTL: max(ttl, 0)}).Err()
	if errors.Is(err, goredis.Nil) {
		return false, nil
	}

	return err == nil, err
}

// Delete implements cache.Store.
func (s *Store) Delete(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}

	full := make([]string, len(keys))
	for i, k := range keys {
		full[i] = s.k(k)
	}

	return s.rdb.Del(ctx, full...).Err()
}

//nolint:gochecknoglobals // compiled once, read-only
var deleteIf = goredis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then return redis.call("DEL", KEYS[1]) end
return 0`)

// DeleteIf implements cache.Store atomically (lock release).
func (s *Store) DeleteIf(ctx context.Context, key string, value []byte) (bool, error) {
	n, err := deleteIf.Run(ctx, s.rdb, []string{s.k(key)}, value).Int()
	return n == 1, err
}

//nolint:gochecknoglobals // compiled once, read-only
var increment = goredis.NewScript(`
local v = redis.call("INCRBY", KEYS[1], ARGV[1])
if v == tonumber(ARGV[1]) and tonumber(ARGV[2]) > 0 then redis.call("PEXPIRE", KEYS[1], ARGV[2]) end
return v`)

// Increment implements cache.Store; a missing key starts at 0 with ttl.
func (s *Store) Increment(ctx context.Context, key string, delta int64, ttl time.Duration) (int64, error) {
	return increment.Run(ctx, s.rdb, []string{s.k(key)}, delta, ttl.Milliseconds()).Int64()
}

// Flush implements cache.Store: it deletes the keys under the prefix.
func (s *Store) Flush(ctx context.Context) error {
	if s.prefix == "" {
		return errors.New("redis: refusing to flush a cache store without a prefix")
	}

	iter := s.rdb.Scan(ctx, 0, escapeGlob(s.prefix)+"*", 500).Iterator()

	var batch []string

	for iter.Next(ctx) {
		batch = append(batch, iter.Val())
		if len(batch) == 500 {
			if err := s.rdb.Del(ctx, batch...).Err(); err != nil {
				return err
			}

			batch = batch[:0]
		}
	}

	if err := iter.Err(); err != nil {
		return err
	}

	if len(batch) > 0 {
		return s.rdb.Del(ctx, batch...).Err()
	}

	return nil
}

func escapeGlob(s string) string {
	out := make([]byte, 0, len(s))

	for i := range len(s) {
		switch s[i] {
		case '*', '?', '[', ']', '\\':
			out = append(out, '\\')
		}

		out = append(out, s[i])
	}

	return string(out)
}

// ---------------------------------------------------------------- sessions

// SessionStore implements session.Store.
type SessionStore struct {
	rdb    goredis.UniversalClient
	prefix string
}

// SessionStore returns a session store with keys under prefix (default
// "session:").
func (c *Client) SessionStore(prefix string) *SessionStore {
	if prefix == "" {
		prefix = "session:"
	}

	return &SessionStore{rdb: c.rdb, prefix: prefix}
}

// Load implements session.Store.
func (s *SessionStore) Load(ctx context.Context, id string) ([]byte, bool, error) {
	b, err := s.rdb.Get(ctx, s.prefix+id).Bytes()
	if errors.Is(err, goredis.Nil) {
		return nil, false, nil
	}

	return b, err == nil, err
}

// Save implements session.Store.
func (s *SessionStore) Save(ctx context.Context, id string, data []byte, expiry time.Time) error {
	ttl := time.Until(expiry)
	if ttl <= 0 {
		return s.Delete(ctx, id)
	}

	return s.rdb.Set(ctx, s.prefix+id, data, ttl).Err()
}

// Delete implements session.Store.
func (s *SessionStore) Delete(ctx context.Context, id string) error {
	return s.rdb.Del(ctx, s.prefix+id).Err()
}

// ---------------------------------------------------------------- pub/sub

// Publish sends payload on channel (server-sent events fan-out, cache
// invalidation...).
func (c *Client) Publish(ctx context.Context, channel string, payload []byte) error {
	return c.rdb.Publish(ctx, channel, payload).Err()
}

// Subscribe delivers messages published on channels until ctx is done; the
// returned channel is closed then.
func (c *Client) Subscribe(ctx context.Context, channels ...string) (<-chan []byte, error) {
	ps := c.rdb.Subscribe(ctx, channels...)
	if _, err := ps.Receive(ctx); err != nil { // wait for the subscription
		_ = ps.Close()
		return nil, fmt.Errorf("redis: subscribe: %w", err)
	}

	out := make(chan []byte, 64)

	go func() {
		defer close(out)
		defer func() { _ = ps.Close() }()

		msgs := ps.Channel()

		for {
			select {
			case <-ctx.Done():
				return
			case m, ok := <-msgs:
				if !ok {
					return
				}

				select {
				case out <- []byte(m.Payload):
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return out, nil
}
