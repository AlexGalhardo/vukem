package orm

import (
	"fmt"
	"slices"
	"strings"
)

// join is one JOIN clause; on holds quoted identifiers and column markers.
type join struct {
	kind  string // "INNER" or "LEFT"
	table string
	on    string
}

var joinOperators = map[string]bool{"=": true, "!=": true, "<>": true, "<": true, "<=": true, ">": true, ">=": true}

// column validates name against the query's tables: its model's columns,
// unqualified or as "table.column", and "table.column" of a joined model.
// Every column name reaches SQL through it.
func (q Query[T]) column(name string) (string, error) {
	if len(q.scope) == 0 {
		return q.m.column(name)
	}

	if name == "*" {
		return name, nil
	}

	table, col, qualified := strings.Cut(name, ".")
	if !qualified {
		if _, ok := q.m.byColumn[table]; ok {
			return name, nil
		}

		return "", fmt.Errorf("orm: unknown column %q on %s", name, q.m.table)
	}

	if table == q.m.table {
		if _, ok := q.m.byColumn[col]; ok {
			return name, nil
		}

		return "", fmt.Errorf("orm: unknown column %q on %s", name, q.m.table)
	}

	for _, jm := range q.scope {
		if jm.table == table {
			if _, ok := jm.byColumn[col]; ok {
				return name, nil
			}

			return "", fmt.Errorf("orm: unknown column %q on %s", name, jm.table)
		}
	}

	return "", fmt.Errorf("orm: column %q does not belong to this query (tables: %s)", name, q.tables())
}

// tables lists the model table and the joined ones, for error messages.
func (q Query[T]) tables() string {
	names := []string{q.m.table}
	for _, jm := range q.scope {
		names = append(names, jm.table)
	}

	return strings.Join(names, ", ")
}

// InnerJoin adds INNER JOIN of model J ON left op right. left and right are
// columns validated against the query's tables ("users.id", "posts.user_id")
// once J is in scope, op is one of = != <> < <= > >=. The result stays []T:
// read other tables' columns with SelectAs and Scan. A model with
// SoftDeletes adds "deleted_at IS NULL" to the ON clause. Joining a table
// twice (self joins) is an error: use orm.Raw.
//
//	QueryOf[Post](db).InnerJoin[User]("users.id", "=", "posts.user_id").
//		Where("users.name", "Ada").Get(ctx)
func (q Query[T]) InnerJoin[J any](left, op, right string) Query[T] {
	return q.joinModel("INNER", metaOf[J](), left, op, right)
}

// LeftJoin is InnerJoin with LEFT JOIN: rows of T without a match stay.
func (q Query[T]) LeftJoin[J any](left, op, right string) Query[T] {
	return q.joinModel("LEFT", metaOf[J](), left, op, right)
}

func (q Query[T]) joinModel(kind string, jm *meta, left, op, right string) Query[T] {
	if q.err != nil {
		return q
	}

	if err := q.canJoin(jm.table); err != nil {
		return q.fail(err)
	}

	if !joinOperators[op] {
		return q.fail(fmt.Errorf("orm: invalid join operator %q", op))
	}

	q = q.clone()
	q.scope = append(q.scope, jm)

	l, err := q.column(left)
	if err != nil {
		return q.fail(err)
	}

	r, err := q.column(right)
	if err != nil {
		return q.fail(err)
	}

	on := q.joinTok(l) + " " + op + " " + q.joinTok(r)

	return q.addJoin(kind, jm, on)
}

// joinTok qualifies a validated column for an ON clause: the model's own
// columns carry its table, as the join may come before or after the others.
func (q Query[T]) joinTok(name string) string {
	if !strings.Contains(name, ".") {
		return q.quote(q.m.table + "." + name)
	}

	return q.quote(name)
}

func (q Query[T]) canJoin(table string) error {
	if table == q.m.table || slices.ContainsFunc(q.joins, func(j join) bool { return j.table == table }) {
		return fmt.Errorf("orm: %s is already joined; self joins need orm.Raw", table)
	}

	return nil
}

func (q Query[T]) addJoin(kind string, jm *meta, on string) Query[T] {
	if jm.soft {
		on += " AND " + q.quote(jm.table+".deleted_at") + " IS NULL"
	}

	q.joins = append(q.joins, join{kind: kind, table: jm.table, on: on})

	return q
}

// JoinRelation joins the related model of a relation declared on T (by
// field name) with INNER JOIN, using the relation's keys. A many-to-many
// relation joins the pivot table and then the related table. A "many"
// relation repeats the rows of T: use Distinct or WhereHas to avoid that.
func (q Query[T]) JoinRelation(name string) Query[T] { return q.joinRelation("INNER", name) }

// LeftJoinRelation is JoinRelation with LEFT JOIN.
func (q Query[T]) LeftJoinRelation(name string) Query[T] { return q.joinRelation("LEFT", name) }

func (q Query[T]) joinRelation(kind, name string) Query[T] {
	if q.err != nil {
		return q
	}

	rel, ok := q.m.relations[name]
	if !ok {
		return q.fail(fmt.Errorf("orm: %s has no relation %q", q.m.typ.Name(), name))
	}

	jm := metaOfType(rel.related)

	if err := q.canJoin(jm.table); err != nil {
		return q.fail(err)
	}

	if err := q.m.singleKey("JoinRelation"); err != nil {
		return q.fail(err)
	}

	q = q.clone()
	q.scope = append(q.scope, jm)

	base, rk := q.m.table, jm.table

	switch rel.kind {
	case relBelongsTo:
		return q.addJoin(kind, jm, q.quote(rk+"."+jm.pk)+" = "+q.quote(base+"."+rel.fk))
	case relHasOne, relHasMany:
		return q.addJoin(kind, jm, q.quote(rk+"."+rel.fk)+" = "+q.quote(base+"."+q.m.pk))
	default:
		if slices.ContainsFunc(q.joins, func(j join) bool { return j.table == rel.pivot }) {
			return q.fail(fmt.Errorf("orm: %s is already joined; self joins need orm.Raw", rel.pivot))
		}

		q.joins = append(q.joins, join{
			kind: kind, table: rel.pivot,
			on: q.quote(rel.pivot+"."+rel.fk) + " = " + q.quote(base+"."+q.m.pk),
		})

		return q.addJoin(kind, jm, q.quote(rk+"."+jm.pk)+" = "+q.quote(rel.pivot+"."+rel.rk))
	}
}
