package bench

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/orm"
)

type Account struct {
	orm.Model
	Name  string `db:"name"`
	Email string `db:"email"`
}

const (
	accountCols   = "id, created_at, updated_at, name, email"
	accountSchema = `CREATE TABLE accounts (id INTEGER PRIMARY KEY AUTOINCREMENT,
		created_at TIMESTAMP, updated_at TIMESTAMP, name TEXT, email TEXT)`
)

// openAccounts opens a fresh in-memory SQLite database with rows accounts.
func openAccounts(b *testing.B, rows int) *database.DB {
	b.Helper()

	ctx := b.Context()

	db, err := database.Open(ctx, ":memory:")
	if err != nil {
		b.Fatal(err)
	}

	b.Cleanup(func() { _ = db.Close() })

	if _, err := db.ExecContext(ctx, accountSchema); err != nil {
		b.Fatal(err)
	}

	for i := range rows {
		a := Account{Name: fmt.Sprintf("User %d", i), Email: fmt.Sprintf("user%d@example.com", i)}
		if err := orm.Create(ctx, db, &a); err != nil {
			b.Fatal(err)
		}
	}

	return db
}

func scanAccount(row interface{ Scan(dest ...any) error }, a *Account) error {
	return row.Scan(&a.ID, &a.CreatedAt, &a.UpdatedAt, &a.Name, &a.Email)
}

func rawInsert(ctx context.Context, db *sql.DB, a *Account) error {
	now := time.Now()
	a.CreatedAt, a.UpdatedAt = now, now

	return db.QueryRowContext(ctx,
		`INSERT INTO "accounts" ("created_at", "updated_at", "name", "email") VALUES (?, ?, ?, ?) RETURNING "id"`,
		a.CreatedAt, a.UpdatedAt, a.Name, a.Email,
	).Scan(&a.ID)
}

func rawFind(ctx context.Context, db *sql.DB, id int64) (Account, error) {
	var a Account

	err := scanAccount(db.QueryRowContext(ctx, "SELECT "+accountCols+" FROM accounts WHERE id = ?", id), &a)

	return a, err
}

func rawList(ctx context.Context, db *sql.DB, limit int) ([]Account, error) {
	rows, err := db.QueryContext(ctx, "SELECT "+accountCols+" FROM accounts ORDER BY id LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Account, 0, limit)

	for rows.Next() {
		var a Account
		if err := scanAccount(rows, &a); err != nil {
			return nil, err
		}

		out = append(out, a)
	}

	return out, rows.Err()
}

func BenchmarkORMInsert(b *testing.B) {
	b.Run("impl=database_sql", func(b *testing.B) {
		db := openAccounts(b, 0)
		ctx := b.Context()

		b.ReportAllocs()

		for b.Loop() {
			a := Account{Name: "Ada", Email: "ada@example.com"}
			if err := rawInsert(ctx, db.SQL(), &a); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("impl=vukem_orm", func(b *testing.B) {
		db := openAccounts(b, 0)
		ctx := b.Context()

		b.ReportAllocs()

		for b.Loop() {
			a := Account{Name: "Ada", Email: "ada@example.com"}
			if err := orm.Create(ctx, db, &a); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkORMFind(b *testing.B) {
	b.Run("impl=database_sql", func(b *testing.B) {
		db := openAccounts(b, 100)
		ctx := b.Context()

		b.ReportAllocs()

		for b.Loop() {
			if a, err := rawFind(ctx, db.SQL(), 50); err != nil || a.ID != 50 {
				b.Fatal(a.ID, err)
			}
		}
	})

	b.Run("impl=vukem_orm", func(b *testing.B) {
		db := openAccounts(b, 100)
		ctx := b.Context()
		q := orm.QueryOf[Account](db)

		b.ReportAllocs()

		for b.Loop() {
			if a, err := q.Find(ctx, 50); err != nil || a.ID != 50 {
				b.Fatal(a.ID, err)
			}
		}
	})
}

func BenchmarkORMSelect100(b *testing.B) {
	b.Run("impl=database_sql", func(b *testing.B) {
		db := openAccounts(b, 200)
		ctx := b.Context()

		b.ReportAllocs()

		for b.Loop() {
			if got, err := rawList(ctx, db.SQL(), 100); err != nil || len(got) != 100 {
				b.Fatal(len(got), err)
			}
		}
	})

	b.Run("impl=vukem_orm", func(b *testing.B) {
		db := openAccounts(b, 200)
		ctx := b.Context()
		q := orm.QueryOf[Account](db).OrderBy("id", orm.Asc).Limit(100)

		b.ReportAllocs()

		for b.Loop() {
			if got, err := q.Get(ctx); err != nil || len(got) != 100 {
				b.Fatal(len(got), err)
			}
		}
	})
}
