package orm

import (
	"errors"
	"fmt"
	"strings"
)

// WhereExists adds "EXISTS (subquery)". The subquery is built on its own
// model, may correlate with this query through WhereOuter, and renders
// SELECT 1 whatever it selects. Its error, if any, becomes this query's.
//
//	hasComments := orm.QueryOf[Comment](db).WhereOuter[Post]("post_id", "=", "id")
//	posts, err := orm.QueryOf[Post](db).WhereExists(hasComments).Get(ctx)
func (q Query[T]) WhereExists[S any](sub Query[S]) Query[T] { return whereSub(q, sub, false) }

// WhereNotExists adds "NOT EXISTS (subquery)".
func (q Query[T]) WhereNotExists[S any](sub Query[S]) Query[T] { return whereSub(q, sub, true) }

func whereSub[T, S any](q Query[T], sub Query[S], not bool) Query[T] {
	if sub.err != nil {
		return q.fail(sub.err)
	}

	if err := checkDialect(sub.ex); err != nil {
		return q.fail(err)
	}

	text, args := sub.rawSelectSQL("1")

	kw := "EXISTS ("
	if not {
		kw = "NOT EXISTS ("
	}

	return q.addCond(false, kw+text+")", args...)
}

// WhereInQuery adds "col IN (subquery)"; the subquery must project exactly
// one column (Select, SelectAs or SelectExpr). A Limit inside IN is rejected
// by MySQL and the database's error is returned as it comes.
func (q Query[T]) WhereInQuery[S any](col string, sub Query[S]) Query[T] {
	return whereInSub(q, col, sub, false)
}

// WhereNotInQuery adds "col NOT IN (subquery)".
func (q Query[T]) WhereNotInQuery[S any](col string, sub Query[S]) Query[T] {
	return whereInSub(q, col, sub, true)
}

func whereInSub[T, S any](q Query[T], colName string, sub Query[S], not bool) Query[T] {
	col, err := q.column(colName)
	if err != nil {
		return q.fail(err)
	}

	if sub.err != nil {
		return q.fail(sub.err)
	}

	if len(sub.selects)+len(sub.exprs) != 1 {
		return q.fail(errors.New("orm: WhereInQuery needs a subquery that selects exactly one column"))
	}

	if err := checkDialect(sub.ex); err != nil {
		return q.fail(err)
	}

	text, args := sub.rawSelectSQL(sub.columnList())

	kw := " IN ("
	if not {
		kw = " NOT IN ("
	}

	return q.addCond(false, q.tok(col)+kw+text+")", args...)
}

// WhereColumn adds "left op right" comparing two columns of the query's
// tables; op is one of = != <> < <= > >=.
func (q Query[T]) WhereColumn(left, op, right string) Query[T] {
	if !joinOperators[op] {
		return q.fail(fmt.Errorf("orm: invalid operator %q in WhereColumn", op))
	}

	l, err := q.column(left)
	if err != nil {
		return q.fail(err)
	}

	r, err := q.column(right)
	if err != nil {
		return q.fail(err)
	}

	return q.addCond(false, q.tok(l)+" "+op+" "+q.tok(r))
}

// WhereOuter correlates a subquery with the query it is embedded in: it adds
// "col op outerCol", where col belongs to this (the inner) query and outerCol
// to model O, the outer one. Both sides are written with their table name,
// which is why the two models must be different tables.
func (q Query[T]) WhereOuter[O any](col, op, outerCol string) Query[T] {
	if !joinOperators[op] {
		return q.fail(fmt.Errorf("orm: invalid operator %q in WhereOuter", op))
	}

	inner, err := q.column(col)
	if err != nil {
		return q.fail(err)
	}

	om := metaOf[O]()
	if om.table == q.m.table {
		return q.fail(errors.New("orm: WhereOuter needs two different tables; self joins need orm.Raw"))
	}

	name := strings.TrimPrefix(outerCol, om.table+".")
	if _, ok := om.byColumn[name]; !ok {
		return q.fail(fmt.Errorf("orm: unknown column %q on %s", outerCol, om.table))
	}

	if !strings.Contains(inner, ".") {
		inner = q.m.table + "." + inner
	}

	return q.addCond(false, q.quote(inner)+" "+op+" "+q.quote(om.table+"."+name))
}
