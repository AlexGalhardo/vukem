package studio

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	goredis "github.com/redis/go-redis/v9"

	"github.com/AlexGalhardo/vukem/drivers/redis"
)

// APIs used, checked with go doc on go-redis v9.23.0: Scan, Type, TTL, Get,
// HScan, LRange, SScan, ZRangeWithScores, XRange, Do and Set with KeepTTL.

const (
	maxKeyElements = 200
	maxValueBytes  = 4096
	defaultScan    = 200
	maxScan        = 1000
)

// RedisDB is the Redis source. It never sends KEYS: keys come from SCAN.
type RedisDB struct {
	name   string
	label  string
	client goredis.UniversalClient
	closer func() error
}

// OpenRedis opens url as the source called name. Errors never quote the URL.
func OpenRedis(ctx context.Context, name, rawURL string) (*RedisDB, error) {
	c, err := redis.Open(ctx, rawURL)
	if err != nil {
		return nil, fmt.Errorf("studio: cannot open Redis (%s): %w", Redact(rawURL), errNoURL(err, rawURL))
	}

	return &RedisDB{name: name, label: Redact(rawURL), client: c.Redis(), closer: c.Close}, nil
}

// newRedisDB wraps an existing client (tests).
func newRedisDB(name, label string, client goredis.UniversalClient) *RedisDB {
	return &RedisDB{name: name, label: label, client: client, closer: client.Close}
}

// Name implements Source.
func (r *RedisDB) Name() string { return r.name }

// Kind implements Source.
func (*RedisDB) Kind() Kind { return KindRedis }

// Label implements Source: host and database, never credentials.
func (r *RedisDB) Label() string { return r.label }

// Ping implements Source.
func (r *RedisDB) Ping(ctx context.Context) error { return r.client.Ping(ctx).Err() }

// Close implements Source.
func (r *RedisDB) Close() error { return r.closer() }

// Keys implements RedisSource: one SCAN step, then each key's type and TTL in
// a pipeline. SCAN may repeat a key and may return an empty page while the
// cursor is not 0; duplicates are dropped here.
func (r *RedisDB) Keys(ctx context.Context, q KeyQuery) (KeyPage, error) {
	match := q.Match
	if match == "" {
		match = "*"
	}

	count := q.Count
	if count < 1 || count > maxScan {
		count = defaultScan
	}

	names, cursor, err := r.client.Scan(ctx, q.Cursor, match, int64(count)).Result()
	if err != nil {
		return KeyPage{}, err
	}

	names = dedupeStrings(names)
	page := KeyPage{Cursor: cursor, Keys: make([]KeyInfo, 0, len(names))}

	if len(names) == 0 {
		return page, nil
	}

	types := make([]*goredis.StatusCmd, len(names))
	ttls := make([]*goredis.DurationCmd, len(names))

	_, err = r.client.Pipelined(ctx, func(p goredis.Pipeliner) error {
		for i, k := range names {
			types[i], ttls[i] = p.Type(ctx, k), p.TTL(ctx, k)
		}

		return nil
	})
	if err != nil && !errors.Is(err, goredis.Nil) {
		return KeyPage{}, err
	}

	for i, k := range names {
		page.Keys = append(page.Keys, KeyInfo{Key: k, Type: types[i].Val(), TTL: ttls[i].Val()})
	}

	return page, nil
}

func dedupeStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := in[:0:0]

	for _, s := range in {
		if !seen[s] {
			seen[s] = true

			out = append(out, s)
		}
	}

	return out
}

// Key implements RedisSource: at most maxKeyElements elements of page (1-based).
func (r *RedisDB) Key(ctx context.Context, key string, page int) (KeyDetail, error) {
	if page < 1 {
		page = 1
	}

	typ, err := r.client.Type(ctx, key).Result()
	if err != nil {
		return KeyDetail{}, err
	}

	if typ == "none" {
		return KeyDetail{}, errors.New("studio: the key does not exist")
	}

	ttl, err := r.client.TTL(ctx, key).Result()
	if err != nil {
		return KeyDetail{}, err
	}

	var d KeyDetail

	d.Key, d.Type, d.TTL = key, typ, ttl
	start := int64(page-1) * maxKeyElements
	stop := start + maxKeyElements - 1

	switch typ {
	case "string":
		err = r.loadString(ctx, &d)
	case "hash":
		err = r.loadHash(ctx, &d, start)
	case "list":
		err = r.loadList(ctx, &d, start, stop)
	case "set":
		err = r.loadSet(ctx, &d, start)
	case "zset":
		err = r.loadZSet(ctx, &d, start, stop)
	case "stream":
		err = r.loadStream(ctx, &d)
	default:
		err = fmt.Errorf("studio: unsupported type %q", typ)
	}

	return d, err
}

func (r *RedisDB) loadString(ctx context.Context, d *KeyDetail) error {
	v, err := r.client.Get(ctx, d.Key).Result()
	if err != nil {
		return err
	}

	d.Size = int64(len(v))
	d.Fields = [][2]string{{"value", display(v)}}

	return nil
}

// loadHash walks the hash with HSCAN, so a large hash is not read at once;
// start is how many elements earlier pages consumed.
func (r *RedisDB) loadHash(ctx context.Context, d *KeyDetail, start int64) error {
	size, err := r.client.HLen(ctx, d.Key).Result()
	if err != nil {
		return err
	}

	d.Size = size

	flat, more, err := r.scanPage(ctx, start, func(c uint64) ([]string, uint64, error) {
		return r.client.HScan(ctx, d.Key, c, "*", maxKeyElements).Result()
	}, 2)
	if err != nil {
		return err
	}

	for i := 0; i+1 < len(flat); i += 2 {
		d.Fields = append(d.Fields, [2]string{display(flat[i]), display(flat[i+1])})
	}

	d.More = more

	return nil
}

