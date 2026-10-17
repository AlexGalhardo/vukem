package database

import (
	"errors"
	"fmt"
	"strings"

	"github.com/AlexGalhardo/vukem/internal/sqlscan"
)

// ErrUnterminatedSQL is returned by RebindChecked when the query ends
// inside a string literal, a quoted identifier, a comment (a trailing "--"
// comment needs its newline) or a dollar-quoted body.
var ErrUnterminatedSQL = errors.New("database: query ends inside an unterminated literal, identifier or comment")

// ErrMixedPlaceholders is returned by RebindChecked when a PostgreSQL
// query mixes "?" placeholders with explicit "$n" parameters: renumbering
// the "?" would bind the wrong values.
var ErrMixedPlaceholders = errors.New("database: query mixes ? placeholders with $n parameters")

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
//
// Rebind does not report malformed input: an unterminated literal is
// returned as scanned and "$n" mixed with "?" is renumbered anyway. Use
// RebindChecked for queries assembled at runtime or from fragments.
func Rebind(d Dialect, query string) string {
	if d.Placeholder(1) == "?" || !strings.Contains(query, "?") {
		return query
	}

	return sqlscan.Scan(d.Name(), query, d.Placeholder).SQL
}

// RebindChecked is Rebind that fails closed: it returns ErrUnterminatedSQL
// when the query (in any dialect) ends inside a literal, quoted identifier,
// comment or dollar-quoted body, and ErrMixedPlaceholders when a
// PostgreSQL query has both "?" placeholders and "$n" parameters. A query
// with only "$n" parameters is returned unchanged.
//
// Prefer it over Rebind whenever the query is built from pieces (string
// concatenation, optional clauses), where an unbalanced quote would
// swallow the SQL appended after it; Rebind stays for constant queries.
func RebindChecked(d Dialect, query string) (string, error) {
	var ph func(int) string
	if d.Placeholder(1) != "?" {
		ph = d.Placeholder
	}

	r := sqlscan.Scan(d.Name(), query, ph)
	if !r.Terminated {
		return "", fmt.Errorf("%w: %q", ErrUnterminatedSQL, query)
	}

	if r.Placeholders > 0 && r.DollarParams > 0 {
		return "", fmt.Errorf("%w: %q", ErrMixedPlaceholders, query)
	}

	if ph == nil {
		return query, nil
	}

	return r.SQL, nil
}
