package orm

import (
	"context"
	"database/sql"
	"fmt"
	"iter"
	"reflect"
	"time"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/internal/naming"
)

// Raw runs a SELECT written with "?" placeholders and scans each row into
// R: a struct mapped by column name (the db tag, or the snake_case of the
// field), or a scalar when R is not a struct and the result has one column.
// A result column R has no field for is an error, so a misspelt alias shows
// up in a test. Placeholders follow database.RebindChecked: "?" becomes $n
// on PostgreSQL, and a query that ends inside a literal or mixes "?" with
// "$1" fails. Encrypted columns are not supported in R.
func Raw[R any](ctx context.Context, ex database.Executor, query string, args ...any) ([]R, error) {
	out := []R{}

	for v, err := range RawEach[R](ctx, ex, query, args...) {
		if err != nil {
			return nil, err
		}

		out = append(out, v)
	}

	return out, nil
}

// RawEach streams the rows of Raw; breaking out of the loop closes the rows.
func RawEach[R any](ctx context.Context, ex database.Executor, query string, args ...any) iter.Seq2[R, error] {
	return func(yield func(R, error) bool) {
		var zero R

		if err := checkDialect(ex); err != nil {
			yield(zero, err)
			return
		}

		text, err := database.RebindChecked(ex.Dialect(), query)
		if err != nil {
			yield(zero, fmt.Errorf("orm: raw: %w", err))
			return
		}

		rows, err := ex.QueryContext(ctx, text, args...)
		if err != nil {
			yield(zero, fmt.Errorf("orm: raw: %w", err))
			return
		}
		defer rows.Close()

		next, err := rowScannerOf[R](rows, ex, "raw")
		if err != nil {
			yield(zero, err)
			return
		}

		for rows.Next() {
			v, err := next(rows)
			if err != nil {
				yield(zero, err)
				return
			}

			if !yield(v, nil) {
				return
			}
		}

		if err := rows.Err(); err != nil {
			yield(zero, fmt.Errorf("orm: raw: %w", err))
		}
	}
}

// isScalar reports whether rt is scanned from one column as a whole rather
// than field by field.
func isScalar(rt reflect.Type) bool {
	return rt.Kind() != reflect.Struct || rt == reflect.TypeFor[time.Time]() ||
		reflect.PointerTo(rt).Implements(reflect.TypeFor[sql.Scanner]()) || isTimeType(rt)
}

// resultFields maps the exported fields of a result struct to column names.
func resultFields(rt reflect.Type) map[string]field {
	byColumn := map[string]field{}

	for _, f := range reflect.VisibleFields(rt) {
		if !f.IsExported() || f.Anonymous && f.Type.Kind() == reflect.Struct || isRelation(f.Type) {
			continue
		}

		col := f.Tag.Get("db")
		if col == "-" {
			continue
		}

		if col == "" {
			col = naming.Snake(f.Name)
		}

		if _, dup := byColumn[col]; !dup {
			byColumn[col] = field{column: col, index: f.Index, typ: f.Type}
		}
	}

	return byColumn
}

// rowScannerOf prepares the scan of rows into R by column name. op names the
// caller in errors ("raw", "scan").
func rowScannerOf[R any](rows *sql.Rows, ex database.Executor, op string) (func(*sql.Rows) (R, error), error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("orm: %s: columns: %w", op, err)
	}

	rt := reflect.TypeFor[R]()
	lite := isSQLite(ex)

	if isScalar(rt) {
		if len(cols) != 1 {
			return nil, fmt.Errorf("orm: %s: %s is scanned from one column, the result has %d", op, rt, len(cols))
		}

		return func(rows *sql.Rows) (R, error) {
			var v R
			if err := rows.Scan(pluckDest(&v, lite)); err != nil {
				return v, fmt.Errorf("orm: %s: scan: %w", op, err)
			}

			return v, nil
		}, nil
	}

	byColumn := resultFields(rt)
	fields := make([]field, len(cols))
	seen := map[string]bool{}

	for i, c := range cols {
		f, ok := byColumn[c]
		if !ok {
			return nil, fmt.Errorf("orm: %s: result column %q has no field in %s", op, c, rt)
		}

		if seen[c] {
			return nil, fmt.Errorf("orm: %s: duplicate result column %q; use SelectAs", op, c)
		}

		seen[c] = true

		if reflect.PointerTo(f.typ).Implements(reflect.TypeFor[opener]()) {
			return nil, fmt.Errorf("orm: %s: encrypted column %q is not supported in %s", op, c, rt)
		}

		fields[i] = f
	}

	dest := make([]any, len(cols))

	return func(rows *sql.Rows) (R, error) {
		var v R

		rv := reflect.ValueOf(&v).Elem()

		for i, f := range fields {
			dest[i] = pluckDest(rv.FieldByIndex(f.index).Addr().Interface(), lite)
		}

		if err := rows.Scan(dest...); err != nil {
			return v, fmt.Errorf("orm: %s: scan: %w", op, err)
		}

		return v, nil
	}, nil
}
