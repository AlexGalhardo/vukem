package database

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// TimeDest is a destination ScanTime accepts.
type TimeDest interface {
	*time.Time | **time.Time | *sql.NullTime | *sql.Null[time.Time]
}

// sqliteTimeLayouts are the text forms the SQLite driver writes (its
// default is time.Time.String) and SQLite's date functions return
// (formats 1 to 7 of https://www.sqlite.org/lang_datefunc.html), plus
// RFC 3339 with "Z". Text without a zone is UTC.
var sqliteTimeLayouts = [...]string{
	"2006-01-02 15:04:05.999999999 -0700 MST",
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02T15:04:05.999999999-07:00",
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999999999",
	"2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04",
	"2006-01-02T15:04",
	"2006-01-02",
}

// ScanTime wraps a time destination so Scan also accepts a timestamp
// stored as text. SQLite reports no column type for MIN, MAX, COALESCE or
// a subquery over a timestamp column, so its driver returns the stored text
// and database/sql cannot assign it to a time.Time:
//
//	var oldest sql.NullTime
//	err := db.QueryRowContext(ctx, "SELECT MIN(created_at) FROM orders").
//		Scan(database.ScanTime(&oldest))
//
// A time.Time value (PostgreSQL, MySQL with parseTime, plain SQLite
// columns) is stored as is; NULL clears a pointer or a nullable
// destination and is an error for a plain time.Time, as in database/sql.
// Text that is not one of SQLite's timestamp forms is an error.
func ScanTime[D TimeDest](dest D) sql.Scanner { return timeScanner[D]{dest} }

type timeScanner[D TimeDest] struct{ dest D }

func (s timeScanner[D]) Scan(src any) error {
	if src == nil {
		switch d := any(s.dest).(type) {
		case **time.Time:
			*d = nil
		case *sql.NullTime:
			*d = sql.NullTime{}
		case *sql.Null[time.Time]:
			*d = sql.Null[time.Time]{}
		default:
			return errors.New("database: scan NULL into time.Time (use *time.Time or sql.NullTime)")
		}

		return nil
	}

	t, err := timeFrom(src)
	if err != nil {
		return err
	}

	switch d := any(s.dest).(type) {
	case *time.Time:
		*d = t
	case **time.Time:
		*d = new(t)
	case *sql.NullTime:
		*d = sql.NullTime{Time: t, Valid: true}
	case *sql.Null[time.Time]:
		*d = sql.Null[time.Time]{V: t, Valid: true}
	}

	return nil
}

func timeFrom(src any) (time.Time, error) {
	var text string

	switch v := src.(type) {
	case time.Time:
		return v, nil
	case string:
		text = v
	case []byte:
		text = string(v)
	default:
		return time.Time{}, fmt.Errorf("database: scan %T into time.Time", src)
	}

	// time.Time.String appends the monotonic clock reading ("m=+0.01").
	if i := strings.Index(text, " m="); i > 0 {
		text = text[:i]
	}

	for _, layout := range sqliteTimeLayouts {
		if t, err := time.ParseInLocation(layout, text, time.UTC); err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("database: scan %q into time.Time: not a timestamp", text)
}
