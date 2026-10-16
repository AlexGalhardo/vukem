package redis

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/AlexGalhardo/vukem/queue"
)

// QueueOptions configure the queue driver.
type QueueOptions struct {
	// Prefix of the keys (default "{queue}:"; the braces keep every key in
	// one Redis Cluster slot, which the Lua scripts need).
	Prefix string
	// Grace is added to a job's timeout before an abandoned reservation is
	// taken over (default 1 minute).
	Grace time.Duration
	// Now is the clock (default time.Now); tests set it.
	Now func() time.Time
}

// Queue implements queue.Driver and queue.FailedStore. Jobs live in sorted
// sets scored by availability; reservation is one atomic Lua script, so
// any number of workers can share a queue. Delivery is at least once.
type Queue struct {
	rdb    goredis.UniversalClient
	prefix string
	grace  time.Duration
	now    func() time.Time
}

// Queue returns a queue driver.
func (c *Client) Queue(o QueueOptions) *Queue {
	if o.Prefix == "" {
		o.Prefix = "{queue}:"
	}

	if o.Grace <= 0 {
		o.Grace = time.Minute
	}

	if o.Now == nil {
		o.Now = time.Now
	}

	return &Queue{rdb: c.rdb, prefix: o.Prefix, grace: o.Grace, now: o.Now}
}

func (q *Queue) ready(name string) string    { return q.prefix + "q:" + name + ":ready" }
func (q *Queue) reserved(name string) string { return q.prefix + "q:" + name + ":reserved" }
func (q *Queue) job(id string) string        { return q.prefix + "job:" + id }
func (q *Queue) failedKey(id string) string  { return q.prefix + "failed:" + id }
func (q *Queue) failedSet() string           { return q.prefix + "failed" }
func (q *Queue) queues() string              { return q.prefix + "queues" }

func ms(t time.Time) float64 { return float64(t.UnixMilli()) }

// Push implements queue.Driver.
func (q *Queue) Push(ctx context.Context, m *queue.Message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}

	_, err = q.rdb.TxPipelined(ctx, func(p goredis.Pipeliner) error {
		p.Set(ctx, q.job(m.ID), b, 0)
		p.ZAdd(ctx, q.ready(m.Queue), goredis.Z{Score: ms(m.AvailableAt), Member: m.ID})
		p.SAdd(ctx, q.queues(), m.Queue)

		return nil
	})

	return err
}

//nolint:gochecknoglobals // compiled once, read-only
var popScript = goredis.NewScript(`
local now = tonumber(ARGV[1])
local expired = redis.call("ZRANGEBYSCORE", KEYS[2], "-inf", now)
for _, id in ipairs(expired) do
  redis.call("ZREM", KEYS[2], id)
  redis.call("ZADD", KEYS[1], now, id)
end
local ids = redis.call("ZRANGEBYSCORE", KEYS[1], "-inf", now, "LIMIT", 0, 1)
if #ids == 0 then return false end
local id = ids[1]
redis.call("ZREM", KEYS[1], id)
local job = redis.call("GET", ARGV[3] .. id)
if not job then return false end
local timeout = tonumber(cjson.decode(job)["timeout"]) or 0
redis.call("ZADD", KEYS[2], now + math.floor(timeout / 1000000) + tonumber(ARGV[2]), id)
return job`)

// Pop implements queue.Driver.
func (q *Queue) Pop(ctx context.Context, queues []string) (*queue.Message, error) {
	now := q.now().UnixMilli()

	for _, name := range queues {
		raw, err := popScript.Run(ctx, q.rdb, []string{q.ready(name), q.reserved(name)},
			now, q.grace.Milliseconds(), q.prefix+"job:").Text()
		if errors.Is(err, goredis.Nil) {
			continue
		}

		if err != nil {
			return nil, err
		}

		var m queue.Message
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			return nil, err
		}

		return &m, nil
	}

	return nil, nil
}

// Ack implements queue.Driver.
func (q *Queue) Ack(ctx context.Context, m *queue.Message) error {
	_, err := q.rdb.TxPipelined(ctx, func(p goredis.Pipeliner) error {
		p.ZRem(ctx, q.reserved(m.Queue), m.ID)
		p.Del(ctx, q.job(m.ID))

		return nil
	})

	return err
}

