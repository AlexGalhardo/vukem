package studio

import (
	"context"
	"database/sql"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/database"
)

const (
	consoleMaxRows = 1000
	// consoleMaxBytes bounds the text of one result: 1000 wide rows of cut cells
	// could still be tens of megabytes of JSON.
	consoleMaxBytes = 8 << 20
	consoleTimeout  = 30 * time.Second
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
			"read-only: only SELECT, WITH, SHOW, EXPLAIN, DESCRIBE, TABLE and VALUES statements run, without INTO " +
				"and without functions that lock, sleep, signal or read files (pg_sleep, GET_LOCK, LOAD_FILE...); " +
				"anything else needs --write and the write switch")
	}

	if fam == "sqlite" && s.memory && !write {
		// Reader and writer are one connection here, so nothing is read-only.
		return ResultSet{}, inputError("the console of an in-memory SQLite database needs write mode")
	}

	rs, err := s.exec(ctx, text, write)
	rs.Kind = statementKind(text, fam)

	return rs, err
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

	var rs ResultSet

	if write && !readOnlyStatement(text, s.family()) && !returningRe.MatchString(text) {
		// A write that returns no rows: Rows cannot say how many it changed, Exec can.
		//nolint:gosec // the console runs the developer's own statement, in a transaction that rolls back on failure
		res, err := tx.ExecContext(ctx, text)
		if err != nil {
			return ResultSet{}, &queryError{err}
		}

		if n, err := res.RowsAffected(); err == nil {
			rs.Affected, rs.HasAffected = n, true
		}
	} else {
		//nolint:gosec // the console runs the developer's own statement; the read-only transaction is the guard
		rows, err := tx.QueryContext(ctx, text)
		if err != nil {
			return ResultSet{}, &queryError{err}
		}

		if rs, err = readResult(rows); err != nil {
			return ResultSet{}, err
		}
	}

	if write {
		if err := tx.Commit(); err != nil {
			return ResultSet{}, &queryError{err}
		}
	}

	rs.Elapsed = time.Since(start)

	return rs, nil
}

// returningRe finds a RETURNING clause: such a statement answers with rows.
var returningRe = regexp.MustCompile(`(?i)\breturning\b`) //nolint:gochecknoglobals // read-only

