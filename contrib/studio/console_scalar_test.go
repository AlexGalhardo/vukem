package studio

import "testing"

func TestScalarFunctionsNamedLikeWriteVerbsOnlyRead(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		text, family, kind string
		read               bool
	}{
		{"WITH x AS (SELECT 'a' v) SELECT replace(v, 'a', 'b') FROM x", "sqlite", "WITH", true},
		{"WITH x AS (SELECT 1.5 v) SELECT truncate (v, 0) FROM x", "mysql", "WITH", true},
		{"WITH x AS (SELECT 'a' v) SELECT insert(v, 1, 1, 'b') FROM x", "mysql", "WITH", true},
		{"EXPLAIN ANALYZE SELECT replace(a, 'x', 'y') FROM t", "postgres", "EXPLAIN", true},
		{"WITH d AS (DELETE FROM t RETURNING id) SELECT replace(id, '1', '2') FROM d", "postgres", "DELETE", false},
		{"WITH x AS (SELECT 1) INSERT INTO t SELECT * FROM x", "postgres", "INSERT", false},
		{"EXPLAIN ANALYZE DELETE FROM t", "postgres", "EXPLAIN", false},
	} {
		if got := readOnlyStatement(tc.text, tc.family); got != tc.read {
			t.Errorf("readOnlyStatement(%q) = %v, want %v", tc.text, got, tc.read)
		}

		if got := statementKind(tc.text, tc.family); got != tc.kind {
			t.Errorf("statementKind(%q) = %q, want %q", tc.text, got, tc.kind)
		}
	}
}
