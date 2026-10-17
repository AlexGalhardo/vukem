package schema

import (
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/database"
)

func TestHasColumn(t *testing.T) {
	t.Parallel()

	db := openDB(t)
	s := New(t.Context(), db)

	if err := s.Create("tokens", func(t *Table) {
		t.String("id")
		t.Timestamp("expires_at").Nullable()
	}); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		table, column string
		want          bool
	}{
		{"tokens", "expires_at", true},
		{"tokens", "revoked_at", false},
		{"missing", "id", false},
	}

	for _, tt := range tests {
		if got, err := s.HasColumn(tt.table, tt.column); err != nil || got != tt.want {
			t.Errorf("HasColumn(%q, %q) = %v, %v, want %v, nil", tt.table, tt.column, got, err, tt.want)
		}
	}

	if _, err := s.HasColumn("tokens", "bad name"); err == nil {
		t.Error(`HasColumn("tokens", "bad name") = nil error, want invalid column name`)
	}

	rec, _ := recorder(database.SQLite)
	if ok, err := rec.HasColumn("tokens", "id"); ok || err != nil {
		t.Errorf("HasColumn() while recording = %v, %v, want false, nil", ok, err)
	}
}

func TestHasColumnDialects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		dialect database.Dialect
		want    string
	}{
		{"postgres", database.Postgres, "table_schema = current_schema() AND table_name = $1 AND column_name = $2"},
		{"mysql", database.MySQL, "table_schema = DATABASE() AND table_name = ? AND column_name = ?"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db, log := serverDialectDB(t, tt.dialect)

			ok, err := New(t.Context(), db).HasColumn("users", "email")
			if ok || err == nil || !strings.Contains(err.Error(), "schema: has column") {
				t.Fatalf("HasColumn() on SQLite = %v, %v, want has column error", ok, err)
			}

			if got := log.all(); len(got) != 1 || !strings.Contains(got[0], tt.want) {
				t.Errorf("statements = %q, want %q", got, tt.want)
			}
		})
	}
}
