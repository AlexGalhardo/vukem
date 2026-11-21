//go:build ignore

// Command fixture writes the SQLite database the Playwright specs browse.
// Run from contrib/studio: go run ./web/e2e/fixture/main.go <path>.
package main

import (
	"database/sql"
	"fmt"
	"os"

	_ "modernc.org/sqlite" // the driver Studio itself uses
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: fixture <path>")
		os.Exit(2)
	}

	if err := write(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, "fixture:", err)
		os.Exit(1)
	}
}

func write(path string) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()

	for _, q := range []string{
		`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL, email TEXT)`,
		`INSERT INTO users (id, name, email) VALUES (1, 'ada', 'ada@example.test'), (2, 'linus', NULL)`,
		`CREATE TABLE posts (id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users (id), title TEXT)`,
		`INSERT INTO posts (id, user_id, title) VALUES (1, 1, 'first'), (2, 1, 'second'), (3, 2, 'third')`,
	} {
		if _, err := db.Exec(q); err != nil {
			return err
		}
	}

	return grid(db)
}

// grid adds the tables the data grid specs use: a large one for scrolling and
// performance, one with every column type for editing, and one without a
// primary key.
func grid(db *sql.DB) error {
	for _, q := range []string{
		`CREATE TABLE events (id INTEGER PRIMARY KEY, kind TEXT NOT NULL, amount REAL, active BOOLEAN, at DATETIME,
			payload JSON, note TEXT)`,
		`WITH RECURSIVE seq(n) AS (SELECT 1 UNION ALL SELECT n + 1 FROM seq WHERE n < 100000)
			INSERT INTO events (id, kind, amount, active, at, payload, note)
			SELECT n, 'kind-' || (n % 10), n * 1.5, n % 2, datetime('2026-01-01', '+' || n || ' minutes'),
				'{"n":' || n || ',"tags":["a","b"]}',
				CASE WHEN n % 7 = 0 THEN NULL WHEN n % 1000 = 0 THEN printf('%0300d', n) ELSE 'note ' || n END
			FROM seq`,
		`CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT NOT NULL, qty INTEGER NOT NULL DEFAULT 1, note TEXT,
			price NUMERIC, active BOOLEAN, born DATE, meta JSON, raw BLOB)`,
		`INSERT INTO items (id, name, qty, note, price, active, born, meta, raw) VALUES
			(1, 'bolt', 5, 'first', 1.25, 1, '2026-03-04', '{"k":1}', x'ff00'),
			(2, 'nut', 7, NULL, 0.5, 0, '2026-03-05', NULL, NULL),
			(3, 'washer', 9, 'third', 0.1, NULL, NULL, '[1,2]', NULL),
			(4, 'edit-me', 1, 'before', 2, 1, NULL, NULL, NULL),
			(5, 'conflict-me', 1, 'before', 2, 1, NULL, NULL, NULL),
			(6, 'delete-me', 1, 'x', 2, 1, NULL, NULL, NULL),
			(7, 'null-me', 1, 'has value', 2, 1, NULL, NULL, NULL),
			(8, 'big', 1, printf('%070000d', 1), 2, 1, NULL, NULL, NULL)`,
		`CREATE TABLE audit_lines (line TEXT, at TEXT)`,
		`INSERT INTO audit_lines VALUES ('one', '2026-01-01'), ('two', '2026-01-02'), ('three', NULL)`,
		`CREATE VIEW audit_view AS SELECT line FROM audit_lines`,
	} {
		if _, err := db.Exec(q); err != nil {
			return err
		}
	}

	return nil
}
