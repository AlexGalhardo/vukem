package pgtest

import (
	"regexp"
	"testing"
)

func TestAdminURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"none", nil, ""},
		{"postgres url", map[string]string{"POSTGRES_URL": "postgres://p"}, "postgres://p"},
		{
			"test url wins",
			map[string]string{"TEST_DATABASE_URL": "postgres://t", "POSTGRES_URL": "postgres://p"},
			"postgres://t",
		},
		{
			"empty test url falls back",
			map[string]string{"TEST_DATABASE_URL": "", "POSTGRES_URL": "postgres://p"},
			"postgres://p",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := adminURLFrom(func(k string) string { return tt.env[k] })
			if got != tt.want {
				t.Errorf("adminURLFrom() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWithDatabase(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		admin   string
		want    string
		wantErr bool
	}{
		{
			"replaces database and keeps query",
			"postgres://u:p@localhost:5432/postgres?sslmode=disable",
			"postgres://u:p@localhost:5432/pgtest_x?sslmode=disable",
			false,
		},
		{"adds database", "postgresql://u@db", "postgresql://u@db/pgtest_x", false},
		{"keyword dsn", "host=localhost dbname=postgres", "", true},
		{"other scheme", "mysql://u@db/app", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := withDatabase(tt.admin, "pgtest_x")
			if (err != nil) != tt.wantErr {
				t.Fatalf("withDatabase(%q) error = %v, wantErr %v", tt.admin, err, tt.wantErr)
			}

			if got != tt.want {
				t.Errorf("withDatabase(%q) = %q, want %q", tt.admin, got, tt.want)
			}
		})
	}
}

func TestNewName(t *testing.T) {
	t.Parallel()

	valid := regexp.MustCompile(`^pgtest_[a-z0-9]{16}$`)
	a, b := newName(), newName()

	if !valid.MatchString(a) {
		t.Errorf("newName() = %q, want match %s", a, valid)
	}

	if a == b {
		t.Errorf("newName() returned %q twice", a)
	}
}
