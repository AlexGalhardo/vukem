package orm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"iter"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/errs"
)

// ErrNotFound is returned when First/Find match no row. It matches
// errs.ErrNotFound, so handlers answer 404 by returning it.
var ErrNotFound = &errs.HTTPError{Status: 404, Message: "Record not found."}

// Direction orders results.
type Direction string

// Sort directions.
const (
	Asc  Direction = "ASC"
	Desc Direction = "DESC"
)

var operators = map[string]bool{
	"=": true, "!=": true, "<>": true, "<": true, "<=": true, ">": true, ">=": true,
	"LIKE": true, "NOT LIKE": true, "ILIKE": true,
}

type cond struct {
	or   bool
	sql  string // with "?" placeholders, renumbered at build time
	args []any
}

// Query builds a SELECT for model T. Builders are values: every method
// returns a modified copy, so a base query can be shared and refined.
type Query[T any] struct {
	ex      database.Executor
	m       *meta
	selects []string
	conds   []cond
	orders  []string
	limit   int
	offset  int
	trashed int // 0 exclude, 1 include, 2 only
	err     error
}

// QueryOf starts a query for model T on ex.
func QueryOf[T any](ex database.Executor) Query[T] {
	return Query[T]{ex: ex, m: metaOf[T](), limit: -1}
}

func (q Query[T]) clone() Query[T] {
	q.selects = slices.Clip(q.selects)
	q.conds = slices.Clip(q.conds)
	q.orders = slices.Clip(q.orders)

	return q
}

func (q Query[T]) fail(err error) Query[T] {
	if q.err == nil {
		q.err = err
	}

	return q
}

// Select limits the selected columns (validated against the model).
func (q Query[T]) Select(cols ...string) Query[T] {
	q = q.clone()

	for _, c := range cols {
		col, err := q.m.column(c)
		if err != nil {
			return q.fail(err)
		}

		q.selects = append(q.selects, col)
	}

	return q
}

// Where adds "col = value", or "col op value" when three arguments are
// given: Where("age", ">=", 18). Operators are allow-listed.
func (q Query[T]) Where(col string, args ...any) Query[T] { return q.where(false, col, args) }

// OrWhere is Where joined with OR.
func (q Query[T]) OrWhere(col string, args ...any) Query[T] { return q.where(true, col, args) }

func (q Query[T]) where(or bool, colName string, args []any) Query[T] {
	col, err := q.m.column(colName)
	if err != nil {
		return q.fail(err)
	}

	op, val := "=", any(nil)

	switch len(args) {
	case 1:
		val = args[0]
	case 2:
		s, ok := args[0].(string)
		if !ok || !operators[strings.ToUpper(s)] {
			return q.fail(fmt.Errorf("orm: invalid operator %v", args[0]))
		}

		op, val = strings.ToUpper(s), args[1]
	default:
		return q.fail(fmt.Errorf("orm: Where(%q) needs 1 or 2 arguments", colName))
	}

	if val == nil {
		switch op {
		case "=":
			return q.addCond(or, q.quote(col)+" IS NULL")
		case "!=", "<>":
			return q.addCond(or, q.quote(col)+" IS NOT NULL")
		}
	}

	return q.addCond(or, q.quote(col)+" "+op+" ?", val)
}

func (q Query[T]) addCond(or bool, sqlFrag string, args ...any) Query[T] {
	q = q.clone()
	q.conds = append(q.conds, cond{or: or, sql: sqlFrag, args: args})

	return q
}

func (q Query[T]) quote(col string) string { return q.ex.Dialect().Quote(col) }

// WhereIn adds "col IN (...)". An empty list matches nothing.
func (q Query[T]) WhereIn(col string, values ...any) Query[T] { return q.whereIn(col, values, false) }

// WhereNotIn adds "col NOT IN (...)". An empty list matches everything.
func (q Query[T]) WhereNotIn(col string, values ...any) Query[T] { return q.whereIn(col, values, true) }

func (q Query[T]) whereIn(colName string, values []any, not bool) Query[T] {
	col, err := q.m.column(colName)
	if err != nil {
		return q.fail(err)
	}

	values = flatten(values)

	if len(values) == 0 {
		if not {
			return q
		}

		return q.addCond(false, "1 = 0")
	}

	kw := " IN ("
	if not {
		kw = " NOT IN ("
	}

	return q.addCond(false, q.quote(col)+kw+strings.TrimSuffix(strings.Repeat("?, ", len(values)), ", ")+")", values...)
}

