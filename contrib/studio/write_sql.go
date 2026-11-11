package studio

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
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
		if d == database.MySQL {
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

// Update implements SQLSource: the columns in fields, for the row with key.
func (s *SQLDB) Update(ctx context.Context, table string, key []string, fields map[string]Field) error {
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

	return s.exactlyOne(
		ctx,
		"UPDATE "+d.Quote(t.Name)+" SET "+strings.Join(sets, ", ")+" WHERE "+where,
		append(vals, args...),
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
	ctx, cancel := context.WithTimeout(ctx, gridTimeout)
	defer cancel()

	err := s.db.Tx(ctx, func(tx *database.Tx) error {
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

// formFields reads the col.<name> and null.<name> form values.
func formFields(r *http.Request, t Table, insert bool) (map[string]Field, error) {
	fields := map[string]Field{}

	for k, vs := range r.PostForm {
		if len(vs) == 0 {
			continue
		}

		switch {
		case strings.HasPrefix(k, "col."):
			f := fields[k[4:]]
			f.Text = vs[0]
			fields[k[4:]] = f
		case strings.HasPrefix(k, "null."):
			f := fields[k[5:]]
			f.Null = vs[0] == "1"
			fields[k[5:]] = f
		}
	}

	for name, f := range fields {
		if _, ok := columnsOf(t)[name]; !ok {
			return nil, inputError("unknown column")
		}

		// A column the browser left empty means "the default" on insert and
		// is dropped here; the update form never sends an empty column unless
		// the developer cleared it on purpose.
		if f.Text == "" && !f.Null && insert {
			delete(fields, name)
		}
	}

	return fields, nil
}

func (s *Studio) rowsBack(src SQLSource, table string) string {
	return "/s/" + url.PathEscape(src.Name()) + "/t/" + url.PathEscape(table)
}

// newRowPage shows the insert form.
func (s *Studio) newRowPage(w http.ResponseWriter, r *http.Request) {
	src := s.sql(r)
	if src == nil {
		http.NotFound(w, r)
		return
	}

	t, err := src.Table(r.Context(), r.PathValue("table"))
	if err != nil || len(t.PrimaryKey) == 0 {
		if err == nil {
			err = ErrNotFound
		}

		s.fail(w, r, err)

		return
	}

	s.page(w, r, http.StatusOK, "rowform", map[string]any{"Source": src.Name(), "Table": t})
}

// insertRow inserts one row from the form.
func (s *Studio) insertRow(w http.ResponseWriter, r *http.Request) {
	src := s.sql(r)
	if src == nil {
		http.NotFound(w, r)
		return
	}

	if err := s.form(w, r); err != nil {
		s.fail(w, r, err)
		return
	}

	table := r.PathValue("table")

	t, err := src.Table(r.Context(), table)
	if err == nil {
		var fields map[string]Field
		if fields, err = formFields(r, t, true); err == nil {
			err = src.Insert(r.Context(), table, fields)
			s.audit(r.Context(), src, "insert", table, err, "columns", fieldNames(fields))
		}
	}

	if err != nil {
		s.fail(w, r, err)
		return
	}

	// The target is built from escaped path parts, never from a form value.
	http.Redirect(w, r, s.rowsBack(src, table), http.StatusSeeOther) //nolint:gosec // G710
}

// changeRow updates or deletes the row named by the k query values. A delete
// needs the key typed back: without it, or with another value, nothing runs.
func (s *Studio) changeRow(w http.ResponseWriter, r *http.Request) {
	src := s.sql(r)
	if src == nil {
		http.NotFound(w, r)
		return
	}

	if err := s.form(w, r); err != nil {
		s.fail(w, r, err)
		return
	}

	table, key := r.PathValue("table"), r.URL.Query()["k"]
	back := s.rowsBack(src, table)

	t, err := src.Table(r.Context(), table)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	if len(t.PrimaryKey) == 0 {
		s.audit(r.Context(), src, r.PostFormValue("action"), table, ErrNotFound)
		s.fail(w, r, ErrNotFound)

		return
	}

	switch action := r.PostFormValue("action"); action {
	case "update":
		var fields map[string]Field
		if fields, err = formFields(r, t, false); err == nil {
			err = src.Update(r.Context(), table, key, fields)
			s.audit(r.Context(), src, "update", table, err, "key", confirmKey(key), "columns", fieldNames(fields))
		}

		back = rowLink(src.Name(), table, key)
	case "delete":
		if !confirmed(r, confirmKey(key)) {
			s.audit(r.Context(), src, "delete", table, inputError("confirmation did not match"), "key", confirmKey(key))
			s.fail(w, r, inputError("type the primary key to confirm the deletion"))

			return
		}

		err = src.Delete(r.Context(), table, key)
		s.audit(r.Context(), src, "delete", table, err, "key", confirmKey(key))
	default:
		err = inputError("unknown action")
	}

	if err != nil {
		s.fail(w, r, err)
		return
	}

	// back is built from escaped path parts and the key's own query encoding.
	http.Redirect(w, r, back, http.StatusSeeOther) //nolint:gosec // G710
}

// fieldNames lists the columns a write touches (never their values).
func fieldNames(fields map[string]Field) string {
	names := make([]string, 0, len(fields))
	for n := range fields {
		names = append(names, n)
	}

	slices.Sort(names)

	return strings.Join(names, ",")
}
