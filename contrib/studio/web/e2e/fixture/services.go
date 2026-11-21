//go:build ignore

// Command services seeds the MongoDB and Redis servers the Playwright specs
// browse, and removes what it seeded. Run from contrib/studio:
//
//	go run ./web/e2e/fixture/services.go seed <mongo-url> <mongo-db> <redis-url> <prefix>
//	go run ./web/e2e/fixture/services.go clean <mongo-url> <mongo-db> <redis-url> <prefix>
//
// The Mongo database and the Redis key prefix are unique to one run, so it
// never touches what else the servers hold.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func main() {
	if len(os.Args) != 6 || (os.Args[1] != "seed" && os.Args[1] != "clean") {
		fmt.Fprintln(os.Stderr, "usage: services seed|clean <mongo-url> <mongo-db> <redis-url> <prefix>")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := run(ctx, os.Args[1], os.Args[2], os.Args[3], os.Args[4], os.Args[5]); err != nil {
		fmt.Fprintln(os.Stderr, "services:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, action, mongoURL, db, redisURL, prefix string) error {
	mc, err := mongo.Connect(options.Client().ApplyURI(mongoURL))
	if err != nil {
		return err
	}
	defer mc.Disconnect(ctx)

	if action == "clean" {
		if err := mc.Database(db).Drop(ctx); err != nil {
			return err
		}
	} else if err := seedMongo(ctx, mc.Database(db)); err != nil {
		return err
	}

	opts, err := goredis.ParseURL(redisURL)
	if err != nil {
		return err
	}

	rc := goredis.NewClient(opts)
	defer rc.Close()

	if action == "clean" {
		return cleanRedis(ctx, rc, prefix)
	}

	return seedRedis(ctx, rc, prefix)
}

func seedMongo(ctx context.Context, db *mongo.Database) error {
	people := make([]any, 0, 230)
	for i := 1; i <= 230; i++ {
		people = append(people, bson.D{
			{Key: "_id", Value: int32(i)},
			{Key: "name", Value: fmt.Sprintf("person-%03d", i)},
			{Key: "age", Value: int32(i % 90)},
			{Key: "active", Value: i%2 == 0},
			{Key: "address", Value: bson.D{{Key: "city", Value: fmt.Sprintf("city-%d", i%5)}, {Key: "zip", Value: fmt.Sprintf("%05d", i)}}},
			{Key: "tags", Value: bson.A{"a", "b", int32(i)}},
		})
	}

	people[0] = bson.D{
		{Key: "_id", Value: int32(1)}, {Key: "name", Value: "ada"}, {Key: "age", Value: int32(36)}, {Key: "active", Value: true},
		{Key: "address", Value: bson.D{{Key: "city", Value: "london"}, {Key: "zip", Value: "00001"}}},
		{Key: "tags", Value: bson.A{"math", "engines"}},
	}

	if _, err := db.Collection("people").InsertMany(ctx, people); err != nil {
		return err
	}

	oid1, _ := bson.ObjectIDFromHex("65a1b2c3d4e5f60718293a4b")
	oid2, _ := bson.ObjectIDFromHex("65a1b2c3d4e5f60718293a4c")
	oid3, _ := bson.ObjectIDFromHex("65a1b2c3d4e5f60718293a4d")

	events := []any{
		bson.D{{Key: "_id", Value: oid1}, {Key: "kind", Value: "login"}, {Key: "at", Value: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}},
		bson.D{{Key: "_id", Value: oid2}, {Key: "kind", Value: "logout"}, {Key: "at", Value: time.Date(2026, 1, 2, 4, 4, 5, 0, time.UTC)}},
		bson.D{{Key: "_id", Value: oid3}, {Key: "kind", Value: "edit-me"}, {Key: "note", Value: "before"}},
	}

	if _, err := db.Collection("events").InsertMany(ctx, events); err != nil {
		return err
	}

	// A document whose text is markup: the interface must show it as text.
	_, err := db.Collection("xss").InsertOne(ctx, bson.D{
		{Key: "_id", Value: int32(1)},
		{Key: "html", Value: "<script>window.__xss = 1</script><img src=x onerror=\"window.__xss = 1\">"},
	})

	return err
}

func seedRedis(ctx context.Context, rc *goredis.Client, p string) error {
	pipe := rc.Pipeline()

	for i := 1; i <= 450; i++ {
		pipe.Set(ctx, fmt.Sprintf("%sscan:%03d", p, i), fmt.Sprintf("v%d", i), 0)
	}

	pipe.Set(ctx, p+"str", "hello", 0)
	pipe.Set(ctx, p+"bin", "\xff\x00\x01abc", 0)
	pipe.Set(ctx, p+"ttl", "soon", 3600*time.Second)
	pipe.Set(ctx, p+"edit", "before", 0)
	pipe.Set(ctx, p+"gone", "bye", 0)
	pipe.HSet(ctx, p+"hash", "a", "1", "b", "2", "c", "three")
	pipe.HSet(ctx, p+"hash-edit", "f", "before")

	for i := 1; i <= 250; i++ {
		pipe.HSet(ctx, p+"hash-big", fmt.Sprintf("f%03d", i), fmt.Sprintf("v%d", i))
	}

	pipe.RPush(ctx, p+"list", "one", "two", "three")
	pipe.SAdd(ctx, p+"set", "x", "y", "z")
	pipe.ZAdd(ctx, p+"zset", goredis.Z{Score: 1.5, Member: "low"}, goredis.Z{Score: 9, Member: "high"})
	pipe.XAdd(ctx, &goredis.XAddArgs{Stream: p + "stream", ID: "1-0", Values: map[string]any{"k": "v1"}})
	pipe.XAdd(ctx, &goredis.XAddArgs{Stream: p + "stream", ID: "2-0", Values: map[string]any{"k": "v2"}})

	_, err := pipe.Exec(ctx)

	return err
}

func cleanRedis(ctx context.Context, rc *goredis.Client, p string) error {
	var cursor uint64

	for {
		keys, next, err := rc.Scan(ctx, cursor, p+"*", 500).Result()
		if err != nil {
			return err
		}

		if len(keys) > 0 {
			if err := rc.Del(ctx, keys...).Err(); err != nil {
				return err
			}
		}

		if next == 0 {
			return nil
		}

		cursor = next
	}
}