// flatten expands a single slice argument: WhereIn("id", ids) or WhereIn("id", 1, 2).
func flatten(values []any) []any {
	if len(values) != 1 {
		return values
	}

	rv := reflect.ValueOf(values[0])
	if rv.Kind() != reflect.Slice || rv.Type().Elem().Kind() == reflect.Uint8 {
		return values
	}

	out := make([]any, rv.Len())
	for i := range out {
		out[i] = rv.Index(i).Interface()
	}

	return out
}

// WhereNull adds "col IS NULL".
func (q Query[T]) WhereNull(col string) Query[T] { return q.Where(col, nil) }

// WhereNotNull adds "col IS NOT NULL".
func (q Query[T]) WhereNotNull(col string) Query[T] { return q.Where(col, "!=", nil) }

// WhereBetween adds "col BETWEEN low AND high".
func (q Query[T]) WhereBetween(colName string, low, high any) Query[T] {
	col, err := q.m.column(colName)
	if err != nil {
		return q.fail(err)
	}

	return q.addCond(false, q.quote(col)+" BETWEEN ? AND ?", low, high)
}

// WhereRaw adds a raw condition. It must use "?" placeholders for every
// value; never concatenate input into sql.
func (q Query[T]) WhereRaw(sqlFrag string, args ...any) Query[T] {
	if n := countPlaceholders(sqlFrag); n != len(args) {
		return q.fail(fmt.Errorf("orm: WhereRaw(%q) has %d placeholders but %d args", sqlFrag, n, len(args)))
	}

	return q.addCond(false, "("+sqlFrag+")", args...)
}

// countPlaceholders counts "?" outside single-quoted literals.
func countPlaceholders(s string) int {
	n, inQuote := 0, false

	for _, r := range s {
		switch {
		case r == '\'':
			inQuote = !inQuote
		case r == '?' && !inQuote:
			n++
		}
	}

	return n
}

// WhereGroup adds a parenthesised group built by fn.
func (q Query[T]) WhereGroup(fn func(g Query[T]) Query[T]) Query[T] {
	g := fn(Query[T]{ex: q.ex, m: q.m, limit: -1})
	if g.err != nil {
		return q.fail(g.err)
	}

	if len(g.conds) == 0 {
		return q
	}

	frag, args := joinConds(g.conds)

	return q.addCond(false, "("+frag+")", args...)
}

func joinConds(conds []cond) (string, []any) {
	var (
		b    strings.Builder
		args []any
	)

	for i, c := range conds {
		if i > 0 {
			if c.or {
				b.WriteString(" OR ")
			} else {
				b.WriteString(" AND ")
			}
		}

		b.WriteString(c.sql)
		args = append(args, c.args...)
	}

	return b.String(), args
}

// OrderBy adds an ORDER BY column (validated) and direction.
func (q Query[T]) OrderBy(colName string, dir Direction) Query[T] {
	col, err := q.m.column(colName)
	if err != nil {
		return q.fail(err)
	}

	if dir != Asc && dir != Desc {
		return q.fail(fmt.Errorf("orm: invalid direction %q", dir))
	}

	q = q.clone()
	q.orders = append(q.orders, q.quote(col)+" "+string(dir))

	return q
}

// Latest orders by created_at (or the primary key) descending.
func (q Query[T]) Latest() Query[T] {
	if q.m.hasCreated {
		return q.OrderBy("created_at", Desc).OrderBy(q.m.pk, Desc)
	}

	return q.OrderBy(q.m.pk, Desc)
}

// Limit sets the maximum number of rows.
func (q Query[T]) Limit(n int) Query[T] { q.limit = n; return q }

// Offset skips n rows.
func (q Query[T]) Offset(n int) Query[T] { q.offset = n; return q }

// Scope applies reusable query functions: q.Scope(Published, ByAuthor(id)).
func (q Query[T]) Scope(fns ...func(Query[T]) Query[T]) Query[T] {
	for _, fn := range fns {
		q = fn(q)
	}

	return q
}

// WithTrashed includes soft-deleted rows.
func (q Query[T]) WithTrashed() Query[T] { q.trashed = 1; return q }

