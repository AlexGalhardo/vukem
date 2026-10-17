package database

import "strings"

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

	var b strings.Builder
	b.Grow(len(query) + 8)

	n := 0

	for i := 0; i < len(query); {
		c := query[i]

		switch {
		case c == '\'':
			escapes := i > 0 && (query[i-1] == 'E' || query[i-1] == 'e') && (i < 2 || !isIdentByte(query[i-2]))
			i = copyUntil(&b, query, i, skipString(query, i, escapes))
		case c == '"':
			i = copyUntil(&b, query, i, skipString(query, i, false))
		case c == '-' && strings.HasPrefix(query[i:], "--"):
			i = copyUntil(&b, query, i, skipLineComment(query, i))
		case c == '/' && strings.HasPrefix(query[i:], "/*"):
			i = copyUntil(&b, query, i, skipBlockComment(query, i))
		case c == '$' && (i == 0 || !isIdentByte(query[i-1])):
			i = copyUntil(&b, query, i, skipDollarQuote(query, i))
		case c == '?' && strings.HasPrefix(query[i:], "??"):
			b.WriteByte('?')
			i += 2
		case c == '?':
			n++
			b.WriteString(d.Placeholder(n))
			i++
		default:
			b.WriteByte(c)
			i++
		}
	}

	return b.String()
}

// copyUntil writes query[i:end] (at least one byte) and returns the new
// position.
func copyUntil(b *strings.Builder, query string, i, end int) int {
	end = max(end, i+1)
	b.WriteString(query[i:end])

	return end
}

// skipString returns the index after the literal or identifier opened at
// query[i]; a doubled quote stays inside it, and so does a backslash-escaped
// one in E'…' strings. An unterminated literal runs to the end.
func skipString(query string, i int, backslash bool) int {
	q := query[i]

	for j := i + 1; j < len(query); j++ {
		switch query[j] {
		case '\\':
			if backslash {
				j++
			}
		case q:
			if j+1 < len(query) && query[j+1] == q {
				j++
				continue
			}

			return j + 1
		}
	}

	return len(query)
}

func skipLineComment(query string, i int) int {
	if j := strings.IndexByte(query[i:], '\n'); j >= 0 {
		return i + j + 1
	}

	return len(query)
}

// skipBlockComment handles PostgreSQL's nested /* */ comments.
func skipBlockComment(query string, i int) int {
	depth := 0

	for j := i; j+1 < len(query); j++ {
		switch query[j : j+2] {
		case "/*":
			depth++
			j++
		case "*/":
			depth--
			j++

			if depth == 0 {
				return j + 1
			}
		}
	}

	return len(query)
}

// skipDollarQuote returns the index after a $tag$…$tag$ body opened at
// query[i], or i+1 when query[i] does not open one ($1, a lone $).
func skipDollarQuote(query string, i int) int {
	j := i + 1
	for j < len(query) && isIdentByte(query[j]) {
		j++
	}

	if j >= len(query) || query[j] != '$' || (j > i+1 && isDigit(query[i+1])) {
		return i + 1
	}

	tag := query[i : j+1]
	if k := strings.Index(query[j+1:], tag); k >= 0 {
		return j + 1 + k + len(tag)
	}

	return len(query)
}

func isIdentByte(c byte) bool {
	return c == '_' || isDigit(c) || (c|0x20 >= 'a' && c|0x20 <= 'z') || c >= 0x80
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
