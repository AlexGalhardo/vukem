package orm

import (
	"context"
	"database/sql"
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

// With eager loads relations (by field name) for Get, First, Find,
// Paginate and CursorPaginate: one query per relation, batched with IN, so
// N rows cost 1 + len(relations) queries. Each does not eager load.
func (q Query[T]) With(relations ...string) Query[T] {
	for _, name := range relations {
		if _, ok := q.m.relations[name]; !ok {
			return q.fail(fmt.Errorf("orm: %s has no relation %q", q.m.typ.Name(), name))
		}
	}

	q = q.clone()
	q.with = append(slices.Clip(q.with), relations...)

	return q
}

// loadRelations fills the requested relations of items (a []T) in place.
func loadRelations(ctx context.Context, ex database.Executor, m *meta, items reflect.Value, names []string) error {
	if items.Len() == 0 {
		return nil
	}

	for _, name := range names {
		rel := m.relations[name]
		rm := metaOfType(rel.related)

		err := m.singleKey("a relation")
		if rel.kind == relBelongsTo {
			err = rm.singleKey("a belongs-to relation")
		}

		if err != nil {
			return fmt.Errorf("orm: load %s.%s: %w", m.typ.Name(), name, err)
		}

		switch rel.kind {
		case relBelongsTo:
			err = loadKeyed(ctx, ex, items, rel, rm, m.byColumn[rel.fk], rm.pk)
		case relHasOne, relHasMany:
			err = loadKeyed(ctx, ex, items, rel, rm, m.byColumn[m.pk], rel.fk)
		case relManyToMany:
			err = loadPivot(ctx, ex, m, items, rel, rm)
		}

		if err != nil {
			return fmt.Errorf("orm: load %s.%s: %w", m.typ.Name(), name, err)
		}
	}

	return nil
}

// loadKeyed loads related rows whose column relCol matches the value of
// local in each item.
func loadKeyed(ctx context.Context, ex database.Executor, items reflect.Value, rel relation, rm *meta,
	local field, relCol string,
) error {
	if local.index == nil {
		return fmt.Errorf("missing local column %q", rel.fk)
	}

	keys := make([]any, 0, items.Len())
	for i := range items.Len() {
		if k, ok := keyOf(items.Index(i).FieldByIndex(local.index)); ok {
			keys = append(keys, k)
		}
	}

	byKey, err := selectIn(ctx, ex, rm, relCol, uniq(keys))
	if err != nil {
		return err
	}

	for i := range items.Len() {
		item := items.Index(i)

		var vals []reflect.Value
		if k, ok := keyOf(item.FieldByIndex(local.index)); ok {
			vals = byKey[k]
		}

		setRelation(item, rel, vals)
	}

	return nil
}

func loadPivot(ctx context.Context, ex database.Executor, m *meta, items reflect.Value, rel relation, rm *meta) error {
	local := m.byColumn[m.pk]

	keys := make([]any, 0, items.Len())
	for i := range items.Len() {
		if k, ok := keyOf(items.Index(i).FieldByIndex(local.index)); ok {
			keys = append(keys, k)
		}
	}

	keys = uniq(keys)
	links := map[any][]any{}

	var relKeys []any

	d := ex.Dialect()

	for chunk := range slices.Chunk(keys, 500) {
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

	byKey, err := selectIn(ctx, ex, rm, rm.pk, uniq(relKeys))
	if err != nil {
		return err
	}

	for i := range items.Len() {
		item := items.Index(i)

		var vals []reflect.Value

		if k, ok := keyOf(item.FieldByIndex(local.index)); ok {
			for _, rk := range links[k] {
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
) (map[any][]reflect.Value, error) {
	out := map[any][]reflect.Value{}
	if len(keys) == 0 {
		return out, nil
	}

	f, ok := rm.byColumn[col]
	if !ok {
		return nil, fmt.Errorf("%s has no column %q", rm.table, col)
	}

	d := ex.Dialect()

	cols := make([]string, len(rm.fields))
	for i, fd := range rm.fields {
		cols[i] = d.Quote(fd.column)
	}

	for chunk := range slices.Chunk(keys, 500) {
		q := "SELECT " + strings.Join(cols, ", ") + " FROM " + d.Quote(rm.table) + " WHERE " + d.Quote(col) +
			" IN (" + marks(len(chunk)) + ")"
		if rm.soft {
			q += " AND " + d.Quote("deleted_at") + " IS NULL"
		}

		q += " ORDER BY " + d.Quote(rm.pk)

		var scan func(*sql.Rows, reflect.Value) error

		err := eachRow(ctx, ex, rebind(q, d), chunk, func(rows *sql.Rows) error {
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
				out[k] = append(out[k], v)
			}

			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	return out, nil
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
