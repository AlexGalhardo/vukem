package orm

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/internal/naming"
)

// ErrNotLoaded is returned when a relation is read without being eager
// loaded with Query.With. Relations never query lazily, so there are no
// hidden N+1 queries (strict mode).
var ErrNotLoaded = errors.New("orm: relation not loaded (use Query.With)")

type relKind int

const (
	relBelongsTo relKind = iota + 1
	relHasOne
	relHasMany
	relManyToMany
)

// relationField is implemented by the relation field types; meta uses it
// to skip them as columns and to find the related model.
type relationField interface {
	relKind() relKind
	relatedType() reflect.Type
}

// relationSetter receives eager-loaded values.
type relationSetter interface {
	setLoaded(values []reflect.Value)
	// loadedItems are the addressable related structs, for the next level
	// of a nested With.
	loadedItems() []reflect.Value
}

// BelongsTo is a relation to the model referenced by a foreign key on this
// model: Post.User with column user_id. Override the column with the tag
// `orm:"fk:author_id"`.
type BelongsTo[T any] struct {
	v      *T
	loaded bool
}

// HasOne is a relation to one model holding a foreign key to this one:
// User.Profile with profiles.user_id. Tag: `orm:"fk:owner_id"`.
type HasOne[T any] struct {
	v      *T
	loaded bool
}

// HasMany is a relation to the models holding a foreign key to this one:
// User.Posts with posts.user_id. Tag: `orm:"fk:author_id"`.
type HasMany[T any] struct {
	items  []T
	loaded bool
}

// ManyToMany relates models through a pivot table: Post.Tags with table
// post_tag (the two singular table names, sorted) and columns post_id and
// tag_id. Tag: `orm:"pivot:taggings,fk:post_id,rk:tag_id"`.
type ManyToMany[T any] struct {
	items  []T
	loaded bool
}

func (BelongsTo[T]) relKind() relKind           { return relBelongsTo }
func (BelongsTo[T]) relatedType() reflect.Type  { return reflect.TypeFor[T]() }
func (HasOne[T]) relKind() relKind              { return relHasOne }
func (HasOne[T]) relatedType() reflect.Type     { return reflect.TypeFor[T]() }
func (HasMany[T]) relKind() relKind             { return relHasMany }
func (HasMany[T]) relatedType() reflect.Type    { return reflect.TypeFor[T]() }
func (ManyToMany[T]) relKind() relKind          { return relManyToMany }
func (ManyToMany[T]) relatedType() reflect.Type { return reflect.TypeFor[T]() }

// Get returns the related model, nil when the foreign key points nowhere,
// or ErrNotLoaded.
func (r BelongsTo[T]) Get() (*T, error) { return one(r.v, r.loaded) }

// Get returns the related model (nil when none) or ErrNotLoaded.
func (r HasOne[T]) Get() (*T, error) { return one(r.v, r.loaded) }

// Get returns the related models or ErrNotLoaded.
func (r HasMany[T]) Get() ([]T, error) { return many(r.items, r.loaded) }

// Get returns the related models or ErrNotLoaded.
func (r ManyToMany[T]) Get() ([]T, error) { return many(r.items, r.loaded) }

// Loaded reports whether the relation was eager loaded.
func (r BelongsTo[T]) Loaded() bool { return r.loaded }

// Loaded reports whether the relation was eager loaded.
func (r HasOne[T]) Loaded() bool { return r.loaded }

// Loaded reports whether the relation was eager loaded.
func (r HasMany[T]) Loaded() bool { return r.loaded }

// Loaded reports whether the relation was eager loaded.
func (r ManyToMany[T]) Loaded() bool { return r.loaded }

// IsZero makes `json:",omitzero"` drop relations that were not loaded.
func (r BelongsTo[T]) IsZero() bool { return !r.loaded }

// IsZero makes `json:",omitzero"` drop relations that were not loaded.
func (r HasOne[T]) IsZero() bool { return !r.loaded }

