// Package sqlscan finds the "?" bind placeholders of a SQL statement,
// skipping string literals, quoted identifiers and comments the way the
// target dialect lexes them. database.Rebind and the orm's WhereRaw check
// share it, so counting placeholders and renumbering them cannot disagree.
package sqlscan

import "strings"

// Result is the outcome of Scan.
type Result struct {
	// SQL is the statement with every placeholder replaced by the
	// placeholder function's output (and, on PostgreSQL, "??" by "?").
	SQL string
	// Placeholders is the number of "?" bind placeholders found.
	Placeholders int
	// DollarParams counts explicit PostgreSQL "$n" parameters outside
	// literals, comments and dollar-quoted bodies. Mixed with "?" they
	// would bind the wrong values, so callers that renumber reject them.
	DollarParams int
	// Terminated is false when the statement ends inside a literal, a quoted
	// identifier, a comment (a "--" comment needs its newline) or a
	// dollar-quoted body: a fragment like that would swallow whatever SQL
	// is appended after it.
	Terminated bool
}

// Scan walks query as dialect ("postgres", "mysql", "sqlite") lexes it;
// any other name, including "", lexes like SQLite. ph renders the n-th
// (1-based) placeholder; nil keeps "?".
//
// Every dialect skips '…' literals, "…" quoted text, "--" line comments and
// /* */ block comments. PostgreSQL adds E'…' backslash escapes, nested
// block comments, $tag$…$tag$ bodies and "??" as a literal "?" (jsonb
// operators). MySQL adds `…` identifiers, backslash escapes in quoted
// strings, "#" comments, and needs a space, tab or newline after "--" for
// a comment ("a--1" is a minus a minus). SQLite adds `…` and […]
// identifiers. Outside PostgreSQL "??" is two placeholders.
//
// It assumes the servers' default lexing: standard_conforming_strings on
// (PostgreSQL), no ANSI_QUOTES or NO_BACKSLASH_ESCAPES (MySQL). MySQL runs
// the contents of /*! */ and /*+ */ comments, but Scan skips them like
// any comment, so placeholders there are not supported.
func Scan(dialect, query string, ph func(n int) string) Result {
	s := scanner{query: query, ph: ph, pg: dialect == "postgres", my: dialect == "mysql", ok: true}
	s.lite = !s.pg && !s.my
	s.b.Grow(len(query) + 8)
	s.run()

	return Result{SQL: s.b.String(), Placeholders: s.n, DollarParams: s.dollars, Terminated: s.ok}
}

type scanner struct {
	query        string
	ph           func(int) string
	pg, my, lite bool
	b            strings.Builder
	n, dollars   int
	ok           bool
}

func (s *scanner) run() {
	q := s.query

	for i := 0; i < len(q); {
		c := q[i]

		switch {
		case c == '\'':
			escapes := s.my || (s.pg && i > 0 && (q[i-1] == 'E' || q[i-1] == 'e') && (i < 2 || !isIdentByte(q[i-2])))
			i = s.copy(i, s.skipQuoted(i, escapes))
		case c == '"', c == '`' && !s.pg:
			i = s.copy(i, s.skipQuoted(i, s.my && c == '"'))
		case c == '[' && s.lite:
			i = s.copy(i, s.skipBracket(i))
		case c == '-' && s.lineComment(i), c == '#' && s.my:
			i = s.copy(i, s.skipLineComment(i))
		case c == '/' && strings.HasPrefix(q[i:], "/*"):
			i = s.copy(i, s.skipBlockComment(i))
		case c == '$' && s.pg && (i == 0 || !isIdentByte(q[i-1])):
			i = s.copy(i, s.skipDollarQuote(i))
		case c == '?' && s.pg && strings.HasPrefix(q[i:], "??"):
			s.b.WriteByte('?')
			i += 2
		case c == '?':
			s.n++

			if s.ph != nil {
				s.b.WriteString(s.ph(s.n))
			} else {
				s.b.WriteByte('?')
			}

			i++
		default:
			s.b.WriteByte(c)
			i++
		}
	}
}

// copy writes query[i:end] (at least one byte) and returns the new
// position.
func (s *scanner) copy(i, end int) int {
	end = max(end, i+1)
	s.b.WriteString(s.query[i:end])

	return end
}

// lineComment reports whether "--" starts a comment at query[i]: always on
// PostgreSQL and SQLite, only before whitespace or the end on MySQL.
func (s *scanner) lineComment(i int) bool {
	q := s.query
	if !strings.HasPrefix(q[i:], "--") {
		return false
	}

	if !s.my || i+2 == len(q) {
		return true
	}

	switch q[i+2] {
	case ' ', '\t', '\n', '\r':
		return true
	}

	return false
}

// skipBracket returns the index after a SQLite [identifier].
func (s *scanner) skipBracket(i int) int {
	if j := strings.IndexByte(s.query[i:], ']'); j >= 0 {
		return i + j + 1
	}

	s.ok = false

	return len(s.query)
}

// skipQuoted returns the index after the quoted text opened at query[i]; a
// doubled quote stays inside it, and so does a backslash-escaped character
// when backslash is set.
func (s *scanner) skipQuoted(i int, backslash bool) int {
	q := s.query
	quote := q[i]

	for j := i + 1; j < len(q); j++ {
		switch q[j] {
		case '\\':
			if backslash {
				j++
			}
		case quote:
			if j+1 < len(q) && q[j+1] == quote {
				j++
				continue
			}

			return j + 1
		}
	}

	s.ok = false

	return len(q)
}

func (s *scanner) skipLineComment(i int) int {
	if j := strings.IndexByte(s.query[i:], '\n'); j >= 0 {
		return i + j + 1
	}

	s.ok = false

	return len(s.query)
}

// skipBlockComment handles PostgreSQL's nested comments; other dialects
// end the comment at the first "*/".
func (s *scanner) skipBlockComment(i int) int {
	q := s.query
	depth := 0

	for j := i; j+1 < len(q); j++ {
		switch q[j : j+2] {
		case "/*":
			if depth == 0 || s.pg {
				depth++
			}

			j++
		case "*/":
			depth--
			j++

			if depth == 0 {
				return j + 1
			}
		}
	}

	s.ok = false

	return len(q)
}

// skipDollarQuote returns the index after a $tag$…$tag$ body opened at
// query[i], or i+1 when query[i] does not open one ($1, a lone $).
func (s *scanner) skipDollarQuote(i int) int {
	q := s.query

	j := i + 1
	for j < len(q) && isIdentByte(q[j]) {
		j++
	}

	if j > i+1 && isDigit(q[i+1]) {
		if isAllDigits(q[i+1 : j]) {
			s.dollars++ // $n parameter
		}

		return i + 1
	}

	if j >= len(q) || q[j] != '$' {
		return i + 1
	}

	tag := q[i : j+1]
	if k := strings.Index(q[j+1:], tag); k >= 0 {
		return j + 1 + k + len(tag)
	}

	s.ok = false

	return len(q)
}

func isIdentByte(c byte) bool {
	return c == '_' || isDigit(c) || (c|0x20 >= 'a' && c|0x20 <= 'z') || c >= 0x80
}

func isAllDigits(s string) bool {
	for i := range len(s) {
		if !isDigit(s[i]) {
			return false
		}
	}

	return true
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
