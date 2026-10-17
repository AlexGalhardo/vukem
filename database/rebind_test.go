package database

import (
	"errors"
	"strings"
	"testing"
)

func TestRebind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		d     Dialect
		query string
		want  string
	}{
		{"sqlite unchanged", SQLite, "SELECT * FROM t WHERE a = ? AND b = ?", "SELECT * FROM t WHERE a = ? AND b = ?"},
		{"mysql unchanged", MySQL, "UPDATE t SET a = ? WHERE id = ?", "UPDATE t SET a = ? WHERE id = ?"},
		{
			"postgres numbers",
			Postgres,
			"SELECT * FROM t WHERE a = ? AND b IN (?, ?)",
			"SELECT * FROM t WHERE a = $1 AND b IN ($2, $3)",
		},
		{"no placeholders", Postgres, "SELECT 1", "SELECT 1"},
		{"string literal", Postgres, "SELECT '?', a FROM t WHERE b = ?", "SELECT '?', a FROM t WHERE b = $1"},
		{"doubled quote in literal", Postgres, "SELECT 'it''s ?' WHERE a = ?", "SELECT 'it''s ?' WHERE a = $1"},
		{"escape string", Postgres, `SELECT E'\'?' WHERE a = ?`, `SELECT E'\'?' WHERE a = $1`},
		{"backslash in plain string", Postgres, `SELECT 'C:\' WHERE a = ?`, `SELECT 'C:\' WHERE a = $1`},
		{"quoted identifier", Postgres, `SELECT "what?" FROM t WHERE a = ?`, `SELECT "what?" FROM t WHERE a = $1`},
		{"line comment", Postgres, "SELECT a -- why?\nFROM t WHERE b = ?", "SELECT a -- why?\nFROM t WHERE b = $1"},
		{
			"block comment",
			Postgres,
			"SELECT /* a? /* nested? */ b? */ a WHERE b = ?",
			"SELECT /* a? /* nested? */ b? */ a WHERE b = $1",
		},
		{"dollar quoted", Postgres, "DO $$ SELECT '?' $$; SELECT ?", "DO $$ SELECT '?' $$; SELECT $1"},
		{"tagged dollar quoted", Postgres, "SELECT $fn$ ? $$ ? $fn$, ?", "SELECT $fn$ ? $$ ? $fn$, $1"},
		{
			"jsonb operator escape",
			Postgres,
			"SELECT * FROM t WHERE data ?? 'k' AND id = ?",
			"SELECT * FROM t WHERE data ? 'k' AND id = $1",
		},
		{"unterminated literal", Postgres, "SELECT 'oops ? WHERE a = ?", "SELECT 'oops ? WHERE a = ?"},
		{"utf-8 text", Postgres, "SELECT 'ação?' WHERE nome = ?", "SELECT 'ação?' WHERE nome = $1"},
		{"crlf comment", Postgres, "SELECT a -- c?\r\nWHERE b = ?", "SELECT a -- c?\r\nWHERE b = $1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := Rebind(tt.d, tt.query); got != tt.want {
				t.Fatalf("Rebind(%s, %q) = %q, want %q", tt.d.Name(), tt.query, got, tt.want)
			}
		})
	}
}

func TestRebindRunsOnSQLite(t *testing.T) {
	t.Parallel()

	db := openTemp(t)

	var got string
	if err := db.QueryRowContext(t.Context(), Rebind(db.Dialect(), "SELECT ? || '?'"), "a").Scan(&got); err != nil {
		t.Fatal(err)
	}

	if got != "a?" {
		t.Fatalf("SELECT = %q, want %q", got, "a?")
	}
}

func TestRebindChecked(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		d       Dialect
		query   string
		want    string
		wantErr error
	}{
		{
			"postgres numbers",
			Postgres,
			"SELECT * FROM t WHERE a = ? AND b = ?",
			"SELECT * FROM t WHERE a = $1 AND b = $2",
			nil,
		},
		{"sqlite unchanged", SQLite, "SELECT * FROM t WHERE a = ?", "SELECT * FROM t WHERE a = ?", nil},
		{"mysql unchanged", MySQL, "SELECT * FROM t WHERE a = ?", "SELECT * FROM t WHERE a = ?", nil},
		{"dollar params alone", Postgres, "SELECT * FROM t WHERE a = $1", "SELECT * FROM t WHERE a = $1", nil},
		{"dollar inside literal", Postgres, "SELECT '$1' WHERE a = ?", "SELECT '$1' WHERE a = $1", nil},
		{
			"jsonb operator",
			Postgres,
			"SELECT 1 WHERE data ?? 'k' AND id = ?",
			"SELECT 1 WHERE data ? 'k' AND id = $1",
			nil,
		},
		{"mixed placeholders", Postgres, "SELECT * FROM t WHERE a = $1 AND b = ?", "", ErrMixedPlaceholders},
		{"unterminated literal postgres", Postgres, "SELECT 'oops WHERE a = ?", "", ErrUnterminatedSQL},
		{"unterminated literal sqlite", SQLite, "SELECT 'oops WHERE a = ?", "", ErrUnterminatedSQL},
		{"unterminated without placeholders", MySQL, "SELECT `a FROM t", "", ErrUnterminatedSQL},
		{"trailing line comment", Postgres, "SELECT ? -- note", "", ErrUnterminatedSQL},
		{"unterminated block comment", SQLite, "SELECT ? /* note", "", ErrUnterminatedSQL},
		{"unterminated dollar body", Postgres, "SELECT $fn$ body WHERE a = ?", "", ErrUnterminatedSQL},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := RebindChecked(tt.d, tt.query)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("RebindChecked(%s, %q) error = %v, want %v", tt.d.Name(), tt.query, err, tt.wantErr)
			}

			if got != tt.want {
				t.Fatalf("RebindChecked(%s, %q) = %q, want %q", tt.d.Name(), tt.query, got, tt.want)
			}
		})
	}
}

func TestRebindCheckedErrorQuotesAShortPrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, query, want string
	}{
		{"short query whole", "SELECT 'oops", `"SELECT 'oops"`},
		{"long query cut", "SELECT '" + strings.Repeat("a", 100), `"SELECT '` + strings.Repeat("a", 72) + `…"`},
		{
			"cut on a rune boundary",
			"SELECT '" + strings.Repeat("a", 71) + "çç" + strings.Repeat("b", 20),
			`"SELECT '` + strings.Repeat("a", 71) + `…"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := RebindChecked(Postgres, tt.query)
			if err == nil || !strings.HasSuffix(err.Error(), ": "+tt.want) {
				t.Fatalf("RebindChecked(%q) error = %v, want it to end with %s", tt.query, err, tt.want)
			}
		})
	}
}