// readResult reads up to 1000 rows and closes rows.
func readResult(rows *sql.Rows) (ResultSet, error) {
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return ResultSet{}, &queryError{err}
	}

	rs := ResultSet{Columns: cols}
	size := 0

	for rows.Next() {
		if len(rs.Rows) == consoleMaxRows || size > consoleMaxBytes {
			rs.Truncate = true
			break
		}

		cells, err := scanRow(rows, len(cols), gridCellRunes)
		if err != nil {
			return ResultSet{}, &queryError{err}
		}

		for _, c := range cells {
			size += len(c.Text)
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

// readOnlyStatement reports whether text starts with a read verb, holds no
// INTO (SELECT ... INTO creates a table or writes a file) and calls none of the
// functions in deniedFunctions. SQLite also runs PRAGMA, which its read-only
// connection holds back from writing. Comments and parentheses before the verb
// are skipped, strings are not read, and a quoted name counts as a function
// name when a "(" follows it ("pg_sleep"(1)).
func readOnlyStatement(text, family string) bool {
	first, ok := "", true
	analyze, writes := false, false // under EXPLAIN: ANALYZE runs what it explains; a write verb or INTO would then write

	if !lex(text, family == "mysql", func(w string, call, quoted bool) {
		switch {
		case call && deniedFunction(w, family):
			ok = false
		case quoted:
		case first == "":
			first = w
		case first == "explain" && (w == "analyze" || w == "analyse"):
			analyze = true
		case first == "explain" && (w == "into" || writeWords[w]):
			writes = true
		case w == "into", first == "with" && writeWords[w]:
			ok = false
		}
	}) {
		return false
	}

	// A plain EXPLAIN of a write only shows its plan; EXPLAIN ANALYZE runs it.
	if analyze && writes {
		return false
	}

	if first == "pragma" {
		return ok && family == "sqlite" && readPragma(text)
	}

	return ok && readVerbs[first]
}

// writeWords are the verbs a data-modifying CTE or an EXPLAIN ANALYZE of a
// write carries behind a read verb. A quoted name never counts.
var writeWords = map[string]bool{ //nolint:gochecknoglobals // read-only
	"insert": true, "update": true, "delete": true, "merge": true, "replace": true, "create": true,
	"drop": true, "alter": true, "truncate": true,
}

// pragmaRe matches "PRAGMA name" or "PRAGMA name(argument)" and nothing else:
// "PRAGMA name = value" sets and "PRAGMA name(value)" sets too, for most names.
var pragmaRe = regexp.MustCompile( //nolint:gochecknoglobals // read-only
	`(?is)^\s*pragma\s+(?:[a-z_]\w*\.)?([a-z_]\w*)\s*(\(\s*(?:'[^']*'|"[^"]*"|\w+)\s*\))?\s*;?\s*$`)

// pragmaTables take an argument and only read.
var pragmaTables = []string{ //nolint:gochecknoglobals // read-only
	"table_info", "table_xinfo", "table_list", "index_list", "index_info", "index_xinfo", "foreign_key_list",
}

// pragmaWriters change state or do work even without an argument.
var pragmaWriters = []string{ //nolint:gochecknoglobals // read-only
	"wal_checkpoint", "optimize", "incremental_vacuum", "shrink_memory", "vacuum", "query_only", "writable_schema",
}

// readPragma reports whether a PRAGMA statement only reads. The read-only
// connection is the barrier that holds; this keeps the refusal in front.
func readPragma(text string) bool {
	m := pragmaRe.FindStringSubmatch(text)
	if m == nil {
		return false
	}

	name := strings.ToLower(m[1])
	if m[2] != "" {
		return slices.Contains(pragmaTables, name)
	}

	return !slices.Contains(pragmaWriters, name)
}

// statementKind names what a statement does, in upper case: its first word,
// or, for a WITH, the write verb it carries (a CTE that deletes is a DELETE).
// It is for display and the confirmation; the barriers do not read it.
func statementKind(text, family string) string {
	first, carried := "", ""

	lex(text, family == "mysql", func(w string, _, quoted bool) {
		switch {
		case quoted:
		case first == "":
			first = w
		case first == "with" && carried == "" && writeWords[w]:
			carried = w
		}
	})

	if carried != "" {
		first = carried
	}

	return strings.ToUpper(first)
}

// deniedFunctions are functions that have a side effect a READ ONLY
// transaction does not stop (a lock, a signal, a sleep, a file or a setting)
// or that read the server's files.
//
// shortcut: a deny list is not exhaustive (an extension or a user-defined
// function can do the same), so it is only a second layer. The barriers that
// hold are the read verb above and the read-only transaction or connection.
// Upgrade to an allow list of functions if the console ever needs to be open
// to untrusted users; today it is a loopback tool behind a session token.
var deniedFunctions = map[string][]string{
	"postgres": {
		"pg_terminate_backend", "pg_cancel_backend", "pg_reload_conf", "pg_rotate_logfile", "pg_read_file",
		"pg_read_binary_file", "pg_ls_dir", "pg_stat_file", "lo_import", "lo_export", "set_config", "pg_notify",
		"txid_current", "nextval", "setval",
	},
	"mysql": {
		"get_lock", "release_lock", "release_all_locks", "sleep", "benchmark", "load_file", "sys_exec", "sys_eval",
	},
	"sqlite": {"load_extension", "readfile", "writefile"},
}

// deniedFunction reports whether calling name is refused in family. PostgreSQL
// also refuses the dblink and pg_sleep families and the advisory lock calls.
func deniedFunction(name, family string) bool {
	if slices.Contains(deniedFunctions[family], name) {
		return true
	}

	return family == "postgres" &&
		(strings.HasPrefix(name, "dblink") || strings.HasPrefix(name, "pg_sleep") ||
			strings.HasPrefix(name, "pg_") && strings.Contains(name, "advisory") && strings.Contains(name, "lock"))
}

// singleStatement reports whether text is one SQL statement, optionally ended
// by a semicolon. A backslash escapes the next character only where backslash
// is true (MySQL); elsewhere it is an ordinary character, as the server reads it.
func singleStatement(text string, backslash bool) bool { return lex(text, backslash, nil) }

// lex walks text and reports whether it is one statement. It reads quoted
// strings and identifiers, comments and PostgreSQL dollar quoting, and fails
// closed: anything it cannot follow (an unterminated quote, a MySQL "/*!"
// comment that the server runs) is "no". word, when set, receives each word
// and each double-quoted or backtick-quoted name in lower case, with whether a
// "(" follows it.
func lex(text string, backslash bool, word func(w string, call, quoted bool)) bool {
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

			if word != nil && c != '\'' {
				word(strings.ToLower(text[i+1:j]), callFollows(text, j+1), true)
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
				word(strings.ToLower(text[i:j]), callFollows(text, j), false)
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

// callFollows reports whether the next token after text[:i], past spaces and
// comments, is an opening parenthesis: a name followed by one is a call.
func callFollows(text string, i int) bool {
	for i < len(text) {
		if text[i] <= ' ' {
			i++
			continue
		}

		if end := commentEnd(text, i, false); end >= 0 {
			i = end
			continue
		}

		return text[i] == '('
	}

	return false
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
