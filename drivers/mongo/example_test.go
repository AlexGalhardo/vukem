package mongo_test

import (
	"context"
	"errors"
	"fmt"
	"os"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/AlexGalhardo/vukem/drivers/mongo"
)

type User struct {
	ID     bson.ObjectID `bson:"_id,omitempty"`
	Email  string        `bson:"email"`
	Active bool          `bson:"active"`
}

func Example() {
	ctx := context.Background()

	m, err := mongo.Open(ctx, os.Getenv("MONGO_URL")) // e.g. mongodb on localhost:27017, database "app"
	if err != nil {
		panic(err)
	}
	defer m.Close(ctx)

	users := mongo.Collection[User](m, "users")

	if _, err := users.Insert(ctx, User{Email: "ada@example.com", Active: true}); err != nil {
		panic(err)
	}

	// Filters are built from validated values, never from request JSON.
	u, err := users.FindOne(ctx, bson.D{{Key: "email", Value: "ada@example.com"}})
	if errors.Is(err, mongo.ErrNotFound) {
		fmt.Println("no such user")
		return
	} else if err != nil {
		panic(err)
	}

	fmt.Println(u.Email)

	for u, err := range users.Find(ctx, bson.D{{Key: "active", Value: true}}) {
		if err != nil {
			panic(err)
		}

		fmt.Println(u.Email)
	}
}
