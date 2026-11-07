package orm

import (
	"database/sql"
	"reflect"
	"time"

	"github.com/AlexGalhardo/vukem/database"
)

// isSQLite reports whether ex is SQLite, whose driver returns a timestamp
// as text when the column has no DATETIME-like declared type (TEXT, none,
// or an expression), so time fields are scanned through database.ScanTime.
func isSQLite(ex database.Executor) bool { return ex.Dialect().Name() == "sqlite" }

func isTimeType(t reflect.Type) bool {
	switch t {
	case reflect.TypeFor[time.Time](), reflect.TypeFor[*time.Time](), reflect.TypeFor[sql.NullTime](),
		reflect.TypeFor[sql.Null[time.Time]](), reflect.TypeFor[NullTime]():
		return true
	default:
		return false
	}
}

// timeDest wraps a pointer to a time field (see isTimeType) in
// database.ScanTime; other pointers pass through.
func timeDest(p any) any {
	switch d := p.(type) {
	case *time.Time:
		return database.ScanTime(d)
	case **time.Time:
		return database.ScanTime(d)
	case *sql.NullTime:
		return database.ScanTime(d)
	case *sql.Null[time.Time]:
		return database.ScanTime(d)
	case *NullTime:
		return database.ScanTime(&d.Null)
	default:
		return p
	}
}

// pluckDest is &v, wrapped by timeDest when lite.
func pluckDest(p any, lite bool) any {
	if lite {
		return timeDest(p)
	}

	return p
}

// utc returns v with a time converted to UTC, so every time bound by the
// ORM is written and compared in one zone. SQLite keeps a time as text that
// includes its zone, and compares text, so a time in another zone would
// sort by its clock reading instead of its instant. Other values pass
// through.
func utc(v any) any {
	switch t := v.(type) {
	case time.Time:
		return t.UTC()
	case *time.Time:
		if t == nil {
			return v
		}

		return t.UTC()
	case sql.NullTime:
		t.Time = t.Time.UTC()
		return t
	case sql.Null[time.Time]:
		t.V = t.V.UTC()
		return t
	case NullTime:
		t.V = t.V.UTC()
		return t
	default:
		return v
	}
}
