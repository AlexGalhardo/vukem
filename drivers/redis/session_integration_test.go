//go:build integration

package redis_test

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/drivers/redis"
)

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// Keys hold SHA-256(id), never the cookie value: reading the keyspace
// (SCAN, a replica, a dump) yields no usable session cookie, and a stored
// key does not load a session.
func TestSecurity_A04_RedisSessionIDsStoredHashed(t *testing.T) {
	c := open(t)
	prefix := unique("hashed")
	s := c.SessionStore(prefix)
	ctx := t.Context()
	id := rand.Text()

	if err := s.Create(ctx, id, "web:7", []byte(`{}`), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	keys, err := c.Redis().Keys(ctx, prefix+"*").Result()
	if err != nil {
		t.Fatal(err)
	}

	for _, k := range keys {
		if strings.Contains(k, id) {
			t.Fatalf("key %q holds the session id", k)
		}
	}

	members, err := c.Redis().SMembers(ctx, prefix+"user:web:7").Result()
	if err != nil || len(members) != 1 || members[0] != sha256Hex(id) {
		t.Fatalf("user set = %v, %v; want the id's SHA-256", members, err)
	}

	if _, found, err := s.Load(ctx, sha256Hex(id)); err != nil || found {
		t.Fatalf("Load(stored hash) = %v, %v; want not found", found, err)
	}
}

// Sessions saved by older versions under the clear id keep working: the
// first load moves them to the hashed key with their TTL.
func TestRedisSessionLegacyKeyRekeyed(t *testing.T) {
	c := open(t)
	prefix := unique("legacy")
	s := c.SessionStore(prefix)
	ctx := t.Context()
	id := rand.Text()

	if err := c.Redis().Set(ctx, prefix+id, `{"v":1}`, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}

	for range 2 {
		data, found, err := s.Load(ctx, id)
		if err != nil || !found || string(data) != `{"v":1}` {
			t.Fatalf("Load(legacy) = %s, %v, %v", data, found, err)
		}
	}

	if n, _ := c.Redis().Exists(ctx, prefix+id).Result(); n != 0 {
		t.Fatal("clear-id key still exists after the first load")
	}

	ttl, err := c.Redis().PTTL(ctx, prefix+sha256Hex(id)).Result()
	if err != nil || ttl <= 0 || ttl > time.Hour {
		t.Fatalf("hashed key TTL = %v, %v; want the legacy TTL", ttl, err)
	}

	// Update works on the re-keyed session and Delete ends it.
	if err := s.Update(ctx, id, "web:7", []byte(`{"v":2}`), time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("Update(rekeyed) = %v", err)
	}

	if err := s.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}

	if _, found, _ := s.Load(ctx, id); found {
		t.Fatal("session loads after Delete")
	}

	// A legacy key is also removed by Delete before any load, and a key that
	// looks like a digest is never read as a clear id.
	other := rand.Text()
	if err := c.Redis().Set(ctx, prefix+other, `{}`, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}

	if err := s.Delete(ctx, other); err != nil {
		t.Fatal(err)
	}

	if n, _ := c.Redis().Exists(ctx, prefix+other).Result(); n != 0 {
		t.Fatal("Delete left the legacy key")
	}

	digestID := sha256Hex("x")
	if err := c.Redis().Set(ctx, prefix+digestID, `{}`, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}

	if _, found, _ := s.Load(ctx, digestID); found {
		t.Fatal("a digest-shaped id loaded as a legacy key")
	}
}

// A legacy session logged out (or revoked) while its first load moves it
// is not written back under the hashed key.
func TestRedisSessionLegacyDeletedDuringRekey(t *testing.T) {
	c := open(t)
	prefix := unique("legacyrace")
	s := c.SessionStore(prefix)
	ctx := t.Context()
	id := rand.Text()

	if err := c.Redis().Set(ctx, prefix+id, `{"v":1}`, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}

	redis.SetBeforeRekey(s, func() {
		if err := s.Delete(ctx, id); err != nil {
			t.Error(err)
		}
	})

	if data, found, err := s.Load(ctx, id); err != nil || found {
		t.Fatalf("Load(legacy deleted during rekey) = %s, %v, %v; want not found", data, found, err)
	}

	redis.SetBeforeRekey(s, nil)

	if _, found, _ := s.Load(ctx, id); found {
		t.Fatal("session resurrected under the hashed key")
	}
}
