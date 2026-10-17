package sqlscan

import (
	"strconv"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func dollar(n int) string { return "$" + strconv.Itoa(n) }

func TestScan(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		dialect string
		query   string
		sql     string
		n       int
		dollars int
		open    bool // ends inside a literal, identifier or comment
	}{
		{"postgres renumbers", "postgres", "a = ? AND b IN (?, ?)", "a = $1 AND b IN ($2, $3)", 3, 0, false},
		{"postgres line comment", "postgres", "a = ? -- b?\n", "a = $1 -- b?\n", 1, 0, false},
		{"postgres quoted identifier", "postgres", `"why?" = ?`, `"why?" = $1`, 1, 0, false},
		{"postgres dollar body", "postgres", "$f$ ? $f$ = ?", "$f$ ? $f$ = $1", 1, 0, false},
		{"postgres E string", "postgres", `E'\'?' = ?`, `E'\'?' = $1`, 1, 0, false},
		{"postgres plain string keeps backslash", "postgres", `'C:\' = ?`, `'C:\' = $1`, 1, 0, false},
		{"postgres nested block comment", "postgres", "/* /* ? */ ? */ ?", "/* /* ? */ ? */ $1", 1, 0, false},
		{"postgres jsonb escape", "postgres", "data ?? 'k' AND id = ?", "data ? 'k' AND id = $1", 1, 0, false},
		{"postgres unterminated literal", "postgres", "a = 'x ?", "a = 'x ?", 0, 0, true},
		{"postgres unterminated line comment", "postgres", "a = ? -- note", "a = $1 -- note", 1, 0, true},
		{"postgres unterminated dollar body", "postgres", "$$ ? ", "$$ ? ", 0, 0, true},
		{"postgres unterminated block comment", "postgres", "/* ?", "/* ?", 0, 0, true},
		{"postgres backtick is an operator", "postgres", "a ` ? ` b", "a ` $1 ` b", 1, 0, false},
		{"postgres bracket is a subscript", "postgres", "a[?] = ?", "a[$1] = $2", 2, 0, false},
		{
			"postgres explicit $n", "postgres", "a = $1 AND b = ? AND c = '$2' AND d = $$ $3 $$",
			"a = $1 AND b = $1 AND c = '$2' AND d = $$ $3 $$", 1, 1, false,
		},
		{"postgres $n inside identifier", "postgres", "a$1 = ?", "a$1 = $1", 1, 0, false},
		{"sqlite double question mark is two", "sqlite", "a = ?? ", "a = ?? ", 2, 0, false},
		{"sqlite no dollar quoting", "sqlite", "$$ ? $$", "$$ ? $$", 1, 0, false},
		{"sqlite backtick identifier", "sqlite", "`a?` = ?", "`a?` = ?", 1, 0, false},
		{"sqlite bracket identifier", "sqlite", "[a?] = ?", "[a?] = ?", 1, 0, false},
		{"sqlite unterminated bracket", "sqlite", "[a = ?", "[a = ?", 0, 0, true},
		{"sqlite block comment not nested", "sqlite", "/* /* */ ?", "/* /* */ ?", 1, 0, false},
		{"sqlite -- is always a comment", "sqlite", "a--?\n?", "a--?\n?", 1, 0, false},
		{"mysql backslash in string", "mysql", `'it\'s ?' = ?`, `'it\'s ?' = ?`, 1, 0, false},
		{"mysql double-quoted string", "mysql", `"a\"?" = ?`, `"a\"?" = ?`, 1, 0, false},
		{"mysql backtick identifier", "mysql", "`a?` = ?", "`a?` = ?", 1, 0, false},
		{"mysql bracket is not a quote", "mysql", "[a?]", "[a?]", 1, 0, false},
		{"mysql hash comment", "mysql", "a = ? # b?\n", "a = ? # b?\n", 1, 0, false},
		{"mysql -- without space is minus minus", "mysql", "a--?", "a--?", 1, 0, false},
		{"mysql -- with tab is a comment", "mysql", "a --\t?\n?", "a --\t?\n?", 1, 0, false},
		{"mysql -- with CR is a comment", "mysql", "a --\r\n?", "a --\r\n?", 1, 0, false},
		{"mysql -- at the end is a comment", "mysql", "a = ? --", "a = ? --", 1, 0, true},
		{"empty dialect lexes like sqlite", "", "[a?] = `b?` --x\n?", "[a?] = `b?` --x\n?", 1, 0, false},
		{"utf-8", "postgres", "'ação?' = ?", "'ação?' = $1", 1, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var ph func(int) string
			if tt.dialect == "postgres" {
				ph = dollar
			}

			want := Result{SQL: tt.sql, Placeholders: tt.n, DollarParams: tt.dollars, Terminated: !tt.open}
			if diff := cmp.Diff(want, Scan(tt.dialect, tt.query, ph)); diff != "" {
				t.Fatalf("Scan(%q, %q) (-want +got):\n%s", tt.dialect, tt.query, diff)
			}
		})
	}
}
