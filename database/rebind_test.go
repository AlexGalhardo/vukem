package database

import "testing"

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
