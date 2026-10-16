package postgres

import (
	"testing"

	"github.com/AlexGalhardo/golang-framework/database"
)

func TestDriver(t *testing.T) {
	t.Parallel()

	d := Driver()
	if d.SQLName != "pgx" || d.Dialect != database.Postgres || len(d.Schemes) != 2 {
		t.Fatalf("Driver() = %+v", d)
	}

	if d.Dialect.Placeholder(3) != "$3" {
		t.Fatal("placeholder")
	}

	// Without a server the connection check fails fast.
	_, err := database.Open(t.Context(), "postgres://nobody:x@127.0.0.1:1/none?sslmode=disable&connect_timeout=1",
		database.WithDriver(d))
	if err == nil {
		t.Fatal("Open without a server succeeded")
	}
}
