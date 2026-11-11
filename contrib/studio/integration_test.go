//go:build integration

package studio

import (
	"crypto/rand"
	"os"
	"strings"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func mongoURL() string {
	if u := os.Getenv("MONGO_URL"); u != "" {
		return u
	}

	return "mongodb://localhost:57017"
}

func redisURL() string {
	if u := os.Getenv("REDIS_URL"); u != "" {
		return u
	}

	return "redis://localhost:56379/0"
}

func TestMongoAgainstARealServer(t *testing.T) {
	db := "studio_" + strings.ToLower(rand.Text()[:8])

	m, err := OpenMongo(t.Context(), "mongo", mongoURL(), db, true)
	if err != nil {
		t.Fatalf("OpenMongo: %v (start MongoDB with task services:up)", err)
	}

	t.Cleanup(func() {
		if r, ok := m.api.(realMongo); ok {
			_ = r.db.Drop(t.Context())
		}

		_ = m.Close()
	})

	ctx := t.Context()

	// Studio never creates a collection: seed one with the driver, then edit
	// through the write path.
	if err := m.api.insert(ctx, "people", bson.D{{Key: "name", Value: "seed"}, {Key: "age", Value: 1}}); err != nil {
		t.Fatal(err)
	}

	for _, doc := range []string{`{"name": "ada", "age": 36}`, `{"name": "linus", "age": 55}`, `{"name": "grace", "age": 85}`} {
		if err := m.Insert(ctx, "people", doc); err != nil {
			t.Fatal(err)
		}
	}

	cols, err := m.Collections(ctx)
	if err != nil || len(cols) != 1 || cols[0].Name != "people" {
		t.Fatalf("Collections = %v, %v", cols, err)
	}

	page, err := m.Documents(ctx, DocQuery{Collection: "people", PerPage: 2})
	if err != nil || len(page.Docs) != 2 || !page.HasMore || page.Next == "" {
		t.Fatalf("first page = %+v, %v", page, err)
	}

	rest, err := m.Documents(ctx, DocQuery{Collection: "people", PerPage: 2, After: page.Next})
	if err != nil || len(rest.Docs) != 2 || rest.HasMore {
		t.Fatalf("second page = %+v, %v", rest, err)
	}

	byField, err := m.Documents(ctx, DocQuery{Collection: "people", Filters: []Filter{{"age", "gt", "50"}}})
	if err != nil || len(byField.Docs) != 2 {
		t.Errorf("age > 50 = %+v, %v", byField, err)
	}

	// A hostile filter value is data, not an operator.
	if _, err := m.Documents(
		ctx,
		DocQuery{Collection: "people", Filters: []Filter{{"name", "eq", `{"$ne": null}`}}},
	); err == nil {
		t.Error("an operator document was accepted as a filter value")
	}

	if res, err := m.Find(ctx, "people", `{"name": "ada"}`); err != nil || len(res.Docs) != 1 {
		t.Errorf("console find = %+v, %v", res, err)
	}

	if _, err := m.Find(ctx, "people", `{"$where": "true"}`); err == nil {
		t.Error("$where reached the server")
	}

	id := page.Docs[0].ID
	if err := m.Replace(ctx, "people", id, `{"name": "replaced"}`); err != nil {
		t.Fatal(err)
	}

	got, err := m.Document(ctx, "people", id)
	if err != nil || !strings.Contains(got.JSON, "replaced") {
		t.Errorf("after Replace: %+v, %v", got, err)
	}

	if err := m.Delete(ctx, "people", id); err != nil {
		t.Fatal(err)
	}

	if err := m.Delete(ctx, "people", id); err == nil {
		t.Error("deleting a document that is gone succeeded")
	}
}

func TestRedisAgainstARealServer(t *testing.T) {
	r, err := OpenRedis(t.Context(), "redis", redisURL())
	if err != nil {
		t.Fatalf("OpenRedis: %v (start Redis with task services:up)", err)
	}

	t.Cleanup(func() { _ = r.Close() })

	ctx := t.Context()
	prefix := "studiotest:" + rand.Text()[:8] + ":"
	raw := r.client

	t.Cleanup(func() {
		for _, k := range []string{"s", "h", "l", "set", "z", "x"} {
			raw.Del(ctx, prefix+k)
		}
	})

	raw.Set(ctx, prefix+"s", "value", time.Hour)
	raw.HSet(ctx, prefix+"h", "f", "v")
	raw.RPush(ctx, prefix+"l", "a", "b")
	raw.SAdd(ctx, prefix+"set", "m")
	raw.ZAdd(ctx, prefix+"z", goredis.Z{Score: 2, Member: "m"})
	raw.XAdd(ctx, &goredis.XAddArgs{Stream: prefix + "x", Values: map[string]any{"k": "v"}})

	for range 600 {
		raw.Set(ctx, prefix+"bulk:"+rand.Text()[:6], "x", time.Minute)
	}

	t.Cleanup(func() {
		keys, _ := raw.Keys(ctx, prefix+"bulk:*").Result()
		if len(keys) > 0 {
			raw.Del(ctx, keys...)
		}
	})

	// SCAN pages through everything with the prefix.
	seen := map[string]bool{}

	var cursor uint64

	for {
		pg, err := r.Keys(ctx, KeyQuery{Match: prefix + "*", Cursor: cursor, Count: 100})
		if err != nil {
			t.Fatal(err)
		}

		for _, k := range pg.Keys {
			seen[k.Key] = true
		}

		if cursor = pg.Cursor; cursor == 0 {
			break
		}
	}

	if len(seen) < 606 {
		t.Errorf("SCAN found %d keys, want at least 606", len(seen))
	}

	for key, typ := range map[string]string{"s": "string", "h": "hash", "l": "list", "set": "set", "z": "zset", "x": "stream"} {
		d, err := r.Key(ctx, prefix+key, 1)
		if err != nil || d.Type != typ || len(d.Fields) == 0 {
			t.Errorf("Key(%s) = %+v, %v", key, d, err)
		}
	}

	if d, _ := r.Key(ctx, prefix+"s", 1); d.TTL <= 0 {
		t.Errorf("string TTL = %v, want about an hour", d.TTL)
	}

	// SET keeps the TTL (needs Redis 6 or later).
	if err := r.SetString(ctx, prefix+"s", "edited"); err != nil {
		t.Fatal(err)
	}

	if d, _ := r.Key(ctx, prefix+"s", 1); d.TTL <= 0 || d.Fields[0][1] != "edited" {
		t.Errorf("after SetString: %+v, want the value changed and the TTL kept", d)
	}

	if out, err := r.Command(ctx, "GET "+prefix+"s", false); err != nil || out != "edited" {
		t.Errorf("console GET = %q, %v", out, err)
	}

	if _, err := r.Command(ctx, "KEYS *", true); err == nil {
		t.Error("KEYS reached the server")
	}

	if err := r.Delete(ctx, prefix+"s"); err != nil {
		t.Fatal(err)
	}
}
