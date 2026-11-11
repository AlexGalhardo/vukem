package orm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/AlexGalhardo/vukem/internal/sqlscan"
)

// Aggregate is an aggregate function call over a column, for SelectExpr and
// Having. Build one with Count, Sum, Avg, Min or Max.
type Aggregate struct{ fn, col string }

// Count is COUNT(col); col may be "*".
func Count(col string) Aggregate { return Aggregate{"COUNT", col} }

// Sum is SUM(col).
func Sum(col string) Aggregate { return Aggregate{"SUM", col} }

// Avg is AVG(col).
func Avg(col string) Aggregate { return Aggregate{"AVG", col} }

// Min is MIN(col).
func Min(col string) Aggregate { return Aggregate{"MIN", col} }

// Max is MAX(col).
func Max(col string) Aggregate { return Aggregate{"MAX", col} }

// aggSQL validates the column against q and renders the call.
func (q Query[T]) aggSQL(a Aggregate) (string, error) {
	col, err := q.column(a.col)
	if err != nil {
		return "", err
	}

	if err := q.checkPlain(col); err != nil {
		return "", err
	}

	return a.fn + "(" + q.tok(col) + ")", nil
}

// checkPlain rejects an encrypted column, which cannot be grouped or
// aggregated (its stored form is ciphertext).
func (q Query[T]) checkPlain(name string) error {
	if name == "*" {
		return nil
	}

	table, col, qualified := strings.Cut(name, ".")
	if !qualified {
		col, table = table, q.m.table
	}

	m := q.m

	for _, jm := range q.scope {
		if jm.table == table {
			m = jm
		}
	}

	if f, ok := m.byColumn[col]; ok && reflect.PointerTo(f.typ).Implements(reflect.TypeFor[opener]()) {
		return fmt.Errorf("orm: cannot group or aggregate encrypted column %s.%s", m.table, col)
	}

	return nil
}

// Distinct makes the SELECT return distinct rows. Count then counts the
// distinct rows (SELECT COUNT(*) FROM (SELECT DISTINCT ...) AS t).
func (q Query[T]) Distinct() Query[T] { q.distinct = true; return q }

// GroupBy adds GROUP BY columns (validated against the query's tables). Read
// grouped rows with Scan; Get, First, Each and Count refuse them.
func (q Query[T]) GroupBy(cols ...string) Query[T] {
	q = q.clone()

	for _, c := range cols {
		col, err := q.column(c)
		if err != nil {
			return q.fail(err)
		}

		if err := q.checkPlain(col); err != nil {
			return q.fail(err)
		}

		q.groups = append(q.groups, col)
	}

	return q
}

// Having adds "agg op value" to HAVING; op is one of = != <> < <= > >=.
func (q Query[T]) Having(agg Aggregate, op string, value any) Query[T] {
	if !joinOperators[op] {
		return q.fail(fmt.Errorf("orm: invalid Having operator %q", op))
	}

	frag, err := q.aggSQL(agg)
	if err != nil {
		return q.fail(err)
	}

	q = q.clone()
	q.havings = append(q.havings, cond{sql: frag + " " + op + " ?", args: []any{utc(value)}})

	return q
}

// HavingRaw adds a raw HAVING condition, lexed like WhereRaw: "?" for every
// value, no input concatenated into it.
func (q Query[T]) HavingRaw(sqlFrag string, args ...any) Query[T] {
	if !rawFragmentOK(sqlFrag) {
		return q.fail(errors.New("orm: HavingRaw fragment holds a reserved control character"))
	}

	family := ""
	if q.ex != nil {
		family = capsOf(q.ex).Family
	}

	scan := sqlscan.Scan(family, sqlFrag, nil)

	switch {
	case !scan.Terminated:
		return q.fail(
			fmt.Errorf("orm: HavingRaw(%q) ends inside an unterminated literal, identifier or comment", sqlFrag),
		)
	case scan.DollarParams > 0:
		return q.fail(fmt.Errorf("orm: HavingRaw(%q) uses $n parameters; write ? placeholders", sqlFrag))
	case scan.Placeholders != len(args):
		return q.fail(
			fmt.Errorf("orm: HavingRaw(%q) has %d placeholders but %d args", sqlFrag, scan.Placeholders, len(args)),
		)
	}

	q = q.clone()
	q.havings = append(q.havings, cond{sql: "(" + sqlFrag + ")", args: args})

	return q
}

// SelectAs projects a validated column under an alias, for Scan.
func (q Query[T]) SelectAs(col, alias string) Query[T] {
	c, err := q.column(col)
	if err != nil {
		return q.fail(err)
	}

	if !identRe.MatchString(alias) {
		return q.fail(fmt.Errorf("orm: invalid alias %q", alias))
	}

	q = q.clone()
	q.exprs = append(q.exprs, q.tok(c)+" AS "+q.quote(alias))

	return q
}

