// Package mysql connects Vukem to MySQL 8 and MariaDB 11 through
// go-sql-driver/mysql. It is a separate module so apps that do not use
// MySQL never download the driver.
//
//	vukem.New(vukem.WithDatabaseDrivers(mysql.Driver()))
//	// DATABASE_URL=mysql://user:pass@localhost:3306/app
package mysql

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"

	// Also registers the "mysql" database/sql driver.
	gomysql "github.com/go-sql-driver/mysql"

	"github.com/AlexGalhardo/vukem/database"
)

// Driver returns the MySQL/MariaDB driver for database.Open and
// vukem.WithDatabaseDrivers. It accepts mysql:// and mariadb:// URLs.
func Driver() database.Driver {
	return database.Driver{
		SQLName: "mysql",
		Dialect: database.MySQL,
		Schemes: []string{"mysql", "mariadb"},
		DSN:     DSN,
	}
}

// ErrInvalidURL is returned (wrapped) for URLs DSN cannot translate. The
// URL itself is never part of the error because it may hold a password.
var ErrInvalidURL = errors.New("mysql: invalid url")

// defaults are the connection parameters DSN sets unless the URL
// overrides them: times scan into time.Time in UTC (also the session time
// zone, so CURRENT_TIMESTAMP agrees), utf8mb4 text, a 10s dial timeout and
// "rows matched" affected counts, as on SQLite and PostgreSQL.
var defaults = [][2]string{
	{"parseTime", "true"},
	{"loc", "UTC"},
	{"time_zone", "'+00:00'"},
	{"charset", "utf8mb4"},
	{"timeout", "10s"},
	{"clientFoundRows", "true"},
}

// DSN translates mysql://user:pass@host:port/dbname?param=value into a
// go-sql-driver data source name. Query parameters are passed through to
// the driver and override the defaults above. Without tls=, connections to
// loopback hosts are plain and every other host requires TLS with a
// verified certificate (tls=true); set tls=false, preferred, skip-verify or
// a registered config name to change that.
func DSN(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", ErrInvalidURL // url.Parse errors quote the URL
	}

	if u.Scheme != "mysql" && u.Scheme != "mariadb" {
		return "", fmt.Errorf("%w: scheme must be mysql or mariadb", ErrInvalidURL)
	}

	host := u.Hostname()
	if host == "" {
		return "", fmt.Errorf("%w: missing host", ErrInvalidURL)
	}

	port := u.Port()
	if port == "" {
		port = "3306"
	}

	name := strings.TrimPrefix(u.Path, "/")
	if strings.Contains(name, "/") {
		return "", fmt.Errorf("%w: the path must be a single database name", ErrInvalidURL)
	}

	params := url.Values{}
	for _, kv := range defaults {
		params.Set(kv[0], kv[1])
	}

	if isLoopback(host) {
		params.Set("tls", "false")
	} else {
		params.Set("tls", "true")
	}

	for k, v := range u.Query() {
		params.Set(k, v[len(v)-1])
	}

	cfg, err := gomysql.ParseDSN("tcp(" + net.JoinHostPort(host, port) + ")/" + name + "?" + params.Encode())
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidURL, err)
	}

	cfg.User = u.User.Username()
	cfg.Passwd, _ = u.User.Password()

	return cfg.FormatDSN(), nil
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}

	ip, err := netip.ParseAddr(host)

	return err == nil && ip.IsLoopback()
}
