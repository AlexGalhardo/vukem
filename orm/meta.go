// Package orm is a generic query builder and model mapper over
// database/sql. Queries are typed (Query[T] returns T values), always use
// bound parameters, and validate column names against the model so input
// can never become SQL (A05). Models are plain structs; persistence
// functions take an Executor (*database.DB or *database.Tx), so there is no
// Active Record handle hidden in models.
package orm

import (
	"database/sql"
	"fmt"
	"reflect"
	"regexp"
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
	DeletedAt sql.Null[time.Time] `db:"deleted_at" json:"deleted_at"`
}

// Tabler overrides the table name (default: snake_case plural of the type).
type Tabler interface{ Table() string }

// Keyer overrides the primary key column (default "id").
type Keyer interface{ PrimaryKey() string }

type field struct {
	column string
	index  []int
	typ    reflect.Type
}

type meta struct {
	typ        reflect.Type
	table      string
	pk         string
	fields     []field
	byColumn   map[string]field
	autoID     bool // integer primary key assigned by the database
	hasCreated bool
	hasUpdated bool
	soft       bool
}

var metaCache sync.Map //nolint:gochecknoglobals // memoization of immutable per-type metadata

var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// metaOf returns cached metadata for T, panicking on invalid models (a
// programming error caught at first use, usually at boot).
func metaOf[T any]() *meta {
	rt := reflect.TypeFor[T]()
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

	m := &meta{typ: rt, table: tableName(rt), pk: "id", byColumn: map[string]field{}}

	zero := reflect.New(rt).Interface()
	if k, ok := zero.(Keyer); ok {
		m.pk = k.PrimaryKey()
	}

	for _, f := range reflect.VisibleFields(rt) {
		if !f.IsExported() || f.Anonymous && f.Type.Kind() == reflect.Struct {
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

		fd := field{column: col, index: f.Index, typ: f.Type}
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

	pkField, ok := m.byColumn[m.pk]
	if !ok {
		panic(fmt.Sprintf("orm: %s has no primary key column %q", rt, m.pk))
	}

	switch pkField.typ.Kind() {
	case reflect.Int, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint32, reflect.Uint64:
		m.autoID = true
	default:
	}

	if !identRe.MatchString(m.table) {
		panic(fmt.Sprintf("orm: %s has invalid table name %q", rt, m.table))
	}

	return m
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

// Columns returns the column names of model T in field order.
func Columns[T any]() []string {
	m := metaOf[T]()

	cols := make([]string, len(m.fields))
	for i, f := range m.fields {
		cols[i] = f.column
	}

	return cols
}
