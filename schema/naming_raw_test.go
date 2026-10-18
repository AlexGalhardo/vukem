package schema

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/database"
)

func TestConstrainedPluralisesLikeTheORM(t *testing.T) {
	t.Parallel()

	tests := []struct {
		column, want string
	}{
		{"user_id", `REFERENCES "users"`},
		{"category_id", `REFERENCES "categories"`},
		{"address_id", `REFERENCES "addresses"`},
		{"day_id", `REFERENCES "days"`},
	}
	for _, tt := range tests {
		t.Run(tt.column, func(t *testing.T) {
			t.Parallel()

			ddl := strings.Join(record(t, database.Postgres, func(s *Schema) error {
				return s.Create("items", func(t *Table) { t.ForeignID(tt.column).Constrained() })
			}), "\n")
			if !strings.Contains(ddl, tt.want) {
				t.Fatalf("Constrained() on %s = %s, want %s", tt.column, ddl, tt.want)
			}
		})
	}
}

// execRecorder is a database.Executor that records ExecContext calls.
type execRecorder struct {
	d     database.Dialect
	query string
	args  []any
}

func (e *execRecorder) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	e.query, e.args = query, args
	return nil, nil //nolint:nilnil // the result is unused by schema
}

func (e *execRecorder) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	panic("unexpected QueryContext")
}

func (e *execRecorder) QueryRowContext(context.Context, string, ...any) *sql.Row {
	panic("unexpected QueryRowContext")
}

func (e *execRecorder) Dialect() database.Dialect { return e.d }

func TestRawRebindsPlaceholders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		d     database.Dialect
		query string
		args  []any
		want  string
	}{
		{
			"postgres with args", database.Postgres,
			"UPDATE users SET role = ? WHERE note <> '?' AND id = ?",
			[]any{"admin", 1},
			"UPDATE users SET role = $1 WHERE note <> '?' AND id = $2",
		},
		{
			// Without args a "?" cannot be a placeholder: jsonb operators stay.
			"postgres without args", database.Postgres,
			"CREATE INDEX x ON t ((data ? 'k'))", nil,
			"CREATE INDEX x ON t ((data ? 'k'))",
		},
		{
			"postgres numbered", database.Postgres,
			"UPDATE users SET role = $1",
			[]any{"admin"},
			"UPDATE users SET role = $1",
		},
		{
			"mysql", database.MySQL,
			"UPDATE users SET role = ?",
			[]any{"admin"},
			"UPDATE users SET role = ?",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ex := &execRecorder{d: tt.d}
			if err := New(t.Context(), ex).Raw(tt.query, tt.args...); err != nil {
				t.Fatal(err)
			}

			if diff := cmp.Diff(tt.want, ex.query); diff != "" {
				t.Fatalf("Raw(%q) mismatch (-want +got):\n%s", tt.query, diff)
			}
		})
	}
}

func TestRawRejectsMalformedQueryWithArgs(t *testing.T) {
	t.Parallel()

	ex := &execRecorder{d: database.Postgres}
	if err := New(t.Context(), ex).Raw("UPDATE t SET a = ? WHERE b = $1", 1); err == nil {
		t.Fatal("Raw with mixed ? and $n = nil, want error")
	}

	if ex.query != "" {
		t.Fatalf("Raw executed %q after a rebind error", ex.query)
	}
}

func TestRawRecordsTheQueryAsWritten(t *testing.T) {
	t.Parallel()

	got := record(t, database.Postgres, func(s *Schema) error {
		return s.Raw("UPDATE users SET role = ?", "admin")
	})
	if diff := cmp.Diff([]string{"UPDATE users SET role = ?"}, got); diff != "" {
		t.Fatalf("recorded mismatch (-want +got):\n%s", diff)
	}
}
