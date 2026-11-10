package devtools

import (
	"database/sql"
	"fmt"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var (
	sqlIdent        = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	sqlDollarTag    = regexp.MustCompile(`^\$(?:[A-Za-z_][A-Za-z0-9_]*)?\$`)
	sqlNumbers      = regexp.MustCompile(`\b\d+(?:\.\d+)?\b`)
	sqlDollar       = regexp.MustCompile(`\$\d+`)
	sqlSpaces       = regexp.MustCompile(`\s+`)
	sqlPlaceholders = regexp.MustCompile(`\(\s*\?(?:\s*,\s*\?)+\s*\)`)
)

// NormalizeSQL returns the statement shape used for display and N+1
// detection: literals become "?", $n placeholders become "?", lists of
// placeholders collapse to "(?...)" and whitespace is collapsed. Literal
// values never reach the dashboard.
func NormalizeSQL(query string) string {
	s := stripStrings(query)
	s = sqlDollar.ReplaceAllString(s, "?")
	s = sqlNumbers.ReplaceAllString(s, "?")
	s = sqlSpaces.ReplaceAllString(strings.TrimSpace(s), " ")

	return sqlPlaceholders.ReplaceAllString(s, "(?...)")
}

// stripStrings replaces every string literal with "?" and drops comments:
// 'x' (doubled quotes; backslash escapes only in PostgreSQL E'x'), "x"
// unless its content is a plain identifier, and $tag$x$tag$. Comments go
// first so an apostrophe in one cannot flip the quote pairing. An
// unterminated literal or comment runs to the end.
//
// shortcut: MySQL backslash escapes ('a\'b') are not understood and can
// leave a fragment; upgrade when a MySQL driver ships.
func stripStrings(q string) string {
	var b strings.Builder

	for i := 0; i < len(q); {
		if end := commentEnd(q, i); end > 0 {
			b.WriteByte(' ')

			i = end

			continue
		}

		end := literalEnd(q, i)
		switch {
		case end < 0:
			b.WriteByte(q[i])

			i++
		case q[i] == '"' && sqlIdent.MatchString(q[i+1:end-1]):
			b.WriteString(q[i:end])

			i = end
		default:
			b.WriteByte('?')

			i = end
		}
	}

	return b.String()
}

// commentEnd returns the index just past the "--" or "/* */" comment
// starting at q[i], or 0 when q[i] does not start one.
func commentEnd(q string, i int) int {
	switch {
	case strings.HasPrefix(q[i:], "--"):
		if j := strings.IndexByte(q[i:], '\n'); j >= 0 {
			return i + j + 1
		}

		return len(q)
	case strings.HasPrefix(q[i:], "/*"):
		if j := strings.Index(q[i+2:], "*/"); j >= 0 {
			return i + 2 + j + 2
		}

		return len(q)
	}

	return 0
}

// literalEnd returns the index just past the quoted run starting at q[i], or
// -1 when q[i] does not start one.
func literalEnd(q string, i int) int {
	switch q[i] {
	case '\'', '"':
		return quotedEnd(q, i, isEscapeString(q, i))
	case '$':
		tag := sqlDollarTag.FindString(q[i:])
		if tag == "" {
			return -1
		}

		if j := strings.Index(q[i+len(tag):], tag); j >= 0 {
			return i + len(tag) + j + len(tag)
		}

		return len(q)
	}

	return -1
}

// isEscapeString reports whether the quote at q[i] opens a PostgreSQL E'...'
// string.
func isEscapeString(q string, i int) bool {
	if i == 0 || q[i] != '\'' || (q[i-1] != 'E' && q[i-1] != 'e') {
		return false
	}

	if i == 1 {
		return true
	}

	c := q[i-2]

	letter := c|0x20 >= 'a' && c|0x20 <= 'z'
	digit := c >= '0' && c <= '9'

	return c != '_' && !letter && !digit
}

func quotedEnd(q string, i int, backslash bool) int {
	quote := q[i]

	for j := i + 1; j < len(q); j++ {
		switch {
		case backslash && q[j] == '\\':
			j++
		case q[j] == quote && j+1 < len(q) && q[j+1] == quote:
			j++
		case q[j] == quote:
			return j + 1
		}
	}

	return len(q)
}

// RedactArgs describes bound values by type (and length for strings and
// bytes), never by value: arguments hold user data, tokens and passwords.
func RedactArgs(args []any) []string {
	if len(args) == 0 {
		return nil
	}

	out := make([]string, len(args))
	for i, a := range args {
		out[i] = redactArg(a)
	}

	return out
}

func redactArg(a any) string {
	switch v := a.(type) {
	case nil:
		return "NULL"
	case string:
		return "string(len=" + strconv.Itoa(len(v)) + ")"
	case []byte:
		return "bytes(len=" + strconv.Itoa(len(v)) + ")"
	case bool:
		return "bool"
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return "int"
	case float32, float64:
		return "float"
	case time.Time:
		return "time"
	case sql.NamedArg:
		return v.Name + "=" + redactArg(v.Value)
	default:
		return fmt.Sprintf("%T", a)
	}
}

func isSelect(normalized string) bool {
	head, _, _ := strings.Cut(normalized, " ")
	head = strings.ToUpper(head)

	return head == "SELECT" || head == "WITH"
}

const modulePath = "github.com/AlexGalhardo/vukem"

// caller returns "dir/file.go:line" of the first frame outside the
// framework, database/sql and the runtime: the app code that ran the query.
func caller() string {
	var pcs [48]uintptr

	n := runtime.Callers(3, pcs[:])
	frames := runtime.CallersFrames(pcs[:n])

	for {
		f, more := frames.Next()
		if !skipFrame(f) {
			return shortFile(f.File) + ":" + strconv.Itoa(f.Line)
		}

		if !more {
			return ""
		}
	}
}

func skipFrame(f runtime.Frame) bool {
	fn := f.Function
	if strings.HasPrefix(fn, modulePath+".") || strings.HasPrefix(fn, modulePath+"/") {
		return !strings.HasSuffix(f.File, "_test.go")
	}

	for _, p := range []string{"runtime.", "database/sql.", "net/http.", "testing."} {
		if strings.HasPrefix(fn, p) {
			return true
		}
	}

	return false
}

func shortFile(path string) string {
	path = strings.ReplaceAll(path, "\\", "/")
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		if j := strings.LastIndexByte(path[:i], '/'); j >= 0 {
			return path[j+1:]
		}
	}

	return path
}
