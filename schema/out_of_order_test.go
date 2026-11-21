package schema

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/database"
)

func createTable(name string) func(s *Schema) error {
	return func(s *Schema) error { return s.Create(name, func(t *Table) { t.ID() }) }
}

// migratedDB runs a and c, leaving c as the latest applied migration.
func migratedDB(t *testing.T) *database.DB {
	t.Helper()

	db := rebuildDB(t)

	m, err := NewMigrator(db, []Migration{mig("20260101_a", createTable("a")), mig("20260301_c", createTable("c"))})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := m.Up(t.Context()); err != nil {
		t.Fatal(err)
	}

	return db
}

func TestUpRefusesAMigrationOlderThanAnAppliedOne(t *testing.T) {
	t.Parallel()

	db := migratedDB(t)
	migs := []Migration{
		mig("20260101_a", createTable("a")),
		mig("20260201_b", createTable("b")),
		mig("20260301_c", createTable("c")),
		mig("20260401_d", createTable("d")),
	}

	m, err := NewMigrator(db, migs)
	if err != nil {
		t.Fatal(err)
	}

	ran, err := m.Up(t.Context())
	if !errors.Is(err, ErrOutOfOrder) || !strings.Contains(err.Error(), "20260201_b") {
		t.Fatalf("Up = %v, %v; want ErrOutOfOrder naming 20260201_b", ran, err)
	}

	if len(ran) != 0 {
		t.Errorf("ran = %v, want nothing run, the in-order 20260401_d included", ran)
	}

	if got := tableNames(t, db); !cmp.Equal(got, []string{"a", "c", "migrations"}) {
		t.Errorf("tables = %v, want a, c and migrations only", got)
	}

	ran, err = m.AllowOutOfOrder().Up(t.Context())
	if err != nil || !cmp.Equal(ran, []string{"20260201_b", "20260401_d"}) {
		t.Errorf("Up with AllowOutOfOrder = %v, %v; want b and d", ran, err)
	}
}

func TestUpAllowsFrameworkMigrationsAddedLate(t *testing.T) {
	t.Parallel()

	db := migratedDB(t)

	module := mig("20260201000000_vukem_create_queue_tables", createTable("jobs_queue"))
	module.AnyOrder = true

	m, err := NewMigrator(db, []Migration{
		mig("20260101_a", createTable("a")),
		mig("20260301_c", createTable("c")),
		module,
	})
	if err != nil {
		t.Fatal(err)
	}

	ran, err := m.Up(t.Context())
	if err != nil || !cmp.Equal(ran, []string{module.ID}) {
		t.Fatalf("Up = %v, %v; want the module migration to run", ran, err)
	}

	st, err := m.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	for _, s := range st {
		if s.OutOfOrder {
			t.Errorf("Status marks %s out of order, want none", s.ID)
		}
	}
}

func TestUpRefusesAnAppMigrationNamedLikeAModule(t *testing.T) {
	t.Parallel()

	m, err := NewMigrator(migratedDB(t), []Migration{
		mig("20260101_a", createTable("a")),
		mig("20260201_vukem_import", createTable("b")),
		mig("20260301_c", createTable("c")),
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := m.Up(t.Context()); !errors.Is(err, ErrOutOfOrder) {
		t.Fatalf("Up = %v, want ErrOutOfOrder", err)
	}
}

func TestUpOnAFreshDatabaseRunsEverything(t *testing.T) {
	t.Parallel()

	m, err := NewMigrator(rebuildDB(t), []Migration{
		mig("2_b", createTable("b")),
		mig("1_a", createTable("a")),
	})
	if err != nil {
		t.Fatal(err)
	}

	if ran, err := m.Up(t.Context()); err != nil || len(ran) != 2 {
		t.Errorf("Up = %v, %v; want two migrations", ran, err)
	}
}
