package orm

import (
	"errors"
	"fmt"
	"reflect"
)

// WhereHas keeps the rows that have at least one related row, optionally
// narrowed by fn (which may be nil): "EXISTS (SELECT 1 FROM related WHERE
// related.fk = this.pk AND ...)". It never repeats rows of T, so it pages
// correctly where a join would not. R must be the relation's model. The
// related model's soft delete applies inside the EXISTS.
func (q Query[T]) WhereHas[R any](relation string, fn func(Query[R]) Query[R]) Query[T] {
	return whereHas(q, relation, fn, false)
}

// WhereDoesntHave keeps the rows without a related row that fn (may be nil)
// accepts: "NOT EXISTS (...)".
func (q Query[T]) WhereDoesntHave[R any](relation string, fn func(Query[R]) Query[R]) Query[T] {
	return whereHas(q, relation, fn, true)
}

func whereHas[T, R any](q Query[T], name string, fn func(Query[R]) Query[R], not bool) Query[T] {
	if q.err != nil {
		return q
	}

	rel, ok := q.m.relations[name]
	if !ok {
		return q.fail(fmt.Errorf("orm: %s has no relation %q", q.m.typ.Name(), name))
	}

	if rel.related != reflect.TypeFor[R]() {
		return q.fail(
			fmt.Errorf("orm: relation %q of %s is %s, not %s", name, q.m.typ.Name(), rel.related, reflect.TypeFor[R]()),
		)
	}

	if err := q.m.singleKey("WhereHas"); err != nil {
		return q.fail(err)
	}

	sub := QueryOf[R](q.ex)
	if fn != nil {
		sub = fn(sub)
	}

	if sub.err != nil {
		return q.fail(sub.err)
	}

	rm := sub.m
	if rm.table == q.m.table {
		return q.fail(errors.New("orm: WhereHas on a relation to the same table needs orm.Raw"))
	}

	// Group an OR filter of fn so the correlation applies to all of it.
	sub = sub.grouped()

	base := q.m.table

	switch rel.kind {
	case relBelongsTo:
		sub = sub.addCond(false, sub.quote(rm.table+"."+rm.pk)+" = "+sub.quote(base+"."+rel.fk))
	case relHasOne, relHasMany:
		sub = sub.addCond(false, sub.quote(rm.table+"."+rel.fk)+" = "+sub.quote(base+"."+q.m.pk))
	default:
		// The pivot joins in, which also makes the related columns of fn
		// render qualified.
		sub = sub.clone()
		sub.joins = append(sub.joins, join{
			kind: "INNER", table: rel.pivot,
			on: sub.quote(rel.pivot+"."+rel.rk) + " = " + sub.quote(rm.table+"."+rm.pk),
		})
		sub = sub.addCond(false, sub.quote(rel.pivot+"."+rel.fk)+" = "+sub.quote(base+"."+q.m.pk))
	}

	return whereSub(q, sub, not)
}
