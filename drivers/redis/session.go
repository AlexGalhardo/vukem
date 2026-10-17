package redis

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/AlexGalhardo/vukem/session"
)

// SessionStore implements session.ListStore (and so session.UserStore):
// sessions can be revoked and listed per user. Keys under the prefix:
//
//	<prefix><id>         session data, with the session's TTL
//	<prefix>owner:<id>   the owning user, same TTL
//	<prefix>user:<user>  set of the user's session ids, kept as long as its
//	                     longest session; ids of ended sessions are dropped
//	                     when the set is read
//
// Every command touches one key, so it works on Redis Cluster. Needs Redis
// 6.2 or later (GETDEL, SET ... GET).
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

func (s *SessionStore) dataKey(id string) string     { return s.prefix + id }
func (s *SessionStore) ownerKey(id string) string    { return s.prefix + "owner:" + id }
func (s *SessionStore) userKey(userID string) string { return s.prefix + "user:" + userID }

// Load implements session.Store.
func (s *SessionStore) Load(ctx context.Context, id string) ([]byte, bool, error) {
	b, err := s.rdb.Get(ctx, s.dataKey(id)).Bytes()
	if errors.Is(err, goredis.Nil) {
		return nil, false, nil
	}

	return b, err == nil, err
}

// Save implements session.Store; the recorded user is kept.
func (s *SessionStore) Save(ctx context.Context, id string, data []byte, expiry time.Time) error {
	ttl := time.Until(expiry)
	if ttl <= 0 {
		return s.Delete(ctx, id)
	}

	return s.rdb.Set(ctx, s.dataKey(id), data, ttl).Err()
}

// Delete implements session.Store.
func (s *SessionStore) Delete(ctx context.Context, id string) error {
	if err := s.rdb.Del(ctx, s.dataKey(id)).Err(); err != nil {
		return fmt.Errorf("redis: delete session: %w", err)
	}

	owner, err := s.rdb.GetDel(ctx, s.ownerKey(id)).Result()
	if errors.Is(err, goredis.Nil) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("redis: delete session owner: %w", err)
	}

	return s.rdb.SRem(ctx, s.userKey(owner), id).Err()
}

// Create implements session.UserStore.
func (s *SessionStore) Create(ctx context.Context, id, userID string, data []byte, expiry time.Time) error {
	ttl := time.Until(expiry)
	if ttl <= 0 {
		return s.Delete(ctx, id)
	}

	if err := s.rdb.Set(ctx, s.dataKey(id), data, ttl).Err(); err != nil {
		return fmt.Errorf("redis: create session: %w", err)
	}

	return s.setOwner(ctx, id, userID, ttl)
}

// Update implements session.UserStore: SET ... XX never recreates a
// session that was revoked or logged out while a request ran.
func (s *SessionStore) Update(ctx context.Context, id, userID string, data []byte, expiry time.Time) error {
	ttl := time.Until(expiry)
	if ttl <= 0 {
		return s.Delete(ctx, id)
	}

	err := s.rdb.SetArgs(ctx, s.dataKey(id), data, goredis.SetArgs{Mode: "XX", TTL: ttl}).Err()
	if errors.Is(err, goredis.Nil) {
		return session.ErrSessionNotFound
	}

	if err != nil {
		return fmt.Errorf("redis: update session: %w", err)
	}

	return s.setOwner(ctx, id, userID, ttl)
}

//nolint:gochecknoglobals // compiled once, read-only
var addMember = goredis.NewScript(`
redis.call("SADD", KEYS[1], ARGV[1])
if redis.call("PTTL", KEYS[1]) < tonumber(ARGV[2]) then redis.call("PEXPIRE", KEYS[1], ARGV[2]) end
return 1`)

// setOwner records userID ("" for a guest) as the owner of id and moves id
// between the users' sets when the owner changed.
func (s *SessionStore) setOwner(ctx context.Context, id, userID string, ttl time.Duration) error {
	var (
		prev string
		err  error
	)

	if userID == "" {
		prev, err = s.rdb.GetDel(ctx, s.ownerKey(id)).Result()
	} else {
		prev, err = s.rdb.SetArgs(ctx, s.ownerKey(id), userID, goredis.SetArgs{TTL: ttl, Get: true}).Result()
	}

	if err != nil && !errors.Is(err, goredis.Nil) {
		return fmt.Errorf("redis: session owner: %w", err)
	}

	if prev != "" && prev != userID {
		if err := s.rdb.SRem(ctx, s.userKey(prev), id).Err(); err != nil {
			return fmt.Errorf("redis: session owner: %w", err)
		}
	}

	if userID == "" {
		return nil
	}

	if err := addMember.Run(ctx, s.rdb, []string{s.userKey(userID)}, id, ttl.Milliseconds()).Err(); err != nil {
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

	for _, ls := range live {
		if ls.id == keepID {
			continue
		}

		if err := s.Delete(ctx, ls.id); err != nil {
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
		out = append(out, session.StoredSession{Key: digest(ls.id), Data: ls.data, Expiry: ls.expiry})
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
		if digest(ls.id) == key {
			return true, s.Delete(ctx, ls.id)
		}
	}

	return false, nil
}

type liveSession struct {
	id     string
	data   []byte
	expiry time.Time
}

var errNoUser = errors.New("redis: session user id is empty")

// userSessions returns the live sessions still owned by userID and drops
// the ids of ended or re-owned ones from the user's set.
func (s *SessionStore) userSessions(ctx context.Context, userID string) ([]liveSession, error) {
	if userID == "" {
		return nil, errNoUser
	}

	ids, err := s.rdb.SMembers(ctx, s.userKey(userID)).Result()
	if err != nil {
		return nil, fmt.Errorf("redis: list sessions: %w", err)
	}

	var (
		out   []liveSession
		stale []any
	)

	for _, id := range ids {
		owner, err := s.rdb.Get(ctx, s.ownerKey(id)).Result()
		if err != nil && !errors.Is(err, goredis.Nil) {
			return nil, fmt.Errorf("redis: list sessions: %w", err)
		}

		data, err := s.rdb.Get(ctx, s.dataKey(id)).Bytes()
		if err != nil && !errors.Is(err, goredis.Nil) {
			return nil, fmt.Errorf("redis: list sessions: %w", err)
		}

		ttl, terr := s.rdb.PTTL(ctx, s.dataKey(id)).Result()
		if terr != nil {
			return nil, fmt.Errorf("redis: list sessions: %w", terr)
		}

		if owner != userID || err != nil || ttl <= 0 {
			stale = append(stale, id)
			continue
		}

		out = append(out, liveSession{id: id, data: data, expiry: time.Now().Add(ttl)})
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
