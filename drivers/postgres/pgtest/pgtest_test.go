package pgtest

import (
	"testing"
	"time"
)

func TestDropTimeoutFrom(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"", defaultDropTimeout, false},
		{"2m", 2 * time.Minute, false},
		{"90s", 90 * time.Second, false},
		{"abc", 0, true},
		{"0s", 0, true},
		{"-5s", 0, true},
		{"30", 0, true},
	}

	for _, tt := range tests {
		got, err := dropTimeoutFrom(func(string) string { return tt.in })
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("dropTimeoutFrom(%q) = %v, %v; want %v, err=%v", tt.in, got, err, tt.want, tt.wantErr)
		}
	}
}

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

	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	a, b := newName(now), newName(now)

	if !nameRe.MatchString(a) || len(a) > 63 {
		t.Errorf("newName() = %q, want match %s within 63 bytes", a, nameRe)
	}

	if a == b {
		t.Errorf("newName() returned %q twice", a)
	}
}

func TestOrphan(t *testing.T) {
	t.Parallel()

	now := time.Now()
	old := newName(now.Add(-7 * time.Hour))

	tests := []struct {
		name string
		db   string
		want bool
	}{
		{"old generated", old, true},
		{"fresh generated", newName(now.Add(-time.Hour)), false},
		{"legacy name without time", "pgtest_abcdefghijklmnop", false},
		{"other database", "postgres", false},
		{"generated shape with suffix", old + "_x", false},
		{"similar prefix", "pgtestx" + old[len("pgtest"):], false},
	}

	for _, tt := range tests {
		if got := orphan(tt.db, now, defaultSweepAfter); got != tt.want {
			t.Errorf("%s: orphan(%q) = %v, want %v", tt.name, tt.db, got, tt.want)
		}
	}
}

func TestSweepAfterFrom(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"", defaultSweepAfter, false},
		{"0", 0, false},
		{"90m", 90 * time.Minute, false},
		{"abc", 0, true},
		{"-1h", 0, true},
		{"6", 0, true},
	}

	for _, tt := range tests {
		got, err := sweepAfterFrom(func(string) string { return tt.in })
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("sweepAfterFrom(%q) = %v, %v; want %v, err=%v", tt.in, got, err, tt.want, tt.wantErr)
		}
	}
}
