package redis

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/AlexGalhardo/vukem/broadcast"
)

// defaultPresenceTTL is how long a connection lives without a Touch.
const defaultPresenceTTL = 60 * time.Second

// Presence is a broadcast.PresenceStore shared by every instance that uses
// the same Redis and prefix: "here" lists and joining/leaving become global.
// A connection that is not touched for the TTL is dropped, so the members of
// an instance that died disappear by themselves (no leaving is announced for
// them).
//
// Each channel uses three keys with the same prefix (a hash of member JSON
// and a hash of member IDs, both by connection, and a sorted set of expiry
// times), and a script touches all three: Redis Cluster is not supported.
// Expiry uses the clock of the app instances (time.Now), so keep their
// clocks in sync (recommended; the heartbeat leaves a wide margin).
type Presence struct {
	rdb    goredis.UniversalClient
	prefix string
	ttl    time.Duration
}

var _ broadcast.PresenceStore = (*Presence)(nil)

// PresenceStore returns a broadcast.PresenceStore shared by every instance
// using the same Redis and prefix. A connection that is not touched for ttl
// is dropped (0 = 60s). Set broadcast.Config.PresenceHeartbeat to a third of
// ttl or less (the default heartbeat is 20s).
//
//	b := broadcast.New(broadcast.Config{Broker: rdb, Presence: rdb.PresenceStore("presence:", 0)})
func (c *Client) PresenceStore(prefix string, ttl time.Duration) *Presence {
	if ttl <= 0 {
		ttl = defaultPresenceTTL
	}

	return &Presence{rdb: c.rdb, prefix: prefix, ttl: ttl}
}

// Every script gets KEYS = {members hash, ids hash, expiry zset} and starts
// by dropping the connections whose expiry has passed, so a dead instance
// never counts. Each runs atomically, which is what makes "first" and "last"
// exact when instances race.
//
// shortcut: present scans the ids hash, O(connections) per join/leave on a
// channel; fine for rooms up to a few thousand connections, switch to a
// per-member counter hash beyond that.
const presencePrune = `
local function prune(m, id, exp, now)
	for _, c in ipairs(redis.call("ZRANGEBYSCORE", exp, "-inf", now)) do
		redis.call("HDEL", m, c)
		redis.call("HDEL", id, c)
		redis.call("ZREM", exp, c)
	end
end
local function present(id, who)
	for _, v in ipairs(redis.call("HVALS", id)) do
		if v == who then return true end
	end
	return false
end
local function keep(keys, ttl)
	for i = 1, 3 do redis.call("PEXPIRE", keys[i], ttl * 2) end
end
`

// ARGV: now, ttl, conn, memberID, member JSON. Returns 1 when the member had
// no live connection on the channel.
//
//nolint:gochecknoglobals // compiled once, read-only
var presenceJoin = goredis.NewScript(presencePrune + `
local now, ttl = tonumber(ARGV[1]), tonumber(ARGV[2])
prune(KEYS[1], KEYS[2], KEYS[3], now)
local first = 1
if present(KEYS[2], ARGV[4]) then first = 0 end
redis.call("HSET", KEYS[1], ARGV[3], ARGV[5])
redis.call("HSET", KEYS[2], ARGV[3], ARGV[4])
redis.call("ZADD", KEYS[3], now + ttl, ARGV[3])
keep(KEYS, ttl)
return first`)

// ARGV: now, ttl, conn. Returns 1 when the connection was the member's last,
// 0 when the member stays or the connection is unknown (or already expired).
//
//nolint:gochecknoglobals // compiled once, read-only
var presenceLeave = goredis.NewScript(presencePrune + `
local now = tonumber(ARGV[1])
prune(KEYS[1], KEYS[2], KEYS[3], now)
local who = redis.call("HGET", KEYS[2], ARGV[3])
if not who then return 0 end
redis.call("HDEL", KEYS[1], ARGV[3])
redis.call("HDEL", KEYS[2], ARGV[3])
redis.call("ZREM", KEYS[3], ARGV[3])
if present(KEYS[2], who) then return 0 end
return 1`)

// ARGV: now, ttl, conn. A connection still in the hashes is revived even if
// its expiry just passed: its client is evidently still connected.
//
//nolint:gochecknoglobals // compiled once, read-only
var presenceTouch = goredis.NewScript(presencePrune + `
local now, ttl = tonumber(ARGV[1]), tonumber(ARGV[2])
if redis.call("HEXISTS", KEYS[2], ARGV[3]) == 0 then return 0 end
redis.call("ZADD", KEYS[3], now + ttl, ARGV[3])
keep(KEYS, ttl)
return 1`)

// ARGV: now, ttl. Returns the member JSON of every live connection.
//
//nolint:gochecknoglobals // compiled once, read-only
var presenceMembers = goredis.NewScript(presencePrune + `
prune(KEYS[1], KEYS[2], KEYS[3], tonumber(ARGV[1]))
return redis.call("HVALS", KEYS[1])`)

func (p *Presence) keys(channel string) []string {
	k := p.prefix + channel

	return []string{k + ":m", k + ":id", k + ":exp"}
}

func (p *Presence) now() int64 { return time.Now().UnixMilli() }

// Join implements broadcast.PresenceStore.
func (p *Presence) Join(ctx context.Context, channel, conn string, m broadcast.Member) (bool, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return false, fmt.Errorf("redis: presence member: %w", err)
	}

	n, err := presenceJoin.Run(ctx, p.rdb, p.keys(channel), p.now(), p.ttl.Milliseconds(), conn, m.ID, string(b)).Int()
	if err != nil {
		return false, fmt.Errorf("redis: presence join: %w", err)
	}

	return n == 1, nil
}

// Leave implements broadcast.PresenceStore. The member the connection was
// joined as decides "last", not memberID, so a wrong ID cannot hide a member.
func (p *Presence) Leave(ctx context.Context, channel, conn, _ string) (bool, error) {
	n, err := presenceLeave.Run(ctx, p.rdb, p.keys(channel), p.now(), p.ttl.Milliseconds(), conn).Int()
	if err != nil {
		return false, fmt.Errorf("redis: presence leave: %w", err)
	}

	return n == 1, nil
}

// Touch implements broadcast.PresenceStore: it pushes the connection's expiry
// to now plus the TTL.
func (p *Presence) Touch(ctx context.Context, channel, conn string) error {
	err := presenceTouch.Run(ctx, p.rdb, p.keys(channel), p.now(), p.ttl.Milliseconds(), conn).Err()
	if err != nil {
		return fmt.Errorf("redis: presence touch: %w", err)
	}

	return nil
}

// Members implements broadcast.PresenceStore: one entry per member ID,
// sorted by ID. Info comes back as decoded JSON (map[string]any, ...).
func (p *Presence) Members(ctx context.Context, channel string) ([]broadcast.Member, error) {
	raw, err := presenceMembers.Run(ctx, p.rdb, p.keys(channel), p.now(), p.ttl.Milliseconds()).StringSlice()
	if err != nil {
		return nil, fmt.Errorf("redis: presence members: %w", err)
	}

	seen := map[string]bool{}
	out := make([]broadcast.Member, 0, len(raw))

	for _, s := range raw {
		var m broadcast.Member
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			return nil, fmt.Errorf("redis: presence member: %w", err)
		}

		if !seen[m.ID] {
			seen[m.ID] = true

			out = append(out, m)
		}
	}

	slices.SortFunc(out, func(a, b broadcast.Member) int { return cmp.Compare(a.ID, b.ID) })

	return out, nil
}