// OnlyTrashed returns only soft-deleted rows.
func (q Query[T]) OnlyTrashed() Query[T] { q.trashed = 2; return q }

// build renders the WHERE clause with dialect placeholders.
func (q Query[T]) whereSQL() (string, []any) {
	conds := slices.Clone(q.conds)

	if q.m.soft {
		switch q.trashed {
		case 0:
			conds = append([]cond{{sql: q.quote("deleted_at") + " IS NULL"}}, wrapOr(conds)...)
		case 2:
			conds = append([]cond{{sql: q.quote("deleted_at") + " IS NOT NULL"}}, wrapOr(conds)...)
		}
	}

	if len(conds) == 0 {
		return "", nil
	}

	frag, args := joinConds(conds)

	return " WHERE " + frag, args
}

// wrapOr groups user conditions when they contain OR so the soft-delete
// filter cannot be bypassed by operator precedence.
func wrapOr(conds []cond) []cond {
	if !slices.ContainsFunc(conds, func(c cond) bool { return c.or }) {
		return conds
	}

	frag, args := joinConds(conds)

	return []cond{{sql: "(" + frag + ")", args: args}}
}

func (q Query[T]) selectSQL(cols string) (string, []any) {
	where, args := q.whereSQL()

	var b strings.Builder
	b.WriteString("SELECT " + cols + " FROM " + q.quote(q.m.table) + where)

	if len(q.orders) > 0 {
		b.WriteString(" ORDER BY " + strings.Join(q.orders, ", "))
	}

	if q.limit >= 0 {
		b.WriteString(" LIMIT " + strconv.Itoa(q.limit))
	}

	if q.offset > 0 {
		if q.limit < 0 && q.ex.Dialect().Name() == "sqlite" {
			b.WriteString(" LIMIT -1") // SQLite needs a LIMIT before OFFSET
		}

		b.WriteString(" OFFSET " + strconv.Itoa(q.offset))
	}

	return renumber(b.String(), q.ex.Dialect()), args
}

func (q Query[T]) columnList() string {
	if len(q.selects) > 0 {
		cols := make([]string, len(q.selects))
		for i, c := range q.selects {
			cols[i] = q.quote(c)
		}

		return strings.Join(cols, ", ")
	}

	cols := make([]string, len(q.m.fields))
	for i, f := range q.m.fields {
		cols[i] = q.quote(f.column)
	}

	return strings.Join(cols, ", ")
}

// renumber converts "?" placeholders to the dialect's style.
func renumber(sqlText string, d database.Dialect) string {
	if d.Placeholder(1) == "?" {
		return sqlText
	}

	var b strings.Builder

	n := 0
	inQuote := false

	for _, r := range sqlText {
		switch {
		case r == '\'':
			inQuote = !inQuote
			b.WriteRune(r)
		case r == '?' && !inQuote:
			n++
			b.WriteString(d.Placeholder(n))
		default:
			b.WriteRune(r)
		}
	}

	return b.String()
}

// SQL returns the SELECT statement and its arguments (debugging, tests).
func (q Query[T]) SQL() (string, []any, error) {
	if q.err != nil {
		return "", nil, q.err
	}

	s, args := q.selectSQL(q.columnList())

	return s, args, nil
}

// Get returns every matching row.
func (q Query[T]) Get(ctx context.Context) ([]T, error) {
	var out []T

	for v, err := range q.Each(ctx) {
		if err != nil {
			return nil, err
		}

		out = append(out, v)
	}

	if out == nil {
		out = []T{}
	}

	return out, nil
}

// Each streams rows without loading them all; breaking out of the loop
// closes the rows.
//
//	for post, err := range orm.QueryOf[Post](db).Each(ctx) { ... }
func (q Query[T]) Each(ctx context.Context) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		var zero T

		if q.err != nil {
			yield(zero, q.err)
			return
		}

		query, args := q.selectSQL(q.columnList())

		rows, err := q.ex.QueryContext(ctx, query, args...)
		if err != nil {
			yield(zero, fmt.Errorf("orm: query %s: %w", q.m.table, err))
			return
		}
		defer rows.Close()

		scan, err := q.m.scanner(rows)
		if err != nil {
			yield(zero, err)
			return
		}

		for rows.Next() {
			var v T
			if err := scan(rows, reflect.ValueOf(&v).Elem()); err != nil {
				yield(zero, err)
				return
			}

			if !yield(v, nil) {
				return
			}
		}

		if err := rows.Err(); err != nil {
			yield(zero, fmt.Errorf("orm: rows %s: %w", q.m.table, err))
		}
	}
}