// IsZero makes `json:",omitzero"` drop relations that were not loaded.
func (r HasMany[T]) IsZero() bool { return !r.loaded }

// IsZero makes `json:",omitzero"` drop relations that were not loaded.
func (r ManyToMany[T]) IsZero() bool { return !r.loaded }

// MarshalJSON encodes the related model (null when none or not loaded).
func (r BelongsTo[T]) MarshalJSON() ([]byte, error) { return json.Marshal(r.v) }

// MarshalJSON encodes the related model (null when none or not loaded).
func (r HasOne[T]) MarshalJSON() ([]byte, error) { return json.Marshal(r.v) }

// MarshalJSON encodes the related models ([] when loaded and empty).
func (r HasMany[T]) MarshalJSON() ([]byte, error) { return marshalMany(r.items, r.loaded) }

// MarshalJSON encodes the related models ([] when loaded and empty).
func (r ManyToMany[T]) MarshalJSON() ([]byte, error) { return marshalMany(r.items, r.loaded) }

func (r *BelongsTo[T]) setLoaded(vs []reflect.Value) { r.v, r.loaded = first[T](vs), true }
func (r *HasOne[T]) setLoaded(vs []reflect.Value)    { r.v, r.loaded = first[T](vs), true }
func (r *HasMany[T]) setLoaded(vs []reflect.Value)   { r.items, r.loaded = all[T](vs), true }
func (r *ManyToMany[T]) setLoaded(vs []reflect.Value) {
	r.items, r.loaded = all[T](vs), true
}

func (r *BelongsTo[T]) loadedItems() []reflect.Value { return ptrItem(r.v) }
func (r *HasOne[T]) loadedItems() []reflect.Value    { return ptrItem(r.v) }
func (r *HasMany[T]) loadedItems() []reflect.Value   { return sliceItems(r.items) }
func (r *ManyToMany[T]) loadedItems() []reflect.Value {
	return sliceItems(r.items)
}

func ptrItem[T any](v *T) []reflect.Value {
	if v == nil {
		return nil
	}

	return []reflect.Value{reflect.ValueOf(v).Elem()}
}

func sliceItems[T any](items []T) []reflect.Value {
	out := make([]reflect.Value, len(items))
	rv := reflect.ValueOf(items)

	for i := range out {
		out[i] = rv.Index(i)
	}

	return out
}

func one[T any](v *T, loaded bool) (*T, error) {
	if !loaded {
		return nil, ErrNotLoaded
	}

	return v, nil
}

func many[T any](items []T, loaded bool) ([]T, error) {
	if !loaded {
		return nil, ErrNotLoaded
	}

	return items, nil
}

func marshalMany[T any](items []T, loaded bool) ([]byte, error) {
	if loaded && items == nil {
		return []byte("[]"), nil
	}

	return json.Marshal(items)
}

func first[T any](vs []reflect.Value) *T {
	if len(vs) == 0 {
		return nil
	}

	v, _ := reflect.TypeAssert[T](vs[0])

	return &v
}

func all[T any](vs []reflect.Value) []T {
	out := make([]T, len(vs))
	for i, v := range vs {
		out[i], _ = reflect.TypeAssert[T](v)
	}

	return out
}

// relation describes one relation field of a model.
type relation struct {
	name    string
	kind    relKind
	index   []int
	related reflect.Type
	fk      string // belongsTo: column on this model; hasOne/hasMany: column on related; m2m: pivot column for this model
	rk      string // m2m: pivot column for the related model
	pivot   string // m2m: pivot table
}

var relationIface = reflect.TypeFor[relationField]()

func isRelation(t reflect.Type) bool { return t.Implements(relationIface) }

