package mysql

import (
	"errors"
	"strings"
	"testing"
	"time"

	gomysql "github.com/go-sql-driver/mysql"
	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/database"
)

func TestDriver(t *testing.T) {
	t.Parallel()

	d := Driver()
	if d.SQLName != "mysql" || d.Dialect != database.MySQL || d.DSN == nil ||
		!cmp.Equal(d.Schemes, []string{"mysql", "mariadb"}) {
		t.Fatalf("Driver() = %+v", d)
	}

	// Without a server the connection check fails fast.
	_, err := database.Open(t.Context(), "mysql://nobody:x@127.0.0.1:1/none?timeout=1s", database.WithDriver(d))
	if err == nil {
		t.Fatal("Open without a server succeeded")
	}
}

func TestDSN(t *testing.T) {
	t.Parallel()

	const base = "?clientFoundRows=true&charset=utf8mb4&parseTime=true&timeout=10s"

	tests := []struct {
		name string
		url  string
		want string
	}{
		{
			"localhost defaults",
			"mysql://app:secret@localhost/app",
			"app:secret@tcp(localhost:3306)/app" + base + "&tls=false&time_zone=%27%2B00%3A00%27",
		},
		{
			"mariadb scheme and port",
			"mariadb://app:secret@127.0.0.1:3307/app",
			"app:secret@tcp(127.0.0.1:3307)/app" + base + "&tls=false&time_zone=%27%2B00%3A00%27",
		},
		{
			"remote host requires verified tls",
			"mysql://app:secret@db.example.com/app",
			"app:secret@tcp(db.example.com:3306)/app" + base + "&tls=true&time_zone=%27%2B00%3A00%27",
		},
		{
			"explicit tls and overrides win",
			"mysql://app@db.example.com/app?tls=skip-verify&timeout=3s&parseTime=false",
			"app@tcp(db.example.com:3306)/app?clientFoundRows=true&charset=utf8mb4" +
				"&timeout=3s&tls=skip-verify&time_zone=%27%2B00%3A00%27",
		},
		{
			"ipv6 loopback",
			"mysql://app:secret@[::1]:3306/app",
			"app:secret@tcp([::1]:3306)/app" + base + "&tls=false&time_zone=%27%2B00%3A00%27",
		},
		{
			"escaped password",
			"mysql://app:p%40ss%3Aw%2Frd@localhost/app",
			"app:p@ss:w/rd@tcp(localhost:3306)/app" + base + "&tls=false&time_zone=%27%2B00%3A00%27",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := DSN(tt.url)
			if err != nil {
				t.Fatalf("DSN(%q) error = %v", tt.url, err)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Fatalf("DSN(%q) mismatch (-want +got):\n%s", tt.url, diff)
			}
		})
	}
}

func TestDSNRoundTrip(t *testing.T) {
	t.Parallel()

	dsn, err := DSN("mysql://app:p%40ss%3Aw%2Frd@localhost:3306/app")
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := gomysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.User != "app" || cfg.Passwd != "p@ss:w/rd" || cfg.Addr != "localhost:3306" || cfg.DBName != "app" ||
		!cfg.ParseTime || cfg.Loc != time.UTC || !cfg.ClientFoundRows || cfg.Timeout != 10*time.Second ||
		cfg.Params["time_zone"] != "'+00:00'" {
		t.Fatalf("ParseDSN(DSN(...)) = %+v", cfg)
	}
}

func TestDSNErrors(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		"mysql://app:hunter2@/app",
		"postgres://app:hunter2@localhost/app",
		"mysql://app:hunter2@localhost/app/extra",
		"mysql://app:hunter2@localhost/app?timeout=forever",
		"mysql://app:hunter2@local host/app",
	} {
		_, err := DSN(raw)
		if !errors.Is(err, ErrInvalidURL) {
			t.Errorf("DSN(%q) = %v, want ErrInvalidURL", raw, err)
		}

		if err != nil && strings.Contains(err.Error(), "hunter2") {
			t.Errorf("DSN(%q) error leaks the password: %v", raw, err)
		}
	}
}

// TestSecurity_A04_MySQLRemoteRequiresTLS: without tls= in the URL, any
// non-loopback host gets verified TLS; only loopback stays plain.
func TestSecurity_A04_MySQLRemoteRequiresTLS(t *testing.T) {
	t.Parallel()

	for host, want := range map[string]string{
		"localhost":        "false",
		"127.0.0.1":        "false",
		"[::1]":            "false",
		"db":               "true",
		"10.0.0.5":         "true",
		"db.example.com":   "true",
		"localhost.evil":   "true",
		"[2001:db8::1]":    "true",
		"127.0.0.1.nip.io": "true",
	} {
		dsn, err := DSN("mysql://u:p@" + host + "/app")
		if err != nil {
			t.Fatalf("DSN(%s) error = %v", host, err)
		}

		cfg, err := gomysql.ParseDSN(dsn)
		if err != nil {
			t.Fatal(err)
		}

		got := "false"
		if cfg.TLSConfig != "" && cfg.TLSConfig != "false" {
			got = cfg.TLSConfig
		}

		if got != want {
			t.Errorf("DSN(%s) tls = %q, want %q", host, got, want)
		}
	}
}
