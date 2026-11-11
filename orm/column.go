package orm

import "fmt"

// Column is a typed reference to the column called name of model M, holding
// values of type V. codegen:models generates one per mapped field (see
// vukem.WithModels); Col builds one by hand. The compiler rejects a value of
// the wrong type (cols.Users.Email.Eq(42)) and a predicate of another model
// (QueryOf[Post](db).Filter(cols.Users.Email.Eq("x"))). The name is still
// validated against M when the predicate reaches a query, so a Column can
// never put an unchecked identifier into SQL.
type Column[M, V any] struct{ name string }

// Col returns the column called name of model M; the name is validated
// against M when a predicate built from it reaches a query.
func Col[M, V any](name string) Column[M, V] { return Column[M, V]{name: name} }

// Name is the column name.
func (c Column[M, V]) Name() string { return c.name }

type predKind int

const (
	predEq predKind = iota + 1
	predNe
	predGt
	predGte
	predLt
	predLte
	predIn
	predNotIn
	predBetween
	predIsNull
	predIsNotNull
	predLike
)

// Predicate is a condition on a column of model M, built from a Column.
type Predicate[M any] struct {
	kind predKind
	col  string
	args []any
}

// Order is an ORDER BY term on a column of model M, built from a Column.
type Order[M any] struct {
	col string
	dir Direction
}

func (c Column[M, V]) pred(kind predKind, args ...any) Predicate[M] {
	return Predicate[M]{kind: kind, col: c.name, args: args}
}

// Eq is col = v. For a NULL test use IsNull: "= NULL" never matches.
func (c Column[M, V]) Eq(v V) Predicate[M] { return c.pred(predEq, v) }

// Ne is col != v.
func (c Column[M, V]) Ne(v V) Predicate[M] { return c.pred(predNe, v) }

// Gt is col > v.
func (c Column[M, V]) Gt(v V) Predicate[M] { return c.pred(predGt, v) }

// Gte is col >= v.
func (c Column[M, V]) Gte(v V) Predicate[M] { return c.pred(predGte, v) }

// Lt is col < v.
func (c Column[M, V]) Lt(v V) Predicate[M] { return c.pred(predLt, v) }

// Lte is col <= v.
func (c Column[M, V]) Lte(v V) Predicate[M] { return c.pred(predLte, v) }

// In is col IN (vs...); an empty list matches nothing.
func (c Column[M, V]) In(vs ...V) Predicate[M] { return c.pred(predIn, anys(vs)...) }

// NotIn is col NOT IN (vs...); an empty list matches everything.
func (c Column[M, V]) NotIn(vs ...V) Predicate[M] { return c.pred(predNotIn, anys(vs)...) }

// Between is col BETWEEN low AND high.
func (c Column[M, V]) Between(low, high V) Predicate[M] { return c.pred(predBetween, low, high) }

// IsNull is col IS NULL.
func (c Column[M, V]) IsNull() Predicate[M] { return c.pred(predIsNull) }

// IsNotNull is col IS NOT NULL.
func (c Column[M, V]) IsNotNull() Predicate[M] { return c.pred(predIsNotNull) }

// Like is col LIKE pattern.
func (c Column[M, V]) Like(pattern string) Predicate[M] { return c.pred(predLike, pattern) }

// Asc orders by the column ascending.
func (c Column[M, V]) Asc() Order[M] { return Order[M]{c.name, Asc} }

// Desc orders by the column descending.
func (c Column[M, V]) Desc() Order[M] { return Order[M]{c.name, Desc} }

func anys[V any](vs []V) []any {
	out := make([]any, len(vs))
	for i, v := range vs {
		out[i] = v
	}

	return out
}

// Filter adds the predicates, joined with AND, through the same methods the
// string API uses (Where, WhereIn, WhereBetween...): one validation of the
// column, one SQL generator.
func (q Query[T]) Filter(preds ...Predicate[T]) Query[T] {
	for _, p := range preds {
		q = applyPredicate(q, p, false, "")
	}

	return q
}

// OrFilter is Filter with every predicate joined with OR (the OrWhere
// family).
func (q Query[T]) OrFilter(preds ...Predicate[T]) Query[T] {
	for _, p := range preds {
		q = applyPredicate(q, p, true, "")
	}

	return q
}

// FilterJoined adds predicates on columns of a joined model J, written with
// J's table name: J must already be joined (InnerJoin, LeftJoin or
// JoinRelation), otherwise the column does not belong to the query.
func (q Query[T]) FilterJoined[J any](preds ...Predicate[J]) Query[T] {
	table := TableOf[J]()

	for _, p := range preds {
		q = applyPredicate(q, p, false, table)
	}

	return q
}

// Sort adds ORDER BY terms in the order given.
func (q Query[T]) Sort(orders ...Order[T]) Query[T] {
	for _, o := range orders {
		q = q.OrderBy(o.col, o.dir)
	}

	return q
}

// applyPredicate translates p into the equivalent string-API call on q; table
// qualifies the column when it belongs to a joined model.
func applyPredicate[T, M any](q Query[T], p Predicate[M], or bool, table string) Query[T] {
	col := p.col
	if table != "" {
		col = table + "." + col
	}

	switch p.kind {
	case predEq:
		return q.where(or, col, p.args)
	case predNe:
		return q.where(or, col, []any{"!=", p.args[0]})
	case predGt:
		return q.where(or, col, []any{">", p.args[0]})
	case predGte:
		return q.where(or, col, []any{">=", p.args[0]})
	case predLt:
		return q.where(or, col, []any{"<", p.args[0]})
	case predLte:
		return q.where(or, col, []any{"<=", p.args[0]})
	case predIn:
		return q.whereIn(or, col, p.args, false)
	case predNotIn:
		return q.whereIn(or, col, p.args, true)
	case predBetween:
		return q.whereBetween(or, col, p.args[0], p.args[1])
	case predIsNull:
		return q.where(or, col, []any{nil})
	case predIsNotNull:
		return q.where(or, col, []any{"!=", nil})
	case predLike:
		return q.where(or, col, []any{"LIKE", p.args[0]})
	default:
		return q.fail(fmt.Errorf("orm: predicate on %q has no operation (build it from a Column)", p.col))
	}
}
