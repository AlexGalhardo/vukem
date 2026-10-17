package database_test

import (
	"context"
	"errors"
	"fmt"

	"github.com/AlexGalhardo/vukem/database"
)

func ExampleOpen() {
	ctx := context.Background()

	// Apps call app.DB(), which reads DATABASE_URL; ":memory:" is SQLite.
	db, err := database.Open(ctx, ":memory:")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer db.Close()

	_, _ = db.ExecContext(ctx, "CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT)")
	_, _ = db.ExecContext(ctx, "INSERT INTO notes (body) VALUES (?)", "hello")

	var body string
	_ = db.QueryRowContext(ctx, "SELECT body FROM notes").Scan(&body)
	fmt.Println(db.Dialect().Name(), body)
	// Output: sqlite hello
}

func ExampleDB_Tx() {
	ctx := context.Background()

	db, _ := database.Open(ctx, ":memory:")
	defer db.Close()

	_, _ = db.ExecContext(ctx, "CREATE TABLE accounts (id INTEGER PRIMARY KEY, balance INTEGER)")
	_, _ = db.ExecContext(ctx, "INSERT INTO accounts (balance) VALUES (100)")

	// Returning an error rolls the transaction back.
	err := db.Tx(ctx, func(tx *database.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE accounts SET balance = balance - 150"); err != nil {
			return err
		}

		return errors.New("insufficient funds")
	})

	var balance int
	_ = db.QueryRowContext(ctx, "SELECT balance FROM accounts").Scan(&balance)
	fmt.Println(err, balance)
	// Output: insufficient funds 100
}
