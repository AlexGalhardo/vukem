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
	name    string
	label   string
	db      *database.DB
	catalog Introspector
	// memory is a SQLite ":memory:" database, where the reader and the writer
	// are one connection and a read-only console cannot be guaranteed.
	memory bool
}

// SQLSource is a relational source: the table browser and the console.
type SQLSource interface {
	Source
	Tables(ctx context.Context) ([]Table, error)
	Table(ctx context.Context, name string) (Table, error)
	Rows(ctx context.Context, q RowQuery) (RowPage, error)
	Row(ctx context.Context, table string, key []string) (RowDetail, error)
	// Query is the console: one statement, read-only unless write is true.
	Query(ctx context.Context, sql string, write bool) (ResultSet, error)
	// Explain returns the statement that shows the plan of sql.
	Explain(sql string) string
	// References lists the foreign keys of other tables that point at table.
	References(ctx context.Context, table string) ([]Reference, error)
	// Writes, used only when Studio runs with --write. A table without a
	// primary key refuses them (ErrNotFound); a write changes exactly one row
	// or rolls back.
	Insert(ctx context.Context, table string, fields map[string]Field) error
	Update(ctx context.Context, table string, key []string, fields map[string]Field) error
	Delete(ctx context.Context, table string, key []string) error
}

// Reference is a foreign key of Table that points at another table.
type Reference struct {
	Table string
	ForeignKey
}

// NewSQL wraps an open database as the source called name, listing tables
// through catalog. Without one the source refuses every table (it fails closed).
func NewSQL(name, label string, db *database.DB, catalog Introspector) *SQLDB {
	if catalog == nil {
		catalog = noCatalog{}
	}

	return &SQLDB{name: name, label: label, db: db, catalog: catalog}
}

// noCatalog answers every request with errNoCatalog.
type noCatalog struct{}

func (noCatalog) Tables(context.Context) ([]Table, error) { return nil, errNoCatalog }
func (noCatalog) Table(context.Context, string) (Table, bool, error) {
	return Table{}, false, errNoCatalog
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

	src := NewSQL(name, Redact(rawURL), db, nil)
	src.memory = openURL == "file::memory:"

	return src, nil
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

// Explain implements SQLSource.
func (s *SQLDB) Explain(text string) string {
	if s.family() == "sqlite" {
		return "EXPLAIN QUERY PLAN " + text
	}

	return "EXPLAIN " + text
}
