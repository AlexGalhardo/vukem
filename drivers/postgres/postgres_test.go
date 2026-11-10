package postgres

import (
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/database"
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

func TestSecurity_A04_PostgresRemoteDefaultsToVerifiedTLS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, in, want string
	}{
		{"loopback name", "postgres://u:p@localhost:5432/app", "postgres://u:p@localhost:5432/app"},
		{
			"loopback ip",
			"postgres://u:p@127.0.0.1/app?application_name=x",
			"postgres://u:p@127.0.0.1/app?application_name=x",
		},
		{"loopback v6", "postgresql://u:p@[::1]:5432/app", "postgresql://u:p@[::1]:5432/app"},
		{
			"unix socket host",
			"postgres://u@/app?host=/var/run/postgresql",
			"postgres://u@/app?host=/var/run/postgresql",
		},
		{
			"remote default",
			"postgres://u:p@db.example.com:5432/app",
			"postgres://u:p@db.example.com:5432/app?sslmode=verify-full",
		},
		{
			"remote with query",
			"postgres://u:p@db.example.com/app?connect_timeout=3",
			"postgres://u:p@db.example.com/app?connect_timeout=3&sslmode=verify-full",
		},
		{
			"remote host param",
			"postgres://u@/app?host=db.example.com",
			"postgres://u@/app?host=db.example.com&sslmode=verify-full",
		},
		{
			"remote explicit disable",
			"postgres://u:p@db.internal/app?sslmode=disable",
			"postgres://u:p@db.internal/app?sslmode=disable",
		},
		{
			"remote explicit require",
			"postgres://u:p@db.internal/app?sslmode=require",
			"postgres://u:p@db.internal/app?sslmode=require",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := DSN(tt.in)
			if err != nil {
				t.Fatalf("DSN(%q): %v", tt.in, err)
			}

			if got != tt.want {
				t.Errorf("DSN(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}

	// Errors never echo the URL, which may hold a password.
	for _, bad := range []string{"postgres://u:hunter2@db.example.com:notaport/app", "postgres://u:hunter2@%zz/app"} {
		_, err := DSN(bad)
		if err == nil {
			t.Fatalf("DSN(%q) succeeded", bad)
		}

		if strings.Contains(err.Error(), "hunter2") || strings.Contains(err.Error(), "db.example.com") {
			t.Errorf("error echoes the URL: %v", err)
		}
	}

	if Driver().DSN == nil {
		t.Error("Driver() does not set the DSN hook")
	}
}
