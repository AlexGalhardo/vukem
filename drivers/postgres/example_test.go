package postgres_test

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/drivers/postgres"
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

func ExampleDriver() {
	d := postgres.Driver()
	fmt.Println(d.SQLName, d.Schemes, d.Dialect.Placeholder(2))
	// Output: pgx [postgres postgresql] $2
}
