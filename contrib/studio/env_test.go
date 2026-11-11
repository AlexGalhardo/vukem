package studio

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"file:storage/database.sqlite":                      "sqlite  storage/database.sqlite",
		"sqlite:storage/app.db?cache=shared":                "sqlite  storage/app.db",
		":memory:":                                          "sqlite  :memory:",
		"postgres://app:s3cret@db.internal:5432/app":        "postgres  db.internal:5432/app",
		"postgresql://app@localhost/app?sslmode=disable":    "postgres  localhost/app",
		"mysql://app:s3cret@db.internal:3306/shop?tls=true": "mysql  db.internal:3306/shop",
		"mariadb://u:p@host/db":                             "mysql  host/db",
		"postgres://host":                                   "postgres  host/",
		"not a url at all":                                  "database",
		"postgres://app:s3cret@db.internal:5432/app#frag":   "postgres  db.internal:5432/app",
		"mysql://app:p%40ss@db:3306/app?password=queryleak": "mysql  db:3306/app",
		"postgres://a:b@c:d/e":                              "database",
	}

	for in, want := range tests {
		got := Redact(in)
		if got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}

		for _, secret := range []string{"s3cret", "p%40ss", "queryleak"} {
			if strings.Contains(got, secret) {
				t.Errorf("Redact(%q) = %q leaks %q", in, got, secret)
			}
		}
	}
}

func TestSQLitePath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	tests := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"file:storage/database.sqlite", filepath.Join(dir, "storage", "database.sqlite"), true},
		{"sqlite:a.db?mode=ro", filepath.Join(dir, "a.db"), true},
		{":memory:", ":memory:", true},
		{"postgres://h/db", "", false},
	}

	for _, tt := range tests {
		got, ok := SQLitePath(dir, tt.in)
		if got != tt.want || ok != tt.wantOK {
			t.Errorf("SQLitePath(%q) = %q, %v, want %q, %v", tt.in, got, ok, tt.want, tt.wantOK)
		}
	}
}

func TestLoadEnv(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(dir, ".env"),
		[]byte("APP_ENV=development\nDATABASE_URL=file:x/y.db\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	noEnv := func(string) (string, bool) { return "", false }

	e, err := LoadEnv(dir, noEnv)
	if err != nil || e.Production || e.DatabaseURL != "file:x/y.db" {
		t.Errorf("LoadEnv = %+v, %v", e, err)
	}

	// The process environment wins over the file.
	e, err = LoadEnv(dir, func(k string) (string, bool) {
		if k == "APP_ENV" {
			return "production", true
		}

		return "", false
	})
	if err != nil || !e.Production {
		t.Errorf("LoadEnv with APP_ENV=production = %+v, %v", e, err)
	}

	// No .env and no APP_ENV is production, and the database defaults.
	e, err = LoadEnv(t.TempDir(), noEnv)
	if err != nil || !e.Production || e.DatabaseURL != DefaultDatabaseURL {
		t.Errorf("LoadEnv(empty dir) = %+v, %v", e, err)
	}
}