// buildRelation reads conventions and the `orm` tag for field f of model rt.
func buildRelation(rt reflect.Type, f reflect.StructField) relation {
	rf, _ := reflect.TypeAssert[relationField](reflect.New(f.Type).Elem())
	r := relation{name: f.Name, kind: rf.relKind(), index: f.Index, related: rf.relatedType()}

	owner := naming.Snake(rt.Name())
	other := naming.Snake(r.related.Name())

	switch r.kind {
	case relBelongsTo:
		r.fk = naming.Snake(f.Name) + "_id"
	case relHasOne, relHasMany:
		r.fk = owner + "_id"
	case relManyToMany:
		names := []string{owner, other}
		sort.Strings(names)
		r.pivot, r.fk, r.rk = strings.Join(names, "_"), owner+"_id", other+"_id"
	}

	for opt := range strings.SplitSeq(f.Tag.Get("orm"), ",") {
		k, v, _ := strings.Cut(strings.TrimSpace(opt), ":")
		switch k {
		case "fk":
			r.fk = v
		case "rk":
			r.rk = v
		case "pivot":
			r.pivot = v
		case "":
		default:
			panic(fmt.Sprintf("orm: %s.%s has unknown orm tag option %q", rt, f.Name, k))
		}
	}

	for _, ident := range []string{r.fk, r.rk, r.pivot} {
		if ident != "" && !identRe.MatchString(ident) {
			panic(fmt.Sprintf("orm: %s.%s has invalid relation identifier %q", rt, f.Name, ident))
		}
	}

	return r
}

// withSpec is one With or WithWhere call: the relation path ("Comments.User"
// loads Comments, then User of every loaded comment) and, for WithWhere, the
// restriction of the last relation's query.
type withSpec struct {
	path       []string
	restricted bool
	where      string // fragment with "?" placeholders; "" for none
	args       []any
	orders     []string
	trashed    int
}

// With eager loads relations (by field name) for Get, First, Find,
// Paginate and CursorPaginate: one query per relation, batched with IN, so
// N rows cost 1 + len(relations) queries. A dotted name loads a nested
// relation ("Comments.User": one more query for the users of every loaded
// comment); each level loads all its rows, with no limit per parent. Each
// does not eager load.
func (q Query[T]) With(relations ...string) Query[T] {
	for _, name := range relations {
		if _, err := q.relationPath(name); err != nil {
			return q.fail(err)
		}
	}

	q = q.clone()

	for _, name := range relations {
		q.withs = append(q.withs, withSpec{path: strings.Split(name, ".")})
	}

	return q
}

// relationPath validates a dotted relation path against the models it walks
// and returns the last relation.
func (q Query[T]) relationPath(name string) (relation, error) {
	m := q.m

	var rel relation

	for seg := range strings.SplitSeq(name, ".") {
		var ok bool
		if rel, ok = m.relations[seg]; !ok {
			return relation{}, fmt.Errorf("orm: %s has no relation %q", m.typ.Name(), seg)
		}

		m = metaOfType(rel.related)
	}

	return rel, nil
}

// WithWhere is With for one relation (a dotted path restricts its last
// level) whose rows are narrowed by fn: the Where conditions and OrderBy of
// the query fn returns apply to the relation's query. Limit, Offset, joins,
// locks and projections are errors, since the rows are loaded for every
// parent at once.
func (q Query[T]) WithWhere[R any](relation string, fn func(Query[R]) Query[R]) Query[T] {
	rel, err := q.relationPath(relation)
	if err != nil {
		return q.fail(err)
	}

	if rel.related != reflect.TypeFor[R]() {
		return q.fail(fmt.Errorf("orm: relation %q of %s is %s, not %s",
			relation, q.m.typ.Name(), rel.related, reflect.TypeFor[R]()))
	}

	spec := withSpec{path: strings.Split(relation, "."), restricted: true}

	if slices.ContainsFunc(q.withs, func(w withSpec) bool { return w.restricted && slices.Equal(w.path, spec.path) }) {
		return q.fail(fmt.Errorf("orm: WithWhere(%q) given more than once", relation))
	}

	if fn != nil {
		sq := fn(QueryOf[R](q.ex))
		if sq.err != nil {
			return q.fail(sq.err)
		}

		if sq.limit >= 0 || sq.offset > 0 || sq.lock != "" || len(sq.joins) > 0 || len(sq.withs) > 0 ||
			len(sq.selects) > 0 || len(sq.exprs) > 0 || len(sq.groups) > 0 || sq.distinct || len(sq.counts) > 0 {
			return q.fail(errors.New("orm: WithWhere supports Where and OrderBy only"))
		}

		spec.trashed = sq.trashed
		spec.orders = make([]string, len(sq.orders))

		for i, o := range sq.orders {
			spec.orders[i] = sq.resolve(o)
		}

		if len(sq.conds) > 0 {
			frag, args := joinConds(sq.conds)
			spec.where, spec.args = sq.resolve(frag), args
		}
	}

	q = q.clone()
	q.withs = append(q.withs, spec)

	return q
}

