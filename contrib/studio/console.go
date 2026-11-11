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
// checking it is one statement. Read-only is not a text analysis: PostgreSQL
// and MySQL run it in a read-only transaction that always rolls back, and
// SQLite on the framework's read-only reader connection. With write, the
// statement runs in a transaction that commits only when it succeeded.
func (s *SQLDB) Query(ctx context.Context, text string, write bool) (ResultSet, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return ResultSet{}, inputError("type a statement")
	}

	d := s.db.Dialect()

	if !singleStatement(text, d == database.MySQL) {
		return ResultSet{}, inputError("the console runs one statement at a time")
	}

	if d == database.SQLite && s.memory && !write {
		// Reader and writer are one connection here, so nothing is read-only.
		return ResultSet{}, inputError("the console of an in-memory SQLite database needs write mode")
	}

	ctx, cancel := context.WithTimeout(ctx, consoleTimeout)
	defer cancel()

	start := time.Now()

	if d == database.SQLite && !write {
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

// singleStatement reports whether text is one SQL statement, optionally ended
// by a semicolon. It reads quoted strings and identifiers, comments and
// PostgreSQL dollar quoting, and fails closed: anything it cannot follow (an
// unterminated quote, a MySQL "/*!" comment that the server runs) is "no".
// A backslash escapes the next character only where backslash is true
// (MySQL); elsewhere it is an ordinary character, as the server reads it.
func singleStatement(text string, backslash bool) bool {
	ended := false

	for i := 0; i < len(text); {
		c := text[i]

		switch {
		case c == ';':
			ended = true
			i++
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '-' && strings.HasPrefix(text[i:], "--"):
			j := strings.IndexByte(text[i:], '\n')
			if j < 0 {
				return true
			}

			i += j + 1
		case c == '/' && strings.HasPrefix(text[i:], "/*"):
			if strings.HasPrefix(text[i:], "/*!") {
				return false
			}

			j := strings.Index(text[i+2:], "*/")
			if j < 0 {
				return false
			}

			i += 2 + j + 2
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
			if j == -2 {
				return false
			}

			if j < 0 {
				i++
				continue
			}

			i = j
		default:
			i++
		}
	}

	return true
}

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
