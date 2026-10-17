//go:build vukem_nosqlite

package database

// The vukem_nosqlite build tag leaves the SQLite driver (about 4 MB) out
// of apps that never open a SQLite database; Open then refuses SQLite URLs.
const sqliteLinked = false