// WithCount loads the number of related rows of HasMany and ManyToMany
// relations into the int64 field tagged orm:"count:<Relation>": one query
// per relation, counting by parent key in batches.
func (q Query[T]) WithCount(relations ...string) Query[T] {
	for _, name := range relations {
		rel, ok := q.m.relations[name]
		if !ok {
			return q.fail(fmt.Errorf("orm: %s has no relation %q", q.m.typ.Name(), name))
		}

		if rel.kind != relHasMany && rel.kind != relManyToMany {
			return q.fail(
				fmt.Errorf("orm: WithCount supports HasMany and ManyToMany, not %q of %s", name, q.m.typ.Name()),
			)
		}

		if _, ok := q.m.counts[name]; !ok {
			return q.fail(fmt.Errorf("orm: %s has no field tagged orm:\"count:%s\"", q.m.typ.Name(), name))
		}
	}

	q = q.clone()
	q.counts = append(q.counts, relations...)

	return q
}

// sql is the tail of an eager-loading query after "col IN (...)": the soft
// delete filter, the restriction of a WithWhere and the order, with the
// arguments of the restriction. A nil spec is the default: live rows by key.
func (s *withSpec) sql(d database.Dialect, rm *meta) (string, []any) {
	var (
		b    strings.Builder
		args []any
	)

	trashed := 0
	if s != nil {
		trashed = s.trashed
	}

	if rm.soft {
		switch trashed {
		case 0:
			b.WriteString(" AND " + d.Quote("deleted_at") + " IS NULL")
		case 2:
			b.WriteString(" AND " + d.Quote("deleted_at") + " IS NOT NULL")
		}
	}

	if s != nil && s.where != "" {
		b.WriteString(" AND (" + s.where + ")")
		args = s.args
	}

	if s != nil && len(s.orders) > 0 {
		b.WriteString(" ORDER BY " + strings.Join(s.orders, ", "))
	} else {
		b.WriteString(" ORDER BY " + d.Quote(rm.pk))
	}

	return b.String(), args
}

// loadNode is a relation to load and the relations nested under it.
type loadNode struct {
	name     string
	spec     *withSpec // restriction, when a WithWhere named this level
	children []*loadNode
}

func buildLoadTree(specs []withSpec) []*loadNode {
	var roots []*loadNode

	for i := range specs {
		level := &roots

		for d, seg := range specs[i].path {
			idx := slices.IndexFunc(*level, func(n *loadNode) bool { return n.name == seg })
			if idx < 0 {
				*level = append(*level, &loadNode{name: seg})
				idx = len(*level) - 1
			}

			node := (*level)[idx]
			if d == len(specs[i].path)-1 && specs[i].restricted {
				node.spec = &specs[i]
			}

			level = &node.children
		}
	}

	return roots
}

