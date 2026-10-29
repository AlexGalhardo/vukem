// Package orm is a generic query builder and model mapper over
// database/sql. Queries are typed (Query[T] returns T values), always use
// bound parameters, and validate column names against the model so input
// can never become SQL (A05). Models are plain structs; persistence
// functions take an Executor (*database.DB or *database.Tx), so there is no
// Active Record handle hidden in models.
//
// Embed [Model] for an ID and timestamps, build reads with [QueryOf], and
// write with [Create], [Update] and [Delete]. Relations, scopes, soft
// deletes and encrypted columns ([Encrypted]) are declared on the struct.
// Tables come from package schema migrations.
//
// An invalid model (not a struct, a bad table or column name, no primary
// key, a relation without its foreign key, an unknown orm tag option)
// panics on its first use, usually at boot: it is a programming error, not
// a runtime condition.
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/database.md.
package orm

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/AlexGalhardo/vukem/internal/naming"
)

// Model provides an auto-increment id and timestamps; embed it in models.
type Model struct {
	ID        int64     `db:"id"         json:"id"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
	UpdatedAt time.Time `db:"updated_at" json:"updated_at"`
}

// SoftDeletes makes Delete set deleted_at instead of removing rows; queries
// exclude deleted rows unless WithTrashed or OnlyTrashed is used.
type SoftDeletes struct {
	DeletedAt NullTime `db:"deleted_at" json:"deleted_at,omitzero"`
}

// NullTime is a nullable timestamp that encodes as JSON null or an RFC
// 3339 time (sql.Null would encode as {"V":...,"Valid":...}).
type NullTime struct {
	sql.Null[time.Time]
}

// MarshalJSON implements json.Marshaler.
func (n NullTime) MarshalJSON() ([]byte, error) {
	if !n.Valid {
		return []byte("null"), nil
	}

	return json.Marshal(n.V)
}

// UnmarshalJSON implements json.Unmarshaler.
func (n *NullTime) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*n = NullTime{}
		return nil
	}

	n.Valid = true

	return json.Unmarshal(b, &n.V)
}

// IsZero reports a NULL value, so `json:",omitzero"` omits it.
func (n NullTime) IsZero() bool { return !n.Valid }

// Tabler overrides the table name (default: snake_case plural of the type).
type Tabler interface{ Table() string }

// Keyer overrides the primary key column (default "id").
type Keyer interface{ PrimaryKey() string }

// CompositeKeyer declares a primary key of several columns, e.g. a ratings
// table keyed by (user_id, item_id). Update, Delete, ForceDelete and
// Restore then filter on every key column; APIs that address a row by one
// value (Query.Find, Query.Chunk, relations, Attach, Reseal, Unique.Ignore)
// return an error. It takes precedence over Keyer.
type CompositeKeyer interface{ PrimaryKeys() []string }

type field struct {
	column string
	index  []int
	typ    reflect.Type
	// readonly columns are read but never written by Create, CreateMany
	// or Update (orm:"readonly"): generated or trigger-maintained columns.
	readonly bool
}

type meta struct {
	typ        reflect.Type
	table      string
	pk         string
	keys       []string // every primary key column; keys[0] == pk
	fields     []field
	byColumn   map[string]field
	autoID     bool // integer primary key assigned by the database
	hasCreated bool
	hasUpdated bool
	soft       bool
	relations  map[string]relation
}

var metaCache sync.Map //nolint:gochecknoglobals // memoization of immutable per-type metadata

var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// metaOf returns cached metadata for T, panicking on invalid models (a
// programming error caught at first use, usually at boot).
func metaOf[T any]() *meta { return metaOfType(reflect.TypeFor[T]()) }

func metaOfType(rt reflect.Type) *meta {
	if v, ok := metaCache.Load(rt); ok {
		m, _ := v.(*meta)
		return m
	}

	m := buildMeta(rt)
	metaCache.Store(rt, m)

	return m
}

func buildMeta(rt reflect.Type) *meta {
	if rt.Kind() != reflect.Struct {
		panic(fmt.Sprintf("orm: model must be a struct, got %s", rt))
	}

	keys := keysOf(rt)
	m := &meta{
		typ:       rt,
		table:     tableName(rt),
		pk:        keys[0],
		keys:      keys,
		byColumn:  map[string]field{},
		relations: map[string]relation{},
	}

	for _, f := range reflect.VisibleFields(rt) {
		if !f.IsExported() || f.Anonymous && f.Type.Kind() == reflect.Struct {
			continue
		}

		if isRelation(f.Type) {
			m.relations[f.Name] = buildRelation(rt, f)
			continue
		}

		col := f.Tag.Get("db")
		if col == "-" {
			continue
		}

		if col == "" {
			col = naming.Snake(f.Name)
		}

		if !identRe.MatchString(col) {
			panic(fmt.Sprintf("orm: %s.%s has invalid column name %q", rt, f.Name, col))
		}

		if _, dup := m.byColumn[col]; dup {
			continue // embedded duplicates: the shallowest field wins (VisibleFields order)
		}

		fd := field{column: col, index: f.Index, typ: f.Type, readonly: columnReadonly(rt, f)}
		m.fields = append(m.fields, fd)
		m.byColumn[col] = fd

		switch col {
		case "created_at":
			m.hasCreated = f.Type == reflect.TypeFor[time.Time]()
		case "updated_at":
			m.hasUpdated = f.Type == reflect.TypeFor[time.Time]()
		case "deleted_at":
			m.soft = true
		}
	}

	for _, r := range m.relations {
		if _, ok := m.byColumn[r.fk]; r.kind == relBelongsTo && !ok {
			panic(fmt.Sprintf("orm: %s.%s needs a %q column (or an orm:\"fk:...\" tag)", rt, r.name, r.fk))
		}
	}

	pkField, ok := m.byColumn[m.pk]
	if !ok {
		panic(fmt.Sprintf("orm: %s has no primary key column %q", rt, m.pk))
	}

	for _, k := range m.keys[1:] {
		if _, ok := m.byColumn[k]; !ok {
			panic(fmt.Sprintf("orm: %s has no primary key column %q", rt, k))
		}
	}

	switch pkField.typ.Kind() {
	case reflect.Int, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint32, reflect.Uint64:
		m.autoID = len(m.keys) == 1 // the database never generates a composite key
	default:
	}

	if !identRe.MatchString(m.table) {
		panic(fmt.Sprintf("orm: %s has invalid table name %q", rt, m.table))
	}

	return m
}

// columnReadonly parses the orm tag of a column field; "readonly" is its
// only option.
func columnReadonly(rt reflect.Type, f reflect.StructField) bool {
	readonly := false

	for opt := range strings.SplitSeq(f.Tag.Get("orm"), ",") {
		switch strings.TrimSpace(opt) {
		case "readonly":
			readonly = true
		case "":
		default:
			panic(fmt.Sprintf("orm: %s.%s has unknown orm tag option %q", rt, f.Name, opt))
		}
	}

	return readonly
}

// keysOf returns the primary key columns of rt: CompositeKeyer, then
// Keyer, then "id".
func keysOf(rt reflect.Type) []string {
	zero := reflect.New(rt).Interface()

	if k, ok := zero.(CompositeKeyer); ok {
		keys := slices.Clone(k.PrimaryKeys())
		if len(keys) == 0 {
			panic(fmt.Sprintf("orm: %s.PrimaryKeys returned no columns", rt))
		}

		return keys
	}

	if k, ok := zero.(Keyer); ok {
		return []string{k.PrimaryKey()}
	}

	return []string{"id"}
}

func tableName(rt reflect.Type) string {
	if t, ok := reflect.TypeAssert[Tabler](reflect.New(rt)); ok {
		return t.Table()
	}

	return naming.Plural(naming.Snake(rt.Name()))
}

// column validates name against the model: plain columns, "table.column"
// for this table, or "*". It never lets input reach SQL unvalidated.
func (m *meta) column(name string) (string, error) {
	if name == "*" {
		return name, nil
	}

	table, col, qualified := strings.Cut(name, ".")
	if !qualified {
		col = table
	} else if table != m.table {
		return "", fmt.Errorf("orm: column %q does not belong to %s", name, m.table)
	}

	if _, ok := m.byColumn[col]; !ok {
		return "", fmt.Errorf("orm: unknown column %q on %s", name, m.table)
	}

	return name, nil
}

// TableOf returns the table name of model T.
func TableOf[T any]() string { return metaOf[T]().table }

// FieldInfo describes a mapped column of a model, for tools that work with
// any model (the admin panel, exporters).
type FieldInfo struct {
	// Column is the column name.
	Column string
	// Name is the Go field name.
	Name string
	// Index locates the field for reflect.Value.FieldByIndex.
	Index []int
	// Type is the Go field type.
	Type reflect.Type
	// Tag is the struct field tag.
	Tag reflect.StructTag
}

// FieldsOf returns the mapped columns of model T in field order.
func FieldsOf[T any]() []FieldInfo {
	m := metaOf[T]()

	out := make([]FieldInfo, len(m.fields))
	for i, f := range m.fields {
		sf := m.typ.FieldByIndex(f.index)
		out[i] = FieldInfo{Column: f.column, Name: sf.Name, Index: slices.Clone(f.index), Type: f.typ, Tag: sf.Tag}
	}

	return out
}

// RelationsOf returns the relation field names of model T (the names
// Query.With takes), sorted.
func RelationsOf[T any]() []string {
	return slices.Sorted(maps.Keys(metaOf[T]().relations))
}

// PrimaryKeyOf returns the primary key column of model T (the first one
// of a composite key, see PrimaryKeysOf).
func PrimaryKeyOf[T any]() string { return metaOf[T]().pk }

// PrimaryKeysOf returns every primary key column of model T: one for a
// Keyer or the default "id", several for a CompositeKeyer.
func PrimaryKeysOf[T any]() []string { return slices.Clone(metaOf[T]().keys) }

// composite reports a composite primary key.
func (m *meta) composite() bool { return len(m.keys) > 1 }

// singleKey fails op on a model with a composite primary key.
func (m *meta) singleKey(op string) error {
	if !m.composite() {
		return nil
	}

	return fmt.Errorf("orm: %s has a composite primary key (%s); %s needs a single-column key",
		m.table, strings.Join(m.keys, ", "), op)
}

// Columns returns the column names of model T in field order.
func Columns[T any]() []string {
	m := metaOf[T]()

	cols := make([]string, len(m.fields))
	for i, f := range m.fields {
		cols[i] = f.column
	}

	return cols
}
