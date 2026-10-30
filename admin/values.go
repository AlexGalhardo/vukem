package admin

import (
	"database/sql/driver"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/orm"
)

const (
	displayTime = "2006-01-02 15:04"
	formTime    = "2006-01-02T15:04"
	listMaxRune = 80
)

// plain unwraps pointers, NullTime and driver.Valuer types into a plain Go
// value (nil for NULL).
func plain(v reflect.Value) any {
	for v.IsValid() && v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}

		v = v.Elem()
	}

	if !v.IsValid() || !v.CanInterface() {
		return nil
	}

	switch x := v.Interface().(type) {
	case time.Time:
		if x.IsZero() {
			return nil
		}

		return x
	case orm.NullTime:
		if !x.Valid {
			return nil
		}

		return x.V
	case driver.Valuer:
		out, err := x.Value()
		if err != nil {
			return nil
		}

		if b, ok := out.([]byte); ok {
			return string(b)
		}

		return out
	case fmt.Stringer:
		return x.String()
	default:
		return x
	}
}

// display formats a value for the list and the detail page.
func display(v reflect.Value) string {
	switch x := plain(v).(type) {
	case nil:
		return ""
	case time.Time:
		return x.Format(displayTime)
	case bool:
		if x {
			return "Yes"
		}

		return "No"
	case string:
		return x
	default:
		return fmt.Sprint(x)
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}

	return string(r[:n-1]) + "…"
}

// formValue formats a value for an input's value attribute.
func formValue(v reflect.Value) string {
	switch x := plain(v).(type) {
	case nil:
		return ""
	case time.Time:
		return x.Format(formTime)
	case bool:
		return strconv.FormatBool(x)
	case string:
		return x
	default:
		return fmt.Sprint(x)
	}
}

// auditValue is the JSON-friendly value recorded in the audit log.
func auditValue(v reflect.Value) any {
	switch x := plain(v).(type) {
	case time.Time:
		return x.UTC().Format(time.RFC3339)
	default:
		return x
	}
}

// nullBase returns the value type of a nullable column type shaped like
// database/sql's: sql.Null[T], sql.NullString, sql.NullInt64, sql.NullBool
// and the rest are a value field followed by Valid bool. Other types are
// returned unchanged.
func nullBase(t reflect.Type) reflect.Type {
	if t.Kind() == reflect.Struct && t.NumField() == 2 && t.Field(1).Name == "Valid" &&
		t.Field(1).Type.Kind() == reflect.Bool && t.Field(0).IsExported() {
		return t.Field(0).Type
	}

	return t
}

func filterable(t reflect.Type) bool {
	switch nullBase(t).Kind() {
	case reflect.String, reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return true
	default:
		return false
	}
}

// filterValue converts a query-string value to the column's Go kind, so it
// binds as the right SQL type; ok is false for values that cannot match.
func filterValue(t reflect.Type, s string) (any, bool) {
	switch t = nullBase(t); t.Kind() {
	case reflect.String:
		return s, true
	case reflect.Bool:
		b, err := strconv.ParseBool(s)
		return b, err == nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(s, 10, 64)
		return n, err == nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(s, 10, 64)
		return n, err == nil
	default:
		return nil, false
	}
}

// encodeKeyPart escapes "-" and "~" so composite ids can be split on "-".
// "-" becomes "~" and "~" becomes "~~". Neither is rewritten by URL decoding.
func encodeKeyPart(s string) string {
	s = strings.ReplaceAll(s, "~", "~~")
	return strings.ReplaceAll(s, "-", "~")
}

// decodeKeyPart reverses encodeKeyPart.
func decodeKeyPart(s string) string {
	var b strings.Builder

	for i := 0; i < len(s); i++ {
		if s[i] != '~' {
			b.WriteByte(s[i])
			continue
		}

		if i+1 < len(s) && s[i+1] == '~' {
			b.WriteByte('~')
			i++

			continue
		}

		b.WriteByte('-')
	}

	return b.String()
}

// keyValue converts a URL id to the primary key's Go kind.
func keyValue(t reflect.Type, s string) (any, bool) {
	if s == "" {
		return nil, false
	}

	if t.Kind() == reflect.String {
		return s, true
	}

	return filterValue(t, s)
}
