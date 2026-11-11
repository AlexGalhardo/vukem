package studio

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/database"
)

const (
	consoleMaxRows = 1000
	consoleTimeout = 30 * time.Second
)

// Query implements SQLSource: the console runs the text as typed, after
// checking it is one statement and, without write, that it is a read.
// Three barriers keep a read-only console from changing data: the verb check
// below (MySQL and MariaDB commit implicitly before DDL, which ends a READ
// ONLY transaction, so the transaction alone does not hold there), a read-only
// transaction that always rolls back on PostgreSQL and MySQL, and the
// read-only reader connection on SQLite. With write, the statement runs in a
// transaction that commits only when it succeeded.
func (s *SQLDB) Query(ctx context.Context, text string, write bool) (ResultSet, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return ResultSet{}, inputError("type a statement")
	}

	fam := s.family()

	if !singleStatement(text, fam == "mysql") {
		return ResultSet{}, inputError("the console runs one statement at a time")
	}

	if !write && !readOnlyStatement(text, fam) {
		return ResultSet{}, inputError(
			"read-only: only SELECT, WITH, SHOW, EXPLAIN, DESCRIBE, TABLE and VALUES statements run; " +
				"anything else needs --write and the write switch")
	}

	if fam == "sqlite" && s.memory && !write {
		// Reader and writer are one connection here, so nothing is read-only.
		return ResultSet{}, inputError("the console of an in-memory SQLite database needs write mode")
	}

	return s.exec(ctx, text, write)
}

// family is the SQL grammar of the dialect: "sqlite", "postgres" or "mysql".
// It reads the capabilities, so a dialect built on one of them (Turso) counts.
func (s *SQLDB) family() string { return database.MustCapabilities(s.db.Dialect()).Family }

// exec runs a checked statement: on the read-only barrier unless write.
func (s *SQLDB) exec(ctx context.Context, text string, write bool) (ResultSet, error) {
	ctx, cancel := context.WithTimeout(ctx, consoleTimeout)
	defer cancel()

	start := time.Now()

	if s.family() == "sqlite" && !write {
		rows, err := s.db.QueryContext(ctx, text)
		if err != nil {
			return ResultSet{}, &queryError{err}
		}

		rs, err := readResult(rows)
		rs.Elapsed = time.Since(start)

		return rs, err
	}

	tx, err := s.db.SQL().BeginTx(ctx, &sql.TxOptions{ReadOnly: !write})
	if err != nil {
		return ResultSet{}, &queryError{err}
	}

	defer func() { _ = tx.Rollback() }()

	//nolint:gosec // the console runs the developer's own statement; the read-only transaction is the guard
	rows, err := tx.QueryContext(ctx, text)
	if err != nil {
		return ResultSet{}, &queryError{err}
	}

	rs, err := readResult(rows)
	if err != nil {
		return ResultSet{}, err
	}

	if write {
		if err := tx.Commit(); err != nil {
			return ResultSet{}, &queryError{err}
		}
	}

	rs.Elapsed = time.Since(start)

	return rs, nil
}

// readResult reads up to 1000 rows and closes rows.
func readResult(rows *sql.Rows) (ResultSet, error) {
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return ResultSet{}, &queryError{err}
	}

	rs := ResultSet{Columns: cols}

	for rows.Next() {
		if len(rs.Rows) == consoleMaxRows {
			rs.Truncate = true
			break
		}

		cells, err := scanRow(rows, len(cols), gridCellRunes)
		if err != nil {
			return ResultSet{}, &queryError{err}
		}

		rs.Rows = append(rs.Rows, cells)
	}

	if err := rows.Err(); err != nil {
		return ResultSet{}, &queryError{err}
	}

	return rs, nil
}

// readVerbs are the statements a read-only console runs.
var readVerbs = map[string]bool{
	"select": true, "with": true, "show": true, "explain": true, "describe": true, "desc": true,
	"table": true, "values": true,
}

