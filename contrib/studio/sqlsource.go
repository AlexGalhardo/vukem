package studio

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/drivers/mysql"
	"github.com/AlexGalhardo/vukem/drivers/postgres"
)

// SQLDB is the SQL source: SQLite, PostgreSQL or MySQL/MariaDB, opened with
// the framework's database package. The table browser, grid and console
// build on it.
type SQLDB struct {
	name  string
	label string
	db    *database.DB
}

// OpenSQL opens the database of rawURL as the source called name. A SQLite
// file is resolved against dir and must exist (Studio never creates one).
// Errors never quote the URL, which may hold a password.
func OpenSQL(ctx context.Context, name, dir, rawURL string) (*SQLDB, error) {
	openURL := rawURL

	if p, ok := SQLitePath(dir, rawURL); ok {
		if p != ":memory:" {
			if _, err := os.Stat(p); err != nil {
				return nil, errors.New("studio: the SQLite database file does not exist")
			}
		}

		openURL = "file:" + p
	}

	db, err := database.Open(ctx, openURL,
		database.WithDriver(postgres.Driver()), database.WithDriver(mysql.Driver()))
	if err != nil {
		// database.Open errors do not echo the URL, but a driver's may.
		return nil, fmt.Errorf("studio: cannot open the database (%s): %w", Redact(rawURL), errNoURL(err, rawURL))
	}

	return &SQLDB{name: name, label: Redact(rawURL), db: db}, nil
}

// errNoURL hides rawURL if an error message happens to carry it.
func errNoURL(err error, rawURL string) error {
	if rawURL == "" {
		return err
	}

	msg := err.Error()
	if redacted := strings.ReplaceAll(msg, rawURL, "[url]"); redacted != msg {
		return errors.New(redacted)
	}

	return err
}

// Name implements Source.
func (s *SQLDB) Name() string { return s.name }

// Kind implements Source.
func (*SQLDB) Kind() Kind { return KindSQL }

// Label implements Source: driver, host and database, never credentials.
func (s *SQLDB) Label() string { return s.label }

// Ping implements Source.
func (s *SQLDB) Ping(ctx context.Context) error { return s.db.SQL().PingContext(ctx) }

// Close implements Source.
func (s *SQLDB) Close() error { return s.db.Close() }

// DB is the connection, for the browser built on this source.
func (s *SQLDB) DB() *database.DB { return s.db }
