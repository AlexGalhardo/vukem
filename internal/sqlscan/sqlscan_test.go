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
		want    Result
	}{
		{"postgres renumbers", "postgres", "a = ? AND b IN (?, ?)", Result{"a = $1 AND b IN ($2, $3)", 3, true}},
		{"postgres line comment", "postgres", "a = ? -- b?\n", Result{"a = $1 -- b?\n", 1, true}},
		{"postgres quoted identifier", "postgres", `"why?" = ?`, Result{`"why?" = $1`, 1, true}},
		{"postgres dollar body", "postgres", "$f$ ? $f$ = ?", Result{"$f$ ? $f$ = $1", 1, true}},
		{"postgres E string", "postgres", `E'\'?' = ?`, Result{`E'\'?' = $1`, 1, true}},
		{"postgres plain string keeps backslash", "postgres", `'C:\' = ?`, Result{`'C:\' = $1`, 1, true}},
		{"postgres nested block comment", "postgres", "/* /* ? */ ? */ ?", Result{"/* /* ? */ ? */ $1", 1, true}},
		{"postgres jsonb escape", "postgres", "data ?? 'k' AND id = ?", Result{"data ? 'k' AND id = $1", 1, true}},
		{"postgres unterminated literal", "postgres", "a = 'x ?", Result{"a = 'x ?", 0, false}},
		{"postgres unterminated line comment", "postgres", "a = ? -- note", Result{"a = $1 -- note", 1, false}},
		{"postgres unterminated dollar body", "postgres", "$$ ? ", Result{"$$ ? ", 0, false}},
		{"postgres unterminated block comment", "postgres", "/* ?", Result{"/* ?", 0, false}},
		{"sqlite double question mark is two", "sqlite", "a = ?? ", Result{"a = ?? ", 2, true}},
		{"sqlite no dollar quoting", "sqlite", "$$ ? $$", Result{"$$ ? $$", 1, true}},
		{"sqlite backtick identifier", "sqlite", "`a?` = ?", Result{"`a?` = ?", 1, true}},
		{"sqlite block comment not nested", "sqlite", "/* /* */ ?", Result{"/* /* */ ?", 1, true}},
		{"mysql backslash in string", "mysql", `'it\'s ?' = ?`, Result{`'it\'s ?' = ?`, 1, true}},
		{"mysql double-quoted string", "mysql", `"a\"?" = ?`, Result{`"a\"?" = ?`, 1, true}},
		{"mysql hash comment", "mysql", "a = ? # b?\n", Result{"a = ? # b?\n", 1, true}},
		{"utf-8", "postgres", "'ação?' = ?", Result{"'ação?' = $1", 1, true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var ph func(int) string
			if tt.dialect == "postgres" {
				ph = dollar
			}

			if diff := cmp.Diff(tt.want, Scan(tt.dialect, tt.query, ph)); diff != "" {
				t.Fatalf("Scan(%s, %q) (-want +got):\n%s", tt.dialect, tt.query, diff)
			}
		})
	}
}