// readOnlyStatement reports whether text starts with a read verb and holds no
// INTO (SELECT ... INTO creates a table or writes a file). SQLite also runs
// PRAGMA, which its read-only connection holds back from writing. Comments
// and parentheses before the verb are skipped, strings and quoted names are
// not read. It does not inspect functions: a read-only transaction does not
// stop a function with side effects either.
func readOnlyStatement(text, family string) bool {
	first, ok := "", true

	if !lex(text, family == "mysql", func(w string) {
		switch {
		case first == "":
			first = w
		case w == "into":
			ok = false
		}
	}) {
		return false
	}

	return ok && (readVerbs[first] || family == "sqlite" && first == "pragma")
}

// singleStatement reports whether text is one SQL statement, optionally ended
// by a semicolon. A backslash escapes the next character only where backslash
// is true (MySQL); elsewhere it is an ordinary character, as the server reads it.
func singleStatement(text string, backslash bool) bool { return lex(text, backslash, nil) }

// lex walks text and reports whether it is one statement. It reads quoted
// strings and identifiers, comments and PostgreSQL dollar quoting, and fails
// closed: anything it cannot follow (an unterminated quote, a MySQL "/*!"
// comment that the server runs) is "no". word, when set, receives each
// unquoted word in lower case.
func lex(text string, backslash bool, word func(string)) bool {
	ended := false

	for i := 0; i < len(text); {
		c := text[i]

		if end := commentEnd(text, i, backslash); end != notComment {
			if end == refuse {
				return false
			}

			i = end

			continue
		}

		switch {
		case c == ';':
			ended = true
			i++
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case ended:
			return false
		case c == '\'' || c == '"' || c == '`':
			j := closingQuote(text, i, backslash)
			if j < 0 {
				return false
			}

			i = j + 1
		case c == '$':
			j := dollarEnd(text, i)
			if j == refuse {
				return false
			}

			i = max(j, i+1)
		case isWordStart(c):
			j := wordEnd(text, i)
			if word != nil {
				word(strings.ToLower(text[i:j]))
			}

			i = j
		default:
			i++
		}
	}

	return true
}

const (
	notComment = -1
	refuse     = -2
)

// commentEnd returns the index after the comment that starts at text[i],
// notComment when none starts there and refuse for a comment the lexer cannot
// trust (unterminated, or a MySQL "/*!" that the server runs).
func commentEnd(text string, i int, backslash bool) int {
	switch {
	case strings.HasPrefix(text[i:], "--") && (!backslash || endsOrSpaced(text, i+2)):
		if j := strings.IndexByte(text[i:], '\n'); j >= 0 {
			return i + j + 1
		}

		return len(text)
	case strings.HasPrefix(text[i:], "/*"):
		j := strings.Index(text[i+2:], "*/")
		if j < 0 || strings.HasPrefix(text[i:], "/*!") {
			return refuse
		}

		return i + 2 + j + 2
	}

	return notComment
}

// wordEnd returns the index after the word that starts at text[i].
func wordEnd(text string, i int) int {
	j := i + 1
	for j < len(text) && (isWordStart(text[j]) || text[j] >= '0' && text[j] <= '9' || text[j] == '$') {
		j++
	}

	return j
}

func isWordStart(c byte) bool { return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

// endsOrSpaced reports whether text[i] is the end of text or a space or control
// character: MySQL starts a "--" comment only then ("1--1" is arithmetic).
func endsOrSpaced(text string, i int) bool { return i >= len(text) || text[i] <= ' ' }

// closingQuote returns the index of the quote that ends the one at text[i].
func closingQuote(text string, i int, backslash bool) int {
	q := text[i]

	for j := i + 1; j < len(text); j++ {
		switch text[j] {
		case '\\':
			if backslash {
				j++
			}
		case q:
			return j
		}
	}

	return -1
}

// dollarEnd returns the index after a $tag$ ... $tag$ string that starts at
// text[i], -2 when it is not closed, and -1 when text[i] is not the start of
// one (a $1 parameter).
func dollarEnd(text string, i int) int {
	j := i + 1
	for j < len(text) && (text[j] == '_' || text[j] >= 'a' && text[j] <= 'z' || text[j] >= 'A' && text[j] <= 'Z') {
		j++
	}

	if j >= len(text) || text[j] != '$' {
		return -1
	}

	tag := text[i : j+1]

	k := strings.Index(text[j+1:], tag)
	if k < 0 {
		return -2
	}

	return j + 1 + k + len(tag)
}
