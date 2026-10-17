package redis

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/AlexGalhardo/vukem/session"
)

// SessionStore implements session.ListStore (and so session.UserStore):
// sessions can be revoked and listed per user. Keys hold SHA-256(id) in
// hex, never the session id, so reading the keyspace (SCAN, a replica, a
// dump) yields no usable cookie:
//
//	<prefix><hash>         session data, with the session's TTL
//	<prefix>owner:<hash>   the owning user, same TTL
//	<prefix>user:<user>    set of the user's session hashes, kept as long as
//	                       its longest session; hashes of ended sessions are
//	                       dropped when the set is read
//	<prefix>gone:<hash>    one-minute marker of a deleted session
//
// Sessions saved under the clear id by older versions are moved to the
// hashed key on their first load. Every command touches one key, so it
// works on Redis Cluster. Needs Redis 6.2 or later (GETDEL, SET ... GET).
type SessionStore struct {
	rdb    goredis.UniversalClient
	prefix string

	beforeRekey func() // test hook: runs between claiming and re-keying a legacy key
}

// SessionStore returns a session store with keys under prefix (default
// "session:").
func (c *Client) SessionStore(prefix string) *SessionStore {
	if prefix == "" {
		prefix = "session:"
	}

	return &SessionStore{rdb: c.rdb, prefix: prefix}
}

func (s *SessionStore) dataKey(h string) string      { return s.prefix + h }
func (s *SessionStore) ownerKey(h string) string     { return s.prefix + "owner:" + h }
func (s *SessionStore) goneKey(h string) string      { return s.prefix + "gone:" + h }
func (s *SessionStore) userKey(userID string) string { return s.prefix + "user:" + userID }

// goneFor covers the window between claiming a legacy key and writing it
// under its hash.
const goneFor = time.Minute

var digestRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// legacyKey is the clear-id key an older version may have saved id under,
// or "" when id cannot be one: a digest-shaped id is never read as a clear
// id, so a leaked key cannot be replayed as one.
func (s *SessionStore) legacyKey(id string) string {
	if id == "" || digestRe.MatchString(id) {
		return ""
	}

	return s.prefix + id
}

// Load implements session.Store.
func (s *SessionStore) Load(ctx context.Context, id string) ([]byte, bool, error) {
	h := digest(id)

	data, found, err := s.get(ctx, s.dataKey(h))
	if err != nil || found {
		return data, found, err
	}

	legacy := s.legacyKey(id)
	if legacy == "" {
		return nil, false, nil
	}

	return s.rekey(ctx, legacy, h)
}

func (s *SessionStore) get(ctx context.Context, key string) ([]byte, bool, error) {
	b, err := s.rdb.Get(ctx, key).Bytes()
	if errors.Is(err, goredis.Nil) {
		return nil, false, nil
	}

	if err != nil {
		return nil, false, fmt.Errorf("redis: load session: %w", err)
	}

	return b, true, nil
}

// rekey moves a session saved under its clear id to its hashed key.
// GETDEL claims the legacy key, so only one load moves it; a Delete that
// ran meanwhile set the gone marker before deleting the hashed key, and the
// moved session is then deleted again. A failure loses the session (the
// user signs in again): nothing is reused.
func (s *SessionStore) rekey(ctx context.Context, legacy, h string) ([]byte, bool, error) {
	ttl, err := s.rdb.PTTL(ctx, legacy).Result()
	if err != nil {
		return nil, false, fmt.Errorf("redis: load session: %w", err)
	}

	if ttl <= 0 {
		return nil, false, nil // missing, or without a TTL (never written by this store)
	}

	data, err := s.rdb.GetDel(ctx, legacy).Bytes()
	if errors.Is(err, goredis.Nil) {
		return s.get(ctx, s.dataKey(h)) // another load moved it first
	}

	if err != nil {
		return nil, false, fmt.Errorf("redis: rekey session: %w", err)
	}

	if s.beforeRekey != nil {
		s.beforeRekey()
	}

	err = s.rdb.SetArgs(ctx, s.dataKey(h), data, goredis.SetArgs{Mode: "NX", TTL: ttl}).Err()
	if err != nil && !errors.Is(err, goredis.Nil) {
		return nil, false, fmt.Errorf("redis: rekey session: %w", err)
	}

	gone, err := s.rdb.Exists(ctx, s.goneKey(h)).Result()
	if err != nil || gone > 0 {
		if derr := s.rdb.Del(ctx, s.dataKey(h)).Err(); derr != nil {
			return nil, false, fmt.Errorf("redis: rekey session: %w", derr)
		}

		if err != nil {
			return nil, false, fmt.Errorf("redis: rekey session: %w", err)
		}

		return nil, false, nil
	}

	return data, true, nil
}