// loadRelations fills the requested relations of items (a []T) in place,
// then the counts.
func loadRelations(
	ctx context.Context, ex database.Executor, m *meta, items reflect.Value, specs []withSpec, counts []string,
) error {
	if items.Len() == 0 {
		return nil
	}

	elems := make([]reflect.Value, items.Len())
	for i := range elems {
		elems[i] = items.Index(i)
	}

	if err := loadLevel(ctx, ex, m, elems, buildLoadTree(specs)); err != nil {
		return err
	}

	return loadCounts(ctx, ex, m, elems, counts)
}

func loadLevel(ctx context.Context, ex database.Executor, m *meta, elems []reflect.Value, nodes []*loadNode) error {
	for _, n := range nodes {
		rel := m.relations[n.name]
		rm := metaOfType(rel.related)

		err := m.singleKey("a relation")
		if rel.kind == relBelongsTo {
			err = rm.singleKey("a belongs-to relation")
		}

		if err != nil {
			return fmt.Errorf("orm: load %s.%s: %w", m.typ.Name(), n.name, err)
		}

		switch rel.kind {
		case relBelongsTo:
			err = loadKeyed(ctx, ex, elems, rel, rm, m.byColumn[rel.fk], rm.pk, n.spec)
		case relHasOne, relHasMany:
			err = loadKeyed(ctx, ex, elems, rel, rm, m.byColumn[m.pk], rel.fk, n.spec)
		case relManyToMany:
			err = loadPivot(ctx, ex, m, elems, rel, rm, n.spec)
		}

		if err != nil {
			return fmt.Errorf("orm: load %s.%s: %w", m.typ.Name(), n.name, err)
		}

		if len(n.children) == 0 {
			continue
		}

		var next []reflect.Value

		for _, e := range elems {
			s, _ := reflect.TypeAssert[relationSetter](e.FieldByIndex(rel.index).Addr())
			next = append(next, s.loadedItems()...)
		}

		if err := loadLevel(ctx, ex, rm, next, n.children); err != nil {
			return err
		}
	}

	return nil
}

// loadCounts fills the orm:"count:..." fields: SELECT fk, COUNT(*) ... GROUP
// BY fk for the parents' keys, a batch at a time.
func loadCounts(ctx context.Context, ex database.Executor, m *meta, elems []reflect.Value, names []string) error {
	if len(names) == 0 {
		return nil
	}

	if err := m.singleKey("WithCount"); err != nil {
		return err
	}

	local := m.byColumn[m.pk]
	d := ex.Dialect()

	keys := make([]any, 0, len(elems))

	for _, e := range elems {
		if k, ok := keyOf(e.FieldByIndex(local.index)); ok {
			keys = append(keys, k)
		}
	}

	keys = uniq(keys)

	for _, name := range names {
		rel := m.relations[name]
		rm := metaOfType(rel.related)
		counts := map[any]int64{}

		for chunk := range slices.Chunk(keys, eagerChunk) {
			err := eachRow(
				ctx,
				ex,
				rebind(relationCountSQL(d, rel, rm, len(chunk)), d),
				chunk,
				func(rows *sql.Rows) error {
					var (
						k any
						n int64
					)

					if err := rows.Scan(&k, &n); err != nil {
						return err
					}

					counts[normKey(k)] = n

					return nil
				},
			)
			if err != nil {
				return fmt.Errorf("orm: count %s.%s: %w", m.typ.Name(), name, err)
			}
		}

		field := m.counts[name]

		for _, e := range elems {
			var n int64
			if k, ok := keyOf(e.FieldByIndex(local.index)); ok {
				n = counts[k]
			}

			e.FieldByIndex(field).SetInt(n)
		}
	}

	return nil
}

