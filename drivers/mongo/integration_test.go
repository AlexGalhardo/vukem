//go:build integration

package mongo_test

import (
	"crypto/rand"
	"errors"
	"os"
	"slices"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/AlexGalhardo/vukem/drivers/mongo"
)

type user struct {
	ID     bson.ObjectID `bson:"_id,omitempty"`
	Email  string        `bson:"email"`
	Age    int           `bson:"age"`
	Active bool          `bson:"active"`
}

// open connects to MONGO_URL (task services:up starts one on :57017) with
// a database unique to the test.
func open(t *testing.T) *mongo.Client {
	t.Helper()

	url := os.Getenv("MONGO_URL")
	if url == "" {
		url = "mongodb://localhost:57017"
	}

	m, err := mongo.Open(t.Context(), url, "vukemtest_"+rand.Text()[:8])
	if err != nil {
		t.Fatalf("mongo: %v (start it with task services:up)", err)
	}

	t.Cleanup(func() {
		ctx := t.Context()
		_ = m.Database().Drop(ctx)
		_ = m.Close(ctx)
	})

	return m
}

func TestCollection(t *testing.T) {
	ctx := t.Context()
	m := open(t)
	users := mongo.Collection[user](m, "users")

	id, err := users.Insert(ctx, user{Email: "ana@example.com", Age: 30, Active: true})
	if err != nil || id == nil {
		t.Fatalf("insert = %v %v", id, err)
	}

	if _, err := users.InsertMany(ctx, []user{
		{Email: "bob@example.com", Age: 25, Active: true},
		{Email: "cid@example.com", Age: 40},
	}); err != nil {
		t.Fatal(err)
	}

	u, err := users.FindOne(ctx, bson.D{{Key: "email", Value: "ana@example.com"}})
	if err != nil || u.Age != 30 || u.ID.IsZero() {
		t.Fatalf("FindOne = %+v %v", u, err)
	}

	if _, err := users.FindOne(ctx, bson.D{{Key: "email", Value: "nobody"}}); !errors.Is(err, mongo.ErrNotFound) {
		t.Fatalf("missing = %v", err)
	}

	var emails []string
	for u, err := range users.Find(ctx, bson.D{{Key: "active", Value: true}}, options.Find().SetSort(bson.D{{Key: "age", Value: 1}})) {
		if err != nil {
			t.Fatal(err)
		}

		emails = append(emails, u.Email)
	}

	if !slices.Equal(emails, []string{"bob@example.com", "ana@example.com"}) {
		t.Fatalf("Find = %v", emails)
	}

	for range users.Find(ctx, bson.D{}) {
		break // early exit closes the cursor
	}

	if n, err := users.Count(ctx, bson.D{}); err != nil || n != 3 {
		t.Fatalf("Count = %d %v", n, err)
	}

	if matched, modified, err := users.UpdateMany(
		ctx,
		bson.D{{Key: "active", Value: false}},
		bson.D{
			{Key: "$set", Value: bson.D{{Key: "active", Value: true}}},
		},
	); err != nil || matched != 1 || modified != 1 {
		t.Fatalf("UpdateMany = %d %d %v", matched, modified, err)
	}

	if _, _, err := users.UpdateOne(ctx, bson.D{{Key: "email", Value: "bob@example.com"}},
		bson.D{{Key: "$inc", Value: bson.D{{Key: "age", Value: 1}}}}); err != nil {
		t.Fatal(err)
	}

	if n, err := users.ReplaceOne(ctx, bson.D{{Key: "email", Value: "cid@example.com"}},
		user{Email: "cid@example.com", Age: 41}); err != nil || n != 1 {
		t.Fatalf("ReplaceOne = %d %v", n, err)
	}

	type ageSum struct {
		Total int `bson:"total"`
	}

	sums, err := mongo.Aggregate[ageSum](ctx, users, bson.A{
		bson.D{
			{
				Key:   "$group",
				Value: bson.D{{Key: "_id", Value: nil}, {Key: "total", Value: bson.D{{Key: "$sum", Value: "$age"}}}},
			},
		},
	})
	if err != nil || len(sums) != 1 || sums[0].Total != 30+26+41 {
		t.Fatalf("Aggregate = %+v %v", sums, err)
	}

	all, err := users.All(ctx, bson.D{})
	if err != nil || len(all) != 3 {
		t.Fatalf("All = %d %v", len(all), err)
	}

	if n, err := users.DeleteOne(ctx, bson.D{{Key: "email", Value: "ana@example.com"}}); err != nil || n != 1 {
		t.Fatalf("DeleteOne = %d %v", n, err)
	}

	if n, err := users.DeleteMany(ctx, bson.D{}); err != nil || n != 2 {
		t.Fatalf("DeleteMany = %d %v", n, err)
	}

	if users.Raw() == nil || m.Raw() == nil || m.Ping(ctx) != nil {
		t.Fatal("accessors or health check")
	}
}