// Save implements session.Store; the recorded user is kept.
func (s *SessionStore) Save(ctx context.Context, id string, data []byte, expiry time.Time) error {
	ttl := time.Until(expiry)
	if ttl <= 0 {
		return s.Delete(ctx, id)
	}

	return s.rdb.Set(ctx, s.dataKey(digest(id)), data, ttl).Err()
}

// Delete implements session.Store, also removing a not yet re-keyed
// legacy key.
func (s *SessionStore) Delete(ctx context.Context, id string) error {
	if err := s.deleteHash(ctx, digest(id)); err != nil {
		return err
	}

	if legacy := s.legacyKey(id); legacy != "" {
		if err := s.rdb.Del(ctx, legacy).Err(); err != nil {
			return fmt.Errorf("redis: delete session: %w", err)
		}
	}

	return nil
}

// deleteHash deletes the session stored under h. The gone marker comes
// first: a legacy re-key checks it after writing the hashed key.
func (s *SessionStore) deleteHash(ctx context.Context, h string) error {
	if err := s.rdb.Set(ctx, s.goneKey(h), "1", goneFor).Err(); err != nil {
		return fmt.Errorf("redis: delete session: %w", err)
	}

	if err := s.rdb.Del(ctx, s.dataKey(h)).Err(); err != nil {
		return fmt.Errorf("redis: delete session: %w", err)
	}

	owner, err := s.rdb.GetDel(ctx, s.ownerKey(h)).Result()
	if errors.Is(err, goredis.Nil) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("redis: delete session owner: %w", err)
	}

	return s.rdb.SRem(ctx, s.userKey(owner), h).Err()
}

// Create implements session.UserStore.
func (s *SessionStore) Create(ctx context.Context, id, userID string, data []byte, expiry time.Time) error {
	ttl := time.Until(expiry)
	if ttl <= 0 {
		return s.Delete(ctx, id)
	}

	h := digest(id)

	if err := s.rdb.Set(ctx, s.dataKey(h), data, ttl).Err(); err != nil {
		return fmt.Errorf("redis: create session: %w", err)
	}

	return s.setOwner(ctx, h, userID, ttl)
}

// Update implements session.UserStore: SET ... XX never recreates a
// session that was revoked or logged out while a request ran.
func (s *SessionStore) Update(ctx context.Context, id, userID string, data []byte, expiry time.Time) error {
	ttl := time.Until(expiry)
	if ttl <= 0 {
		return s.Delete(ctx, id)
	}

	h := digest(id)

	err := s.rdb.SetArgs(ctx, s.dataKey(h), data, goredis.SetArgs{Mode: "XX", TTL: ttl}).Err()
	if errors.Is(err, goredis.Nil) {
		return session.ErrSessionNotFound
	}

	if err != nil {
		return fmt.Errorf("redis: update session: %w", err)
	}

	return s.setOwner(ctx, h, userID, ttl)
}

//nolint:gochecknoglobals // compiled once, read-only
var addMember = goredis.NewScript(`
redis.call("SADD", KEYS[1], ARGV[1])
if redis.call("PTTL", KEYS[1]) < tonumber(ARGV[2]) then redis.call("PEXPIRE", KEYS[1], ARGV[2]) end
return 1`)

