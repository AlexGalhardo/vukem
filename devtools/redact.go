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
	sqlStrings      = regexp.MustCompile(`'(?:[^']|'')*'`)
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
	s := sqlStrings.ReplaceAllString(query, "?")
	s = sqlDollar.ReplaceAllString(s, "?")
	s = sqlNumbers.ReplaceAllString(s, "?")
	s = sqlSpaces.ReplaceAllString(strings.TrimSpace(s), " ")

	return sqlPlaceholders.ReplaceAllString(s, "(?...)")
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