// SelectExpr projects an aggregate under an alias, for Scan.
func (q Query[T]) SelectExpr(agg Aggregate, alias string) Query[T] {
	if !identRe.MatchString(alias) {
		return q.fail(fmt.Errorf("orm: invalid alias %q", alias))
	}

	frag, err := q.aggSQL(agg)
	if err != nil {
		return q.fail(err)
	}

	q = q.clone()
	q.exprs = append(q.exprs, frag+" AS "+q.quote(alias))

	return q
}

// errProjected is returned by the model readers for a query that projects
// columns or groups rows.
var errProjected = errors.New("orm: use Scan for projected or grouped queries")

// Scan runs the query and scans each row into R by column name (the db tag,
// or the snake_case of the field); a result column R has no field for, or a
// repeated one, is an error. The query must list its columns with Select,
// SelectAs or SelectExpr (those of Select first, then the others in call
// order); it honours Where, joins, GroupBy, Having, Distinct, OrderBy, Limit
// and Offset.
func (q Query[T]) Scan[R any](ctx context.Context) ([]R, error) {
	if q.err != nil {
		return nil, q.err
	}

	if len(q.selects) == 0 && len(q.exprs) == 0 {
		return nil, errors.New("orm: Scan needs an explicit column list (Select, SelectAs or SelectExpr)")
	}

	if err := q.checkLock(); err != nil {
		return nil, err
	}

	query, args, err := q.selectSQL(q.columnList())
	if err != nil {
		return nil, err
	}

	rows, err := q.ex.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("orm: scan %s: %w", q.m.table, err)
	}
	defer rows.Close()

	next, err := rowScannerOf[R](rows, q.ex, "scan")
	if err != nil {
		return nil, err
	}

	out := []R{}

	for rows.Next() {
		v, err := next(rows)
		if err != nil {
			return nil, err
		}

		out = append(out, v)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("orm: scan %s: %w", q.m.table, err)
	}

	return out, nil
}

// aggregate runs SELECT expr over the filtered rows, ignoring OrderBy,
// Limit, Offset and locks, and scans the single value into dest.
func (q Query[T]) aggregate(ctx context.Context, expr string, dest any) error {
	if q.err != nil {
		return q.err
	}

	if len(q.groups) > 0 || len(q.exprs) > 0 {
		return errProjected
	}

	q.orders, q.limit, q.offset, q.lock, q.wait = nil, -1, 0, "", ""

	query, args, err := q.selectSQL(expr)
	if err != nil {
		return err
	}

	if err := q.ex.QueryRowContext(ctx, query, args...).Scan(dest); err != nil {
		return fmt.Errorf("orm: aggregate %s: %w", q.m.table, err)
	}

	return nil
}

// Sum returns the sum of a column over the matching rows: SELECT
// COALESCE(SUM(col), 0). V is the column's Go type; no rows sum to zero.
func (q Query[T]) Sum[V any](ctx context.Context, col string) (V, error) {
	var v V

	frag, err := q.aggSQL(Sum(col))
	if err != nil {
		return v, err
	}

	err = q.aggregate(ctx, "COALESCE("+frag+", 0)", pluckDest(&v, isSQLite(q.ex)))

	return v, err
}

// Avg returns the average of a column over the matching rows as float64, or
// ErrNotFound when no row has a value (the average of nothing is NULL).
func (q Query[T]) Avg(ctx context.Context, col string) (float64, error) {
	frag, err := q.aggSQL(Avg(col))
	if err != nil {
		return 0, err
	}

	var v sql.NullFloat64
	if err := q.aggregate(ctx, frag, &v); err != nil {
		return 0, err
	}

	if !v.Valid {
		return 0, ErrNotFound
	}

	return v.Float64, nil
}

// Min returns the smallest value of a column over the matching rows, or
// ErrNotFound when no row matches.
func (q Query[T]) Min[V any](ctx context.Context, col string) (V, error) {
	return extreme[T, V](ctx, q, Min(col))
}

// Max returns the largest value of a column over the matching rows, or
// ErrNotFound when no row matches.
func (q Query[T]) Max[V any](ctx context.Context, col string) (V, error) {
	return extreme[T, V](ctx, q, Max(col))
}

func extreme[T, V any](ctx context.Context, q Query[T], agg Aggregate) (V, error) {
	var zero V

	frag, err := q.aggSQL(agg)
	if err != nil {
		return zero, err
	}

	var v sql.Null[V]
	if err := q.aggregate(ctx, frag, pluckDest(&v, isSQLite(q.ex))); err != nil {
		return zero, err
	}

	if !v.Valid {
		return zero, ErrNotFound
	}

	return v.V, nil
}
