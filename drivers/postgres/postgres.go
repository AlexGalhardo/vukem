// Package postgres connects Vukem to PostgreSQL through pgx. It is a
// separate module so apps that only use SQLite never download pgx.
//
//	vukem.New(vukem.WithDatabaseDrivers(postgres.Driver()))
//	// DATABASE_URL=postgres://user:pass@localhost:5432/app?sslmode=disable
package postgres

import (
	// Registers the "pgx" database/sql driver.
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/AlexGalhardo/vukem/database"
)

// Driver returns the PostgreSQL driver for database.Open and
// vukem.WithDatabaseDrivers. It accepts postgres:// and postgresql:// URLs.
func Driver() database.Driver {
	return database.Driver{SQLName: "pgx", Dialect: database.Postgres, Schemes: []string{"postgres", "postgresql"}}
}
