package database

import (
	"strings"

	"github.com/AlexGalhardo/vukem/internal/sqlscan"
)

// Rebind rewrites the "?" placeholders of a hand-written query to d's
// style, so raw SQL shared between SQLite, MySQL and PostgreSQL is written
// once with "?": on PostgreSQL the n-th "?" becomes "$n", on the other
// dialects the query is returned unchanged.
//
// "?" inside string literals ('…', E'…'), quoted identifiers ("…"),
// dollar-quoted bodies ($$…$$, $tag$…$tag$) and comments (-- and nested
// /* */) is left alone. On PostgreSQL "??" is a literal "?", for the jsonb
// operators (data ?? 'key' runs as data ? 'key'). Rebind only renumbers
// placeholders: values still go through args, never into the query text.
func Rebind(d Dialect, query string) string {
	if d.Placeholder(1) == "?" || !strings.Contains(query, "?") {
		return query
	}

	return sqlscan.Scan(d.Name(), query, d.Placeholder).SQL
}
