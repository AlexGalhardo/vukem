package postgres_test

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/drivers/postgres"
	"github.com/AlexGalhardo/vukem/sse"
)

// An app registers the driver once; DATABASE_URL then selects it by scheme.
func Example() {
	// DATABASE_URL=postgres://user:pass@localhost:5432/app?sslmode=disable
	app, err := vukem.New(vukem.WithDatabaseDrivers(postgres.Driver()))
	if err != nil {
		log.Fatal(err)
	}

	db, err := app.DB()
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(db.Dialect().Placeholder(1))
}

// Outside an app, pass the driver to database.Open.
func Example_open() {
	ctx := context.Background()

	db, err := database.Open(ctx, os.Getenv("DATABASE_URL"), database.WithDriver(postgres.Driver()))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	if err := db.Ping(ctx); err != nil {
		log.Print(err)
	}
}

// The broker carries events between processes, so the queue:work service can
// stream progress to a browser connected to the web service. Both processes
// build the hub the same way, on the same database.
func ExampleNewBroker() {
	ctx := context.Background()

	db, err := database.Open(ctx, os.Getenv("DATABASE_URL"), database.WithDriver(postgres.Driver()))
	if err != nil {
		log.Fatal(err)
	}
	// Not PostgreSQL: the broker would accept every publish and deliver
	// nothing, so an app fails to boot instead.
	broker, err := postgres.NewBroker(db)
	if err != nil {
		log.Fatal(err)
	}

	defer db.Close()

	hub := sse.NewHub(broker)
	defer hub.Close()

	// In the worker: progress for one conversion, at most postgres.MaxNotifyPayload bytes.
	if err := hub.Publish(ctx, "conversions:42", sse.Event{Event: "progress", Data: `{"percent":10}`}); err != nil {
		log.Print(err)
	}
}

func ExampleDriver() {
	d := postgres.Driver()
	fmt.Println(d.SQLName, d.Schemes, d.Dialect.Placeholder(2))
	// Output: pgx [postgres postgresql] $2
}
