package schema

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/database"
)

func TestPartialIndexDDL(t *testing.T) {
	t.Parallel()

	create := func(s *Schema) error {
		return s.Create("reservations", func(t *Table) {
			t.ID()
			t.BigInteger("number_id")
			t.String("status")
			t.UniqueIndex("number_id").Where("status = 'live'")
			t.Index("status").Where("status <> 'expired'")
		})
	}
	alter := func(s *Schema) error {
		return s.Table("reservations", func(t *Table) {
			t.UniqueIndex("number_id", "status").Where("status = 'live'")
		})
	}

	tests := []struct {
		name string
		d    database.Dialect
		fn   func(*Schema) error
		want []string
	}{
		{"sqlite create", database.SQLite, create, []string{
			`CREATE UNIQUE INDEX "reservations_number_id_unique" ON "reservations" ("number_id") WHERE status = 'live'`,
			`CREATE INDEX "reservations_status_index" ON "reservations" ("status") WHERE status <> 'expired'`,
		}},
		{"postgres alter", database.Postgres, alter, []string{
			`CREATE UNIQUE INDEX "reservations_number_id_status_unique" ON "reservations" ("number_id", "status") ` +
				`WHERE status = 'live'`,
		}},
		{"sqlite alter", database.SQLite, alter, []string{
			`CREATE UNIQUE INDEX "reservations_number_id_status_unique" ON "reservations" ("number_id", "status") ` +
				`WHERE status = 'live'`,
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// CREATE TABLE output is covered by the per-dialect DDL tests.
			got := slices.DeleteFunc(record(t, tt.d, tt.fn), func(s string) bool {
				return strings.HasPrefix(s, "CREATE TABLE")
			})

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Fatalf("DDL mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestPartialIndexErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		d    database.Dialect
		fn   func(*Schema) error
		want string
	}{
		{"mysql has no partial indexes", database.MySQL, func(s *Schema) error {
			return s.Create("t", func(t *Table) { t.ID(); t.UniqueIndex("id").Where("id > 0") })
		}, "mysql does not support partial indexes"},
		{"empty expression", database.Postgres, func(s *Schema) error {
			return s.Create("t", func(t *Table) { t.ID(); t.UniqueIndex("id").Where(" ") })
		}, "needs an expression"},
		{"invalid column keeps its error", database.Postgres, func(s *Schema) error {
			return s.Create("t", func(t *Table) { t.ID(); t.UniqueIndex("bad col").Where("id > 0") })
		}, "invalid column name"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s, stmts := recorder(tt.d)

			err := tt.fn(s)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want containing %q", err, tt.want)
			}

			if len(*stmts) != 0 {
				t.Fatalf("statements ran despite the error: %q", *stmts)
			}
		})
	}
}

func TestPartialUniqueIndexEnforcedOnSQLite(t *testing.T) {
	t.Parallel()

	db := openDB(t)
	s := New(t.Context(), db)

	if err := s.Create("reservations", func(t *Table) {
		t.ID()
		t.Integer("number_id")
		t.String("status")
		t.UniqueIndex("number_id").Where("status = 'live'")
	}); err != nil {
		t.Fatal(err)
	}

	insert := func(status string) error {
		_, err := db.ExecContext(t.Context(),
			"INSERT INTO reservations (number_id, status) VALUES (?, ?)", 7, status)

		return err
	}

	for _, status := range []string{"expired", "expired", "live"} {
		if err := insert(status); err != nil {
			t.Fatalf("insert %s: %v", status, err)
		}
	}

	if err := insert("live"); err == nil || !strings.Contains(err.Error(), "UNIQUE") {
		t.Fatalf("second live reservation err = %v, want a UNIQUE violation", err)
	}
}