// relationCountSQL counts the related rows of a HasMany or ManyToMany relation by
// parent key; soft-deleted related rows are not counted.
func relationCountSQL(d database.Dialect, rel relation, rm *meta, n int) string {
	if rel.kind == relManyToMany {
		q := "SELECT " + d.Quote(rel.pivot+"."+rel.fk) + ", COUNT(*) FROM " + d.Quote(rel.pivot) +
			" INNER JOIN " + d.Quote(rm.table) + " ON " + d.Quote(rm.table+"."+rm.pk) + " = " +
			d.Quote(rel.pivot+"."+rel.rk) + " WHERE " + d.Quote(rel.pivot+"."+rel.fk) + " IN (" + marks(n) + ")"
		if rm.soft {
			q += " AND " + d.Quote(rm.table+".deleted_at") + " IS NULL"
		}

		return q + " GROUP BY " + d.Quote(rel.pivot+"."+rel.fk)
	}

	q := "SELECT " + d.Quote(rel.fk) + ", COUNT(*) FROM " + d.Quote(rm.table) +
		" WHERE " + d.Quote(rel.fk) + " IN (" + marks(n) + ")"
	if rm.soft {
		q += " AND " + d.Quote("deleted_at") + " IS NULL"
	}

	return q + " GROUP BY " + d.Quote(rel.fk)
}

// loadKeyed loads related rows whose column relCol matches the value of
// local in each item.
func loadKeyed(ctx context.Context, ex database.Executor, items []reflect.Value, rel relation, rm *meta,
	local field, relCol string, spec *withSpec,
) error {
	if local.index == nil {
		return fmt.Errorf("missing local column %q", rel.fk)
	}

	keys := make([]any, 0, len(items))
	for i := range items {
		if k, ok := keyOf(items[i].FieldByIndex(local.index)); ok {
			keys = append(keys, k)
		}
	}

	byKey, _, err := selectIn(ctx, ex, rm, relCol, uniq(keys), spec)
	if err != nil {
		return err
	}

	for i := range items {
		item := items[i]

		var vals []reflect.Value
		if k, ok := keyOf(item.FieldByIndex(local.index)); ok {
			vals = byKey[k]
		}

		setRelation(item, rel, vals)
	}

	return nil
}

func loadPivot(
	ctx context.Context, ex database.Executor, m *meta, items []reflect.Value, rel relation, rm *meta, spec *withSpec,
) error {
	local := m.byColumn[m.pk]

	keys := make([]any, 0, len(items))
	for i := range items {
		if k, ok := keyOf(items[i].FieldByIndex(local.index)); ok {
			keys = append(keys, k)
		}
	}

	keys = uniq(keys)
	links := map[any][]any{}

	var relKeys []any

	d := ex.Dialect()

	for chunk := range slices.Chunk(keys, eagerChunk) {
		q := rebind("SELECT "+d.Quote(rel.fk)+", "+d.Quote(rel.rk)+" FROM "+d.Quote(rel.pivot)+
			" WHERE "+d.Quote(rel.fk)+" IN ("+marks(len(chunk))+")", d)

		err := eachRow(ctx, ex, q, chunk, func(rows *sql.Rows) error {
			var a, b any
			if err := rows.Scan(&a, &b); err != nil {
				return err
			}

			ka, kb := normKey(a), normKey(b)
			links[ka] = append(links[ka], kb)
			relKeys = append(relKeys, kb)

			return nil
		})
		if err != nil {
			return err
		}
	}

	byKey, order, err := selectIn(ctx, ex, rm, rm.pk, uniq(relKeys), spec)
	if err != nil {
		return err
	}

	rank := make(map[any]int, len(order))
	for i, k := range order {
		rank[k] = i
	}

	for i := range items {
		item := items[i]

		var vals []reflect.Value

		if k, ok := keyOf(item.FieldByIndex(local.index)); ok {
			// The related rows in the order the query returned them (a
			// WithWhere OrderBy), not in the order of the pivot rows.
			rks := slices.Clone(links[k])
			slices.SortStableFunc(rks, func(a, b any) int { return rank[a] - rank[b] })

			for _, rk := range rks {
				vals = append(vals, byKey[rk]...)
			}
		}

		setRelation(item, rel, vals)
	}

	return nil
}

