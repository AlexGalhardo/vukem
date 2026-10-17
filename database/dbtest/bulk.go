package dbtest

import (
	"context"
	"errors"
	"testing"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/schema"
)

// number is a raffle number: bulk inserted, range-checked and locked.
type number struct {
	orm.Model

	Value  int
	Status string
}

// Table implements orm.Tabler.
func (number) Table() string { return "dbt_numbers" }

// descRow lives in a table whose ids count down (PostgreSQL only).
type descRow struct {
	ID    int64
	Value int
}

// Table implements orm.Tabler.
func (descRow) Table() string { return "dbt_desc" }

// bulkRows needs more than one statement on every dialect (4 parameters
// per row against limits of 32766 and 65535).
const bulkRows = 20000

// mysqlBulkRows is the row count on MySQL, where CreateMany with generated
// ids runs one INSERT per row (no reliable ids for multi-row inserts):
// bulkRows round trips would only test the server's latency (about 45ms
// per parameterized statement against Docker Desktop on Windows, i.e. 15
// minutes per server).
const mysqlBulkRows = 200

func testBulkLocksChecks(ctx context.Context, t *testing.T, db *database.DB) {
	t.Helper()

	s := schema.New(ctx, db)
	if err := s.Create("dbt_numbers", func(t *schema.Table) {
		t.ID()
		t.Integer("value").Unique()
		t.String("status", 20).Default("free")
		t.Timestamps()
		t.Check("dbt_numbers_value_check", "value BETWEEN 1 AND 100000")
	}); err != nil {
		t.Fatal(err)
	}

	rows := bulkRows
	if db.Dialect().Name() == "mysql" {
		rows = mysqlBulkRows
	}

	numbers := make([]number, rows)
	for i := range numbers {
		numbers[i] = number{Value: i + 1, Status: "free"}
	}

	if err := orm.CreateMany(ctx, db, numbers); err != nil {
		t.Fatalf("CreateMany: %v", err)
	}

	if n, err := orm.QueryOf[number](db).Count(ctx); err != nil || n != int64(rows) {
		t.Fatalf("Count after CreateMany = %d, %v, want %d", n, err, rows)
	}

	for _, i := range []int{0, rows / 2, rows - 1} {
		got, err := orm.QueryOf[number](db).Find(ctx, numbers[i].ID)
		if err != nil || got.Value != numbers[i].Value {
			t.Fatalf("Find(numbers[%d].ID = %d) = %+v, %v, want value %d", i, numbers[i].ID, got, err, numbers[i].Value)
		}
	}

	if err := orm.Create(ctx, db, &number{Value: 0, Status: "free"}); err == nil {
		t.Fatal("CHECK dbt_numbers_value_check not enforced")
	}

	t.Run("DescendingIDs", func(t *testing.T) { testDescendingIDs(ctx, t, db, s) })
	t.Run("AlterCheck", func(t *testing.T) { testAlterCheck(ctx, t, db, s) })
	t.Run("Locks", func(t *testing.T) { testLocks(ctx, t, db) })
}

func testAlterCheck(ctx context.Context, t *testing.T, db *database.DB, s *schema.Schema) {
	t.Helper()

	addStatus := func(t *schema.Table) { t.Check("dbt_numbers_status_check", "status IN ('free', 'held')") }

	if db.Dialect().Name() == "sqlite" {
		if err := s.Table("dbt_numbers", addStatus); err == nil {
			t.Fatal("sqlite added a CHECK to an existing table")
		}

		return
	}

	if err := s.Table("dbt_numbers", addStatus); err != nil {
		t.Fatal(err)
	}

	if err := orm.Create(ctx, db, &number{Value: bulkRows + 1, Status: "sold"}); err == nil {
		t.Fatal("added CHECK not enforced")
	}

	if err := s.Table("dbt_numbers", func(t *schema.Table) { t.DropCheck("dbt_numbers_status_check") }); err != nil {
		t.Fatal(err)
	}

	if err := orm.Create(ctx, db, &number{Value: bulkRows + 2, Status: "sold"}); err != nil {
		t.Fatalf("insert after DropCheck: %v", err)
	}
}

func testLocks(ctx context.Context, t *testing.T, db *database.DB) {
	t.Helper()

	if _, err := orm.QueryOf[number](db).LockForUpdate().First(ctx); !errors.Is(err, orm.ErrLockOutsideTx) {
		t.Fatalf("lock on *DB = %v, want ErrLockOutsideTx", err)
	}

	err := db.Tx(ctx, func(tx *database.Tx) error {
		held, err := orm.QueryOf[number](tx).Where("value", 1).LockForUpdate().First(ctx)
		if err != nil {
			return err
		}

		if _, err := orm.QueryOf[number](tx).Where("value", 2).ForShare().Get(ctx); err != nil {
			return err
		}

		held.Status = "held"
		if err := orm.Update(ctx, tx, &held); err != nil {
			return err
		}

		// SQLite has one writer connection: a second transaction would wait
		// for this one, and its locks are no-ops anyway.
		if db.Dialect().Name() == "sqlite" {
			return nil
		}

		// The NoWait error aborts the other transaction on Postgres, so it
		// is returned (and rolls back) instead of committed.
		err = db.Tx(ctx, func(other *database.Tx) error {
			_, err := orm.QueryOf[number](other).Where("value", 1).LockForUpdate().NoWait().First(ctx)
			return err
		})
		if err == nil {
			t.Error("NoWait got a row locked by another transaction")
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if db.Dialect().Name() == "sqlite" {
		return
	}

	err = db.Tx(ctx, func(tx *database.Tx) error {
		if _, err := orm.QueryOf[number](tx).Where("value", 1).LockForUpdate().First(ctx); err != nil {
			return err
		}

		return db.Tx(ctx, func(other *database.Tx) error {
			got, err := orm.QueryOf[number](other).
				WhereIn("value", 1, 2).
				OrderBy("value", orm.Asc).
				LockForUpdate().
				SkipLocked().
				Pluck[int](ctx, "value")
			if err != nil {
				return err
			}

			if len(got) != 1 || got[0] != 2 {
				t.Errorf("SkipLocked = %v, want [2]", got)
			}

			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
}

// testDescendingIDs checks CreateMany fails closed when generated ids do
// not increase in VALUES order, instead of pairing them by guess.
func testDescendingIDs(ctx context.Context, t *testing.T, db *database.DB, s *schema.Schema) {
	t.Helper()

	if db.Dialect().Name() != "postgres" {
		t.Skip("descending identity sequences are PostgreSQL only")
	}

	if err := s.Raw(`CREATE TABLE dbt_desc (
  id BIGINT GENERATED BY DEFAULT AS IDENTITY (START WITH 1000 INCREMENT BY -1 MINVALUE 1 MAXVALUE 1000) PRIMARY KEY,
  value INTEGER NOT NULL
)`); err != nil {
		t.Fatal(err)
	}

	rows := []descRow{{Value: 1}, {Value: 2}, {Value: 3}}
	if err := orm.CreateMany(ctx, db, rows); err == nil {
		t.Fatal("CreateMany with a descending sequence succeeded, want an error")
	}

	if n, err := orm.QueryOf[descRow](db).Count(ctx); err != nil || n != 0 {
		t.Fatalf("rows after failed CreateMany = %d, %v, want 0 (rolled back)", n, err)
	}

	for i, r := range rows {
		if r.ID != 0 {
			t.Fatalf("rows[%d].ID = %d after rollback, want 0", i, r.ID)
		}
	}
}
