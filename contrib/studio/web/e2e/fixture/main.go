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

	return nil
}