// Release implements queue.Driver.
func (q *Queue) Release(ctx context.Context, m *queue.Message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}

	_, err = q.rdb.TxPipelined(ctx, func(p goredis.Pipeliner) error {
		p.Set(ctx, q.job(m.ID), b, 0)
		p.ZRem(ctx, q.reserved(m.Queue), m.ID)
		p.ZAdd(ctx, q.ready(m.Queue), goredis.Z{Score: ms(m.AvailableAt), Member: m.ID})

		return nil
	})

	return err
}

// Fail implements queue.Driver.
func (q *Queue) Fail(ctx context.Context, m *queue.Message) error {
	now := q.now().UTC()

	b, err := json.Marshal(queue.FailedJob{Message: *m, FailedAt: now})
	if err != nil {
		return err
	}

	_, err = q.rdb.TxPipelined(ctx, func(p goredis.Pipeliner) error {
		p.ZRem(ctx, q.reserved(m.Queue), m.ID)
		p.Del(ctx, q.job(m.ID))
		p.Set(ctx, q.failedKey(m.ID), b, 0)
		p.ZAdd(ctx, q.failedSet(), goredis.Z{Score: ms(now), Member: m.ID})

		return nil
	})

	return err
}

// Failed implements queue.FailedStore, newest first.
func (q *Queue) Failed(ctx context.Context) ([]queue.FailedJob, error) {
	ids, err := q.rdb.ZRevRange(ctx, q.failedSet(), 0, -1).Result()
	if err != nil || len(ids) == 0 {
		return nil, err
	}

	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = q.failedKey(id)
	}

	vals, err := q.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}

	out := make([]queue.FailedJob, 0, len(vals))

	for _, v := range vals {
		s, ok := v.(string)
		if !ok {
			continue
		}

		var fj queue.FailedJob
		if err := json.Unmarshal([]byte(s), &fj); err != nil {
			return nil, err
		}

		out = append(out, fj)
	}

	return out, nil
}

func (q *Queue) failedIDs(ctx context.Context, ids []string) ([]string, error) {
	if len(ids) > 0 {
		return ids, nil
	}

	return q.rdb.ZRange(ctx, q.failedSet(), 0, -1).Result()
}

// Retry implements queue.FailedStore: jobs go back to their queue with
// attempts reset (all when ids is empty).
func (q *Queue) Retry(ctx context.Context, ids ...string) (int, error) {
	ids, err := q.failedIDs(ctx, ids)
	if err != nil {
		return 0, err
	}

	n := 0

	for _, id := range ids {
		raw, err := q.rdb.Get(ctx, q.failedKey(id)).Bytes()
		if errors.Is(err, goredis.Nil) {
			continue
		}

		if err != nil {
			return n, err
		}

		var fj queue.FailedJob
		if err := json.Unmarshal(raw, &fj); err != nil {
			return n, err
		}

		m := fj.Message
		m.Attempts, m.AvailableAt = 0, q.now().UTC()

		if err := q.Push(ctx, &m); err != nil {
			return n, err
		}

		if err := q.forget(ctx, id); err != nil {
			return n, err
		}

		n++
	}

	return n, nil
}

// Forget implements queue.FailedStore (all when ids is empty).
func (q *Queue) Forget(ctx context.Context, ids ...string) (int, error) {
	ids, err := q.failedIDs(ctx, ids)
	if err != nil {
		return 0, err
	}

	n := 0

	for _, id := range ids {
		removed, err := q.rdb.ZRem(ctx, q.failedSet(), id).Result()
		if err != nil {
			return n, err
		}

		if err := q.rdb.Del(ctx, q.failedKey(id)).Err(); err != nil {
			return n, err
		}

		n += int(removed)
	}

	return n, nil
}

func (q *Queue) forget(ctx context.Context, id string) error {
	_, err := q.rdb.TxPipelined(ctx, func(p goredis.Pipeliner) error {
		p.ZRem(ctx, q.failedSet(), id)
		p.Del(ctx, q.failedKey(id))

		return nil
	})

	return err
}

// Pending returns the number of waiting or running jobs.
func (q *Queue) Pending(ctx context.Context) (int, error) {
	names, err := q.rdb.SMembers(ctx, q.queues()).Result()
	if err != nil {
		return 0, err
	}

	total := 0

	for _, name := range names {
		for _, key := range []string{q.ready(name), q.reserved(name)} {
			n, err := q.rdb.ZCard(ctx, key).Result()
			if err != nil {
				return 0, err
			}

			total += int(n)
		}
	}

	return total, nil
}