// selectIn returns rows of rm whose column col is in keys, grouped by that
// column's value, in primary key order. Soft-deleted rows are excluded.
func selectIn(
	ctx context.Context,
	ex database.Executor,
	rm *meta,
	col string,
	keys []any,
	spec *withSpec,
) (map[any][]reflect.Value, []any, error) {
	out := map[any][]reflect.Value{}

	var order []any // the keys of col in the order the rows came back

	if len(keys) == 0 {
		return out, order, nil
	}

	f, ok := rm.byColumn[col]
	if !ok {
		return nil, nil, fmt.Errorf("%s has no column %q", rm.table, col)
	}

	d := ex.Dialect()

	cols := make([]string, len(rm.fields))
	for i, fd := range rm.fields {
		cols[i] = d.Quote(fd.column)
	}

	for chunk := range slices.Chunk(keys, eagerChunk) {
		q := "SELECT " + strings.Join(cols, ", ") + " FROM " + d.Quote(rm.table) + " WHERE " + d.Quote(col) +
			" IN (" + marks(len(chunk)) + ")"

		tail, extra := spec.sql(d, rm)
		q += tail
		args := append(slices.Clone(chunk), extra...)

		var scan func(*sql.Rows, reflect.Value) error

		err := eachRow(ctx, ex, rebind(q, d), args, func(rows *sql.Rows) error {
			if scan == nil {
				var err error
				if scan, err = rm.scanner(rows, ex); err != nil {
					return err
				}
			}

			v := reflect.New(rm.typ).Elem()
			if err := scan(rows, v); err != nil {
				return err
			}

			if k, ok := keyOf(v.FieldByIndex(f.index)); ok {
				if len(out[k]) == 0 {
					order = append(order, k)
				}

				out[k] = append(out[k], v)
			}

			return nil
		})
		if err != nil {
			return nil, nil, err
		}
	}

	return out, order, nil
}

func setRelation(item reflect.Value, rel relation, vals []reflect.Value) {
	s, _ := reflect.TypeAssert[relationSetter](item.FieldByIndex(rel.index).Addr())
	s.setLoaded(vals)
}

// keyOf normalizes a key field (ints, strings, pointers to them) so values
// scanned from different column types compare equal in maps.
func keyOf(v reflect.Value) (any, bool) {
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil, false
		}

		v = v.Elem()
	}

	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int(), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return int64(v.Uint()), true //nolint:gosec // database keys fit in int64
	case reflect.String:
		return v.String(), v.String() != ""
	default:
		// Types with their own column form (uuid.UUID) compare by it, as the
		// keys scanned from a pivot table do.
		if val, ok := reflect.TypeAssert[driver.Valuer](v); ok {
			if dv, err := val.Value(); err == nil {
				if b, isBytes := dv.([]byte); isBytes {
					dv = string(b)
				}

				return dv, !v.IsZero()
			}
		}

		return v.Interface(), !v.IsZero()
	}
}

// normKey normalizes values scanned into any (int64, []byte, string).
func normKey(v any) any {
	if b, ok := v.([]byte); ok {
		return string(b)
	}

	if rv := reflect.ValueOf(v); rv.IsValid() {
		if k, ok := keyOf(rv); ok {
			return k
		}
	}

	return v
}

func uniq(keys []any) []any {
	seen := map[any]bool{}
	out := keys[:0]

	for _, k := range keys {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}

	return out
}

// eagerChunk is how many keys one eager-loading query binds: below every
// known parameter limit.
const eagerChunk = 500

func marks(n int) string { return strings.TrimSuffix(strings.Repeat("?, ", n), ", ") }

// eachRow runs q and calls fn for every row, closing the rows.
func eachRow(ctx context.Context, ex database.Executor, q string, args []any, fn func(*sql.Rows) error) error {
	rows, err := ex.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}

	return rows.Err()
}
