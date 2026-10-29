package schema

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/database"
)

func TestIndexUsingDDL(t *testing.T) {
	t.Parallel()

	got := slices.DeleteFunc(record(t, database.Postgres, func(s *Schema) error {
		return s.Create("items", func(t *Table) {
			t.ID()
			t.JSON("tags")
			t.Timestamp("released_at")
			t.Index("tags").Using("GIN")
			t.Index("released_at").Using("brin").Where("released_at IS NOT NULL")
		})
	}), func(s string) bool { return strings.HasPrefix(s, "CREATE TABLE") })

	want := []string{
		`CREATE INDEX "items_tags_index" ON "items" USING gin ("tags")`,
		`CREATE INDEX "items_released_at_index" ON "items" USING brin ("released_at") WHERE released_at IS NOT NULL`,
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("DDL mismatch (-want +got):\n%s", diff)
	}
}

func TestIndexUsingErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		d      database.Dialect
		method string
		want   string
	}{
		{"sqlite has no index methods", database.SQLite, "gin", "only postgres supports index methods"},
		{"mysql has no gin", database.MySQL, "gin", "only postgres supports index methods"},
		{"unknown method", database.Postgres, "gin; DROP TABLE users", "unknown index method"},
		{"empty method", database.Postgres, "", "unknown index method"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s, stmts := recorder(tt.d)

			err := s.Create("t", func(t *Table) { t.ID(); t.JSON("doc"); t.Index("doc").Using(tt.method) })
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Create = %v, want error containing %q", err, tt.want)
			}

			if len(*stmts) != 0 {
				t.Fatalf("statements ran despite the error: %q", *stmts)
			}
		})
	}
}
