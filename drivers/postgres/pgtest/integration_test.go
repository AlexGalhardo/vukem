//go:build integration

package pgtest_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/drivers/postgres"
	"github.com/AlexGalhardo/vukem/drivers/postgres/pgtest"
	"github.com/AlexGalhardo/vukem/vukemtest"
)

// admin is POSTGRES_URL or the compose service (task services:up).
func admin() string {
	if u := os.Getenv("POSTGRES_URL"); u != "" {
		return u
	}

	return "postgres://vukem:vukem@localhost:55432/vukem_test?sslmode=disable"
}

func exists(t *testing.T, name string) bool {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), admin())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())

	var n int
	if err := conn.QueryRow(t.Context(), "SELECT count(*) FROM pg_database WHERE datname = $1", name).
		Scan(&n); err != nil {
		t.Fatal(err)
	}

	return n == 1
}

func TestDatabase(t *testing.T) {
	var name string

	t.Run("create", func(t *testing.T) {
		url := pgtest.Database(t, admin())

		// An open connection must not block the drop in Cleanup.
		conn, err := pgx.Connect(t.Context(), url)
		if err != nil {
			t.Fatalf("connect to %s: %v", url, err)
		}

		if err := conn.QueryRow(t.Context(), "SELECT current_database()").Scan(&name); err != nil {
			t.Fatal(err)
		}

		if _, err := conn.Exec(t.Context(), "CREATE TABLE posts (id int)"); err != nil {
			t.Fatal(err)
		}

		if !exists(t, name) {
			t.Fatalf("database %q not found", name)
		}

		other := pgtest.Database(t, admin())
		if other == url {
			t.Fatalf("Database returned %q twice", url)
		}
	})

	if name == "" {
		t.Fatal("subtest did not report the database name")
	}

	if exists(t, name) {
		t.Errorf("database %q still exists after Cleanup", name)
	}
}

func TestDatabase_EnvAdmin(t *testing.T) {
	t.Setenv("TEST_DATABASE_URL", admin())

	conn, err := pgx.Connect(t.Context(), pgtest.Database(t, ""))
	if err != nil {
		t.Fatal(err)
	}

	_ = conn.Close(t.Context())
}

func TestDatabase_Vukemtest(t *testing.T) {
	app := vukemtest.New(t, vukem.New, vukemtest.Options{
		Env:        map[string]string{"DATABASE_URL": pgtest.Database(t, admin())},
		AppOptions: []vukem.Option{vukem.WithDatabaseDrivers(postgres.Driver())},
	})

	db, err := app.DB()
	if err != nil {
		t.Fatal(err)
	}

	if got := db.Dialect().Name(); got != "postgres" {
		t.Fatalf("Dialect().Name() = %q, want postgres", got)
	}

	if _, err := db.ExecContext(t.Context(), "CREATE TABLE posts (id serial PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
}
