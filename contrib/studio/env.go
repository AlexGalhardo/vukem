package studio

import (
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/AlexGalhardo/vukem/config"
)

// DefaultDatabaseURL is the framework's default (database.go in the core).
const DefaultDatabaseURL = "file:storage/database.sqlite"

// Env is what Studio reads from the project's .env files and the process
// environment.
type Env struct {
	// Production reports APP_ENV=production. An unset APP_ENV counts as
	// production, as in the framework.
	Production bool
	// DatabaseURL is DATABASE_URL, or the SQLite default.
	DatabaseURL string
}

// LoadEnv reads the .env files of the project in dir, merged with the
// environment getenv gives (os.LookupEnv in production code).
func LoadEnv(dir string, getenv func(string) (string, bool)) (Env, error) {
	src, err := config.LoadSource(os.DirFS(dir), getenv, os.ReadFile)
	if err != nil {
		return Env{}, err
	}

	e := Env{Production: src.Env() == config.Production, DatabaseURL: src.Get("DATABASE_URL")}
	if e.DatabaseURL == "" {
		e.DatabaseURL = DefaultDatabaseURL
	}

	return e, nil
}

// Redact returns what is safe to show of a database URL: the driver, and the
// host and database or the file. User, password and query string never
// appear. A URL it cannot read is shown as "database".
func Redact(rawURL string) string {
	switch {
	case rawURL == ":memory:":
		return "sqlite  :memory:"
	case strings.HasPrefix(rawURL, "file:"), strings.HasPrefix(rawURL, "sqlite:"):
		p := rawURL[strings.Index(rawURL, ":")+1:]
		p = strings.TrimPrefix(p, "//")
		p, _, _ = strings.Cut(p, "?")

		return "sqlite  " + p
	}

	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" {
		return "database"
	}

	driver := u.Scheme
	if driver == "postgresql" {
		driver = "postgres"
	}

	if driver == "mariadb" {
		driver = "mysql"
	}

	return driver + "  " + u.Host + "/" + strings.TrimPrefix(path.Clean("/"+u.Path), "/")
}

// SQLitePath resolves the file of a sqlite URL against dir, and reports
// whether url is a SQLite URL.
func SQLitePath(dir, rawURL string) (string, bool) {
	if rawURL == ":memory:" {
		return rawURL, true
	}

	prefix := ""

	switch {
	case strings.HasPrefix(rawURL, "file:"):
		prefix = "file:"
	case strings.HasPrefix(rawURL, "sqlite:"):
		prefix = "sqlite:"
	default:
		return "", false
	}

	p, _, _ := strings.Cut(strings.TrimPrefix(rawURL, prefix), "?")
	p = strings.TrimPrefix(p, "//")

	if filepath.IsAbs(p) {
		return filepath.Clean(p), true
	}

	return filepath.Join(dir, filepath.FromSlash(p)), true
}