// First returns the first matching row or ErrNotFound.
func (q Query[T]) First(ctx context.Context) (T, error) {
	if len(q.orders) == 0 {
		q = q.OrderBy(q.m.pk, Asc)
	}

	for v, err := range q.Limit(1).Each(ctx) {
		return v, err
	}

	var zero T

	return zero, ErrNotFound
}

// Find returns the row with the given primary key or ErrNotFound.
func (q Query[T]) Find(ctx context.Context, id any) (T, error) {
	return q.Where(q.m.pk, id).First(ctx)
}

// Count returns the number of matching rows.
func (q Query[T]) Count(ctx context.Context) (int64, error) {
	if q.err != nil {
		return 0, q.err
	}

	c := q
	c.orders, c.limit, c.offset = nil, -1, 0
	query, args := c.selectSQL("COUNT(*)")

	var n int64
	if err := q.ex.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("orm: count %s: %w", q.m.table, err)
	}

	return n, nil
}

// Exists reports whether any row matches.
func (q Query[T]) Exists(ctx context.Context) (bool, error) {
	n, err := q.Limit(1).Count(ctx)
	return n > 0, err
}

// Pluck returns one column of every matching row as []V.
func (q Query[T]) Pluck[V any](ctx context.Context, colName string) ([]V, error) {
	col, err := q.m.column(colName)
	if err != nil {
		return nil, err
	}

	if q.err != nil {
		return nil, q.err
	}

	query, args := q.selectSQL(q.quote(col))

	rows, err := q.ex.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("orm: pluck %s.%s: %w", q.m.table, col, err)
	}
	defer rows.Close()

	out := []V{}

	for rows.Next() {
		var v V
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("orm: pluck %s.%s: %w", q.m.table, col, err)
		}

		out = append(out, v)
	}

	return out, rows.Err()
}

// Chunk processes rows in batches of size ordered by primary key; fn may
// stop early by returning an error.
func (q Query[T]) Chunk(ctx context.Context, size int, fn func([]T) error) error {
	if size <= 0 {
		return errors.New("orm: chunk size must be positive")
	}

	for page := 0; ; page++ {
		batch, err := q.OrderBy(q.m.pk, Asc).Limit(size).Offset(page * size).Get(ctx)
		if err != nil {
			return err
		}

		if len(batch) == 0 {
			return nil
		}

		if err := fn(batch); err != nil {
			return err
		}

		if len(batch) < size {
			return nil
		}
	}
}

// Page is one page of results.
type Page[T any] struct {
	Items    []T   `json:"data"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PerPage  int   `json:"per_page"`
	LastPage int   `json:"last_page"`
}

// Paginate returns page (1-based) with perPage items and the total count.
func (q Query[T]) Paginate(ctx context.Context, page, perPage int) (Page[T], error) {
	page = max(page, 1)
	perPage = max(perPage, 1)

	total, err := q.Count(ctx)
	if err != nil {
		return Page[T]{}, err
	}

	items, err := q.Limit(perPage).Offset((page - 1) * perPage).Get(ctx)
	if err != nil {
		return Page[T]{}, err
	}

	last := int((total + int64(perPage) - 1) / int64(perPage))

	return Page[T]{Items: items, Total: total, Page: page, PerPage: perPage, LastPage: max(last, 1)}, nil
}

// scanner maps result columns to struct fields once per query.
func (m *meta) scanner(rows *sql.Rows) (func(*sql.Rows, reflect.Value) error, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	idx := make([][]int, len(cols))
	for i, c := range cols {
		if f, ok := m.byColumn[c]; ok {
			idx[i] = f.index
		}
	}

	return func(rows *sql.Rows, v reflect.Value) error {
		dest := make([]any, len(cols))

		for i, index := range idx {
			if index == nil {
				dest[i] = new(any)
				continue
			}

			dest[i] = v.FieldByIndex(index).Addr().Interface()
		}

		if err := rows.Scan(dest...); err != nil {
			return fmt.Errorf("orm: scan %s: %w", m.table, err)
		}

		return nil
	}, nil
}
