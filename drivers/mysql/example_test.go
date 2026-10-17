package mysql_test

import (
	"fmt"
	"log"

	"github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/drivers/mysql"
)

// An app registers the driver once; DATABASE_URL then selects it by scheme.
func Example() {
	// DATABASE_URL=mysql://user:pass@localhost:3306/app
	app, err := vukem.New(vukem.WithDatabaseDrivers(mysql.Driver()))
	if err != nil {
		log.Fatal(err)
	}

	db, err := app.DB()
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(db.Dialect().Placeholder(1))
}

func ExampleDriver() {
	d := mysql.Driver()
	fmt.Println(d.SQLName, d.Schemes, d.Dialect.Placeholder(2))
	// Output: mysql [mysql mariadb] ?
}

// Remote hosts require a verified TLS certificate unless the URL says
// otherwise; loopback hosts connect in plain text.
func ExampleDSN() {
	dsn, err := mysql.DSN("mysql://app:secret@db.example.com/app")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(dsn)
	// Output: app:secret@tcp(db.example.com:3306)/app?clientFoundRows=true&charset=utf8mb4&parseTime=true&timeout=10s&tls=true&time_zone=%27%2B00%3A00%27
}