// setOwner records userID ("" for a guest) as the owner of the session
// stored under h and moves h between the users' sets when the owner
// changed.
func (s *SessionStore) setOwner(ctx context.Context, h, userID string, ttl time.Duration) error {
	var (
		prev string
		err  error
	)

	if userID == "" {
		prev, err = s.rdb.GetDel(ctx, s.ownerKey(h)).Result()
	} else {
		prev, err = s.rdb.SetArgs(ctx, s.ownerKey(h), userID, goredis.SetArgs{TTL: ttl, Get: true}).Result()
	}

	if err != nil && !errors.Is(err, goredis.Nil) {
		return fmt.Errorf("redis: session owner: %w", err)
	}

	if prev != "" && prev != userID {
		if err := s.rdb.SRem(ctx, s.userKey(prev), h).Err(); err != nil {
			return fmt.Errorf("redis: session owner: %w", err)
		}
	}

	if userID == "" {
		return nil
	}

	if err := addMember.Run(ctx, s.rdb, []string{s.userKey(userID)}, h, ttl.Milliseconds()).Err(); err != nil {
		return fmt.Errorf("redis: index session: %w", err)
	}

	return nil
}

// RevokeOthers implements session.UserStore.
func (s *SessionStore) RevokeOthers(ctx context.Context, userID, keepID string) error {
	live, err := s.userSessions(ctx, userID)
	if err != nil {
		return err
	}

	keep := ""
	if keepID != "" {
		keep = digest(keepID)
	}

	for _, ls := range live {
		if ls.hash == keep {
			continue
		}

		if err := s.deleteHash(ctx, ls.hash); err != nil {
			return err
		}
	}

	return nil
}

// ListUser implements session.ListStore.
func (s *SessionStore) ListUser(ctx context.Context, userID string) ([]session.StoredSession, error) {
	live, err := s.userSessions(ctx, userID)
	if err != nil {
		return nil, err
	}

	out := make([]session.StoredSession, 0, len(live))
	for _, ls := range live {
		out = append(out, session.StoredSession{Key: ls.hash, Data: ls.data, Expiry: ls.expiry})
	}

	return out, nil
}

// DeleteUserKey implements session.ListStore.
func (s *SessionStore) DeleteUserKey(ctx context.Context, userID, key string) (bool, error) {
	live, err := s.userSessions(ctx, userID)
	if err != nil {
		return false, err
	}

	for _, ls := range live {
		if ls.hash == key {
			return true, s.deleteHash(ctx, ls.hash)
		}
	}

	return false, nil
}

type liveSession struct {
	hash   string
	data   []byte
	expiry time.Time
}

var errNoUser = errors.New("redis: session user id is empty")

// userSessions returns the live sessions still owned by userID and drops
// the hashes of ended or re-owned ones from the user's set.
func (s *SessionStore) userSessions(ctx context.Context, userID string) ([]liveSession, error) {
	if userID == "" {
		return nil, errNoUser
	}

	hashes, err := s.rdb.SMembers(ctx, s.userKey(userID)).Result()
	if err != nil {
		return nil, fmt.Errorf("redis: list sessions: %w", err)
	}

	var (
		out   []liveSession
		stale []any
	)

	for _, h := range hashes {
		owner, err := s.rdb.Get(ctx, s.ownerKey(h)).Result()
		if err != nil && !errors.Is(err, goredis.Nil) {
			return nil, fmt.Errorf("redis: list sessions: %w", err)
		}

		data, found, err := s.get(ctx, s.dataKey(h))
		if err != nil {
			return nil, err
		}

		ttl, err := s.rdb.PTTL(ctx, s.dataKey(h)).Result()
		if err != nil {
			return nil, fmt.Errorf("redis: list sessions: %w", err)
		}

		if owner != userID || !found || ttl <= 0 {
			stale = append(stale, h)
			continue
		}

		out = append(out, liveSession{hash: h, data: data, expiry: time.Now().Add(ttl)})
	}

	if len(stale) > 0 {
		if err := s.rdb.SRem(ctx, s.userKey(userID), stale...).Err(); err != nil {
			return nil, fmt.Errorf("redis: prune sessions: %w", err)
		}
	}

	return out, nil
}

func digest(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:])
}
