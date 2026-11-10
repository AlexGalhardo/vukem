// Package postgres connects Vukem to PostgreSQL through pgx. It is a
// separate module so apps that only use SQLite never download pgx.
//
//	vukem.New(vukem.WithDatabaseDrivers(postgres.Driver()))
//	// DATABASE_URL=postgres://user:pass@localhost:5432/app?sslmode=disable
//
// Once [Driver] is registered, everything built on the database package
// (ORM, schema builder, migrations, database-backed stores) speaks the
// PostgreSQL dialect.
//
// [NewBroker] is the second half of the module: an sse.Broker on
// LISTEN/NOTIFY, so server-sent events cross processes (web service to
// queue:work service) on the database the app already has.
//
//	broker, err := postgres.NewBroker(db)
//	hub := sse.NewHub(broker)
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/database.md.
package postgres

import (
	"errors"
	"net/netip"
	"net/url"
	"strings"

	// Registers the "pgx" database/sql driver.
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/AlexGalhardo/vukem/database"
)

// ErrInvalidURL is returned for a DATABASE_URL that does not parse. It never
// quotes the URL, which may hold a password.
var ErrInvalidURL = errors.New("postgres: invalid database url")

// Driver returns the PostgreSQL driver for database.Open and
// vukem.WithDatabaseDrivers. It accepts postgres:// and postgresql:// URLs;
// see [DSN] for the TLS default.
func Driver() database.Driver {
	return database.Driver{
		SQLName: "pgx", Dialect: database.Postgres, Schemes: []string{"postgres", "postgresql"}, DSN: DSN,
	}
}

// DSN returns rawURL for pgx, adding sslmode=verify-full when the host is
// not loopback (or a unix socket) and the URL has no sslmode. pgx's own
// default (prefer) falls back to plaintext silently; an explicit sslmode, for
// example sslmode=disable on a private network, always wins.
func DSN(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", ErrInvalidURL // url.Parse errors quote the URL
	}

	q := u.Query()
	host := u.Hostname()

	if h := q.Get("host"); h != "" {
		host = h
	}

	if q.Has("sslmode") || isLocal(host) {
		return rawURL, nil
	}

	if u.RawQuery == "" {
		u.RawQuery = "sslmode=verify-full"
	} else {
		u.RawQuery += "&sslmode=verify-full"
	}

	return u.String(), nil
}

// isLocal reports an empty host (libpq's default socket), a unix socket
// directory, localhost or a loopback IP.
func isLocal(host string) bool {
	if host == "" || strings.HasPrefix(host, "/") || strings.EqualFold(host, "localhost") {
		return true
	}

	ip, err := netip.ParseAddr(host)

	return err == nil && ip.IsLoopback()
}
