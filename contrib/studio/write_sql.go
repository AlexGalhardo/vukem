package studio

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/database"
)

// Field is a form value for one column: its text, or NULL.
type Field struct {
	Text string
	Null bool
}

var (
	decimalRe = regexp.MustCompile(`^[+-]?\d+(\.\d+)?$`)
	timeForms = []string{
		time.RFC3339Nano, "2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02",
	}
)

// baseType is the declared type without its length or modifiers, lower-cased:
// "varchar(255)" is "varchar", "int unsigned" is "int", "timestamp with time
// zone" is "timestamp".
func baseType(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	if i := strings.IndexAny(t, "( "); i > 0 {
		t = t[:i]
	}

	return t
}

// coerce turns a form value into the Go value to bind, by the declared type of
// the column. A value that does not fit is an input error naming the column;
// nothing is sent to the database in that case.
func coerce(col Column, f Field) (any, error) {
	if f.Null {
		if !col.Nullable {
			return nil, inputError(col.Name + ": the column does not accept NULL")
		}

		return nil, nil
	}

	bad := func(want string) error { return inputError(col.Name + ": expected " + want) }
	text := strings.TrimSpace(f.Text)

	switch baseType(col.Type) {
	case "int", "integer", "tinyint", "smallint", "mediumint", "bigint", "int2", "int4", "int8", "serial",
		"smallserial", "bigserial":
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return nil, bad("a whole number")
		}

		return n, nil
	case "bool", "boolean":
		switch strings.ToLower(text) {
		case "true", "1":
			return true, nil
		case "false", "0":
			return false, nil
		}

		return nil, bad("true, false, 1 or 0")
	case "real", "float", "double", "float4", "float8":
		n, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return nil, bad("a number")
		}

		return n, nil
	case "numeric", "decimal", "number":
		// A decimal stays text: the database converts it without losing digits.
		if !decimalRe.MatchString(text) {
			return nil, bad("a decimal number")
		}

		return text, nil
	case "date", "datetime", "timestamp", "timestamptz", "time":
		for _, layout := range timeForms {
			if tm, err := time.Parse(layout, text); err == nil {
				return tm.UTC(), nil
			}
		}

		return nil, bad("a date in the format the grid shows (2006-01-02 or RFC 3339)")
	case "json", "jsonb":
		if !json.Valid([]byte(f.Text)) {
			return nil, bad("valid JSON")
		}

		return f.Text, nil
	case "blob", "bytea", "binary", "varbinary", "tinyblob", "mediumblob", "longblob":
		return nil, inputError(col.Name + ": binary columns are read-only")
	}

	return f.Text, nil
}

// columnsOf maps column names to their definitions.
func columnsOf(t Table) map[string]Column {
	m := make(map[string]Column, len(t.Columns))
	for _, c := range t.Columns {
		m[c.Name] = c
	}

	return m
}

// errRowCount rolls back a write that did not change exactly one row.
var errRowCount = errors.New("studio: the statement did not change exactly one row")

// coerceFields checks every field against t and returns the columns in table
// order with their values. skipPK is true for an update, which never changes
// the primary key.
func coerceFields(t Table, fields map[string]Field, skipPK bool) ([]string, []any, error) {
	known := columnsOf(t)

	for name := range fields {
		c, ok := known[name]
		if !ok {
			return nil, nil, inputError("unknown column")
		}

		if skipPK && slices.Contains(t.PrimaryKey, c.Name) {
			return nil, nil, inputError(c.Name + ": the primary key cannot be changed")
		}
	}

	var (
		cols []string
		vals []any
	)

	for _, c := range t.Columns {
		f, ok := fields[c.Name]
		if !ok {
			continue
		}

		v, err := coerce(c, f)
		if err != nil {
			return nil, nil, err
		}

		cols, vals = append(cols, c.Name), append(vals, v)
	}

	return cols, vals, nil
}

// Insert implements SQLSource. A table without a primary key is read-only.
func (s *SQLDB) Insert(ctx context.Context, table string, fields map[string]Field) error {
	t, err := s.writable(ctx, table)
	if err != nil {
		return err
	}

	cols, vals, err := coerceFields(t, fields, false)
	if err != nil {
		return err
	}

	d := s.db.Dialect()

	var q string

	if len(cols) == 0 {
		q = "INSERT INTO " + d.Quote(t.Name) + " DEFAULT VALUES"
		if s.family() == "mysql" {
			q = "INSERT INTO " + d.Quote(t.Name) + " () VALUES ()"
		}
	} else {
		quoted, ph := make([]string, len(cols)), make([]string, len(cols))
		for i, c := range cols {
			quoted[i], ph[i] = d.Quote(c), d.Placeholder(i+1)
		}

		q = "INSERT INTO " + d.Quote(
			t.Name,
		) + " (" + strings.Join(
			quoted,
			", ",
		) + ") VALUES (" + strings.Join(
			ph,
			", ",
		) + ")"
	}

	return s.exactlyOne(ctx, q, vals)
}

// ConflictError says a guarded update found the row changed since it was read.
// Current holds the values of the guarded columns now; the error text carries
// none of them, so it is safe to log.
type ConflictError struct {
	Current map[string]Cell
}

func (*ConflictError) Error() string { return "studio: the row changed since it was read" }