func (r *RedisDB) loadSet(ctx context.Context, d *KeyDetail, start int64) error {
	size, err := r.client.SCard(ctx, d.Key).Result()
	if err != nil {
		return err
	}

	d.Size = size

	members, more, err := r.scanPage(ctx, start, func(c uint64) ([]string, uint64, error) {
		return r.client.SScan(ctx, d.Key, c, "*", maxKeyElements).Result()
	}, 1)
	if err != nil {
		return err
	}

	for i, m := range members {
		d.Fields = append(d.Fields, [2]string{strconv.FormatInt(start+int64(i), 10), display(m)})
	}

	d.More = more

	return nil
}

// scanPage drains an HSCAN/SSCAN cursor, skips the first skip elements and
// returns at most maxKeyElements of them (width values per element). It is
// bounded: skip is at most a few thousand pages of 200.
func (*RedisDB) scanPage(
	_ context.Context, skip int64, step func(uint64) ([]string, uint64, error), width int,
) (vals []string, more bool, err error) {
	var (
		cursor uint64
		seen   int64
	)

	for {
		batch, next, err := step(cursor)
		if err != nil {
			return nil, false, err
		}

		for i := 0; i+width <= len(batch); i += width {
			if seen >= skip && int64(len(vals)/width) < maxKeyElements {
				vals = append(vals, batch[i:i+width]...)
			} else if seen >= skip {
				more = true
			}

			seen++
		}

		if next == 0 || more {
			return vals, more, nil
		}

		cursor = next
	}
}

func (r *RedisDB) loadList(ctx context.Context, d *KeyDetail, start, stop int64) error {
	size, err := r.client.LLen(ctx, d.Key).Result()
	if err != nil {
		return err
	}

	d.Size = size

	items, err := r.client.LRange(ctx, d.Key, start, stop).Result()
	if err != nil {
		return err
	}

	for i, it := range items {
		d.Fields = append(d.Fields, [2]string{strconv.FormatInt(start+int64(i), 10), display(it)})
	}

	d.More = size > stop+1

	return nil
}

func (r *RedisDB) loadZSet(ctx context.Context, d *KeyDetail, start, stop int64) error {
	size, err := r.client.ZCard(ctx, d.Key).Result()
	if err != nil {
		return err
	}

	d.Size = size

	members, err := r.client.ZRangeWithScores(ctx, d.Key, start, stop).Result()
	if err != nil {
		return err
	}

	for _, m := range members {
		d.Fields = append(d.Fields, [2]string{display(fmt.Sprint(m.Member)), strconv.FormatFloat(m.Score, 'g', -1, 64)})
	}

	d.More = size > stop+1

	return nil
}

func (r *RedisDB) loadStream(ctx context.Context, d *KeyDetail) error {
	size, err := r.client.XLen(ctx, d.Key).Result()
	if err != nil {
		return err
	}

	d.Size = size

	msgs, err := r.client.XRangeN(ctx, d.Key, "-", "+", maxKeyElements).Result()
	if err != nil {
		return err
	}

	for _, m := range msgs {
		d.Fields = append(d.Fields, [2]string{m.ID, display(fmt.Sprint(m.Values))})
	}

	d.More = size > maxKeyElements

	return nil
}

// display shows a value: text as is (cut at maxValueBytes), anything that is
// not valid UTF-8 with \xNN escapes, so binary values are visible and safe.
func display(v string) string {
	cut := false
	if len(v) > maxValueBytes {
		v, cut = v[:maxValueBytes], true
	}

	if !utf8.ValidString(v) {
		var b strings.Builder

		for i := range len(v) {
			if c := v[i]; c >= 0x20 && c < 0x7f {
				b.WriteByte(c)
			} else {
				fmt.Fprintf(&b, `\x%02x`, c)
			}
		}

		v = b.String()
	}

	if cut {
		v += "…"
	}

	return v
}

// Command implements RedisSource: one allowed command line, run as is.
func (r *RedisDB) Command(ctx context.Context, line string, write bool) (string, error) {
	args, err := parseRedisLine(line)
	if err != nil {
		return "", err
	}

	if err := checkRedisArgs(args, write); err != nil {
		return "", err
	}

	anyArgs := make([]any, len(args))
	for i, a := range args {
		anyArgs[i] = a
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	v, err := r.client.Do(ctx, anyArgs...).Result()
	if errors.Is(err, goredis.Nil) {
		return "(nil)", nil
	}

	if err != nil {
		return "", err
	}

	return display(fmt.Sprintf("%v", v)), nil
}

// SetString implements RedisSource: SET with KEEPTTL, so editing a value does
// not drop its expiry. KeepTTL needs Redis 6.0 or later; the server's error
// is shown otherwise and nothing is changed.
func (r *RedisDB) SetString(ctx context.Context, key, value string) error {
	return r.client.Set(ctx, key, value, goredis.KeepTTL).Err()
}

// SetField implements RedisSource: HSET of one field.
func (r *RedisDB) SetField(ctx context.Context, key, field, value string) error {
	return r.client.HSet(ctx, key, field, value).Err()
}

// SetTTL implements RedisSource: EXPIRE, or PERSIST for a ttl of 0 or less.
func (r *RedisDB) SetTTL(ctx context.Context, key string, ttl time.Duration) error {
	if ttl <= 0 {
		return r.client.Persist(ctx, key).Err()
	}

	return r.client.Expire(ctx, key, ttl).Err()
}

// Delete implements RedisSource: DEL of one key.
func (r *RedisDB) Delete(ctx context.Context, key string) error {
	return r.client.Del(ctx, key).Err()
}
