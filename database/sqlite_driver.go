//go:build !vukem_nosqlite

package database

import _ "modernc.org/sqlite" // pure-Go SQLite driver ("sqlite"); no cgo, works on Windows

const sqliteLinked = true