// guard locks the row where the dialect can and compares the expected values
// with what is stored, as the grid would show them. Comparing the displayed
// text in Go keeps NULL handling and type formats identical in every dialect;
// the lock (SQLite's BEGIN IMMEDIATE already serialises writers) keeps the
// check and the UPDATE atomic.
func (s *SQLDB) guard(ctx context.Context, tx *database.Tx, t Table, key []string, expect map[string]Field) error {
	known := columnsOf(t)

	for name := range expect {
		if _, ok := known[name]; !ok {
			return inputError("unknown column")
		}
	}

	d := s.db.Dialect()

	var cols []string

	for _, c := range t.Columns {
		if _, ok := expect[c.Name]; ok {
			cols = append(cols, c.Name)
		}
	}

	quoted := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = d.Quote(c)
	}

	where, args := s.keyWhere(t, key, 0)
	q := "SELECT " + strings.Join(quoted, ", ") + " FROM " + d.Quote(t.Name) + " WHERE " + where

	if database.MustCapabilities(d).RowLocks {
		q += " FOR UPDATE"
	}

	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return &queryError{err}
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return &queryError{err}
		}

		return errRowCount
	}

	cells, err := scanRow(rows, len(cols), detailCellMax)
	if err != nil {
		return &queryError{err}
	}

	current, stale := make(map[string]Cell, len(cols)), false

	for i, c := range cols {
		current[c] = cells[i]

		if cells[i].Binary || cells[i].Truncated {
			return inputError(c + ": a binary or oversized value cannot be guarded")
		}

		want := expect[c]
		if want.Null != cells[i].Null || (!want.Null && want.Text != cells[i].Text) {
			stale = true
		}
	}

	if stale {
		return &ConflictError{Current: current}
	}

	return nil
}

// Update implements SQLSource: the columns in fields, for the row with key.
func (s *SQLDB) Update(ctx context.Context, table string, key []string, fields, expect map[string]Field) error {
	t, err := s.writable(ctx, table)
	if err != nil {
		return err
	}

	if len(key) != len(t.PrimaryKey) {
		return inputError("the key does not match the primary key")
	}

	cols, vals, err := coerceFields(t, fields, true)
	if err != nil {
		return err
	}

	if len(cols) == 0 {
		return inputError("nothing to change")
	}

	d := s.db.Dialect()
	sets := make([]string, len(cols))

	for i, c := range cols {
		sets[i] = d.Quote(c) + " = " + d.Placeholder(i+1)
	}

	where, args := s.keyWhere(t, key, len(cols))

	var pre func(context.Context, *database.Tx) error
	if len(expect) > 0 {
		pre = func(ctx context.Context, tx *database.Tx) error { return s.guard(ctx, tx, t, key, expect) }
	}

	return s.exactlyOneAfter(
		ctx,
		"UPDATE "+d.Quote(t.Name)+" SET "+strings.Join(sets, ", ")+" WHERE "+where,
		append(vals, args...),
		pre,
	)
}

// Delete implements SQLSource: the row with key.
func (s *SQLDB) Delete(ctx context.Context, table string, key []string) error {
	t, err := s.writable(ctx, table)
	if err != nil {
		return err
	}

	if len(key) != len(t.PrimaryKey) {
		return inputError("the key does not match the primary key")
	}

	where, args := s.keyWhere(t, key, 0)

	return s.exactlyOne(ctx, "DELETE FROM "+s.db.Dialect().Quote(t.Name)+" WHERE "+where, args)
}

// writable returns the table when it can be written: a primary key is the
// only way to point at one row, so a table without one is read-only.
func (s *SQLDB) writable(ctx context.Context, table string) (Table, error) {
	t, err := s.Table(ctx, table)
	if err != nil {
		return Table{}, err
	}

	if len(t.PrimaryKey) == 0 {
		return Table{}, ErrNotFound
	}

	return t, nil
}

// keyWhere is the WHERE clause on every primary key column; its placeholders
// start after skip bound values.
func (s *SQLDB) keyWhere(t Table, key []string, skip int) (string, []any) {
	d := s.db.Dialect()
	parts, args := make([]string, len(key)), make([]any, len(key))

	for i, v := range key {
		parts[i], args[i] = d.Quote(t.PrimaryKey[i])+" = "+d.Placeholder(skip+i+1), v
	}

	return strings.Join(parts, " AND "), args
}

// exactlyOne runs q in a transaction and rolls back unless it changed one row.
func (s *SQLDB) exactlyOne(ctx context.Context, q string, args []any) error {
	return s.exactlyOneAfter(ctx, q, args, nil)
}

// exactlyOneAfter is exactlyOne preceded by pre, run in the same transaction;
// an error of pre rolls everything back.
func (s *SQLDB) exactlyOneAfter(
	ctx context.Context, q string, args []any, pre func(context.Context, *database.Tx) error,
) error {
	ctx, cancel := context.WithTimeout(ctx, gridTimeout)
	defer cancel()

	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		if pre != nil {
			if err := pre(ctx, tx); err != nil {
				return err
			}
		}

		res, err := tx.ExecContext(ctx, q, args...)
		if err != nil {
			return &queryError{err}
		}

		if n, err := res.RowsAffected(); err != nil || n != 1 {
			return errRowCount
		}

		return nil
	})
	if errors.Is(err, errRowCount) {
		return &queryError{err}
	}

	return err
}

// confirmKey is what the developer types back to delete a row.
func confirmKey(key []string) string { return strings.Join(key, ",") }

// fieldNames lists the columns a write touches (never their values).
func fieldNames(fields map[string]Field) string {
	names := make([]string, 0, len(fields))
	for n := range fields {
		names = append(names, n)
	}

	slices.Sort(names)

	return strings.Join(names, ",")
}
