package schema

import (
	"fmt"
	"strings"
)

type kind int

const (
	kindID kind = iota
	kindString
	kindText
	kindInteger
	kindBigInteger
	kindBoolean
	kindDecimal
	kindFloat
	kindDate
	kindTimestamp
	kindJSON
	kindUUID
	kindULID
	kindEnum
	kindBinary
)

// Column is a column definition; modifiers chain.
type Column struct {
	t          *Table
	name       string
	kind       kind
	length     int
	precision  int
	scale      int
	nullable   bool
	hasDefault bool
	def        any
	unique     bool
	enum       []string
	ref        *reference
	refInline  bool
}

type reference struct {
	column   string
	table    string
	refCol   string
	onDelete string
}

type index struct {
	name   string
	cols   []string
	unique bool
}

// Table collects column and index definitions for Create or Table.
type Table struct {
	s       *Schema
	name    string
	alter   bool
	cols    []*Column
	indexes []index
	fks     []*reference
	renames [][2]string
	drops   []string
	dropIdx []string
	err     error
}

func (t *Table) fail(err error) {
	if t.err == nil {
		t.err = err
	}
}

func (t *Table) add(name string, k kind) *Column {
	if err := checkIdent("column", name); err != nil {
		t.fail(err)
	}

	c := &Column{t: t, name: name, kind: k}
	t.cols = append(t.cols, c)

	return c
}

// ID adds an auto-increment big integer primary key "id".
func (t *Table) ID() *Column { return t.add("id", kindID) }

// String adds a VARCHAR column (default length 255).
func (t *Table) String(name string, length ...int) *Column {
	c := t.add(name, kindString)

	c.length = 255
	if len(length) > 0 && length[0] > 0 {
		c.length = length[0]
	}

	return c
}

// Text adds an unbounded text column.
func (t *Table) Text(name string) *Column { return t.add(name, kindText) }

// Integer adds an integer column.
func (t *Table) Integer(name string) *Column { return t.add(name, kindInteger) }

// BigInteger adds a 64-bit integer column.
func (t *Table) BigInteger(name string) *Column { return t.add(name, kindBigInteger) }

// Boolean adds a boolean column.
func (t *Table) Boolean(name string) *Column { return t.add(name, kindBoolean) }

// Decimal adds a NUMERIC(precision, scale) column.
func (t *Table) Decimal(name string, precision, scale int) *Column {
	c := t.add(name, kindDecimal)
	c.precision, c.scale = precision, scale

	return c
}

// Float adds a floating-point column.
func (t *Table) Float(name string) *Column { return t.add(name, kindFloat) }

// Date adds a date column.
func (t *Table) Date(name string) *Column { return t.add(name, kindDate) }

// Timestamp adds a timestamp column (TIMESTAMPTZ on PostgreSQL).
func (t *Table) Timestamp(name string) *Column { return t.add(name, kindTimestamp) }

// Timestamps adds nullable created_at and updated_at.
func (t *Table) Timestamps() {
	t.Timestamp("created_at").Nullable()
	t.Timestamp("updated_at").Nullable()
}

// SoftDeletes adds a nullable deleted_at timestamp.
func (t *Table) SoftDeletes() *Column { return t.Timestamp("deleted_at").Nullable() }

// JSON adds a JSON column (JSONB on PostgreSQL, TEXT on SQLite).
func (t *Table) JSON(name string) *Column { return t.add(name, kindJSON) }

// UUID adds a UUID column.
func (t *Table) UUID(name string) *Column { return t.add(name, kindUUID) }

// ULID adds a CHAR(26) column.
func (t *Table) ULID(name string) *Column { return t.add(name, kindULID) }

// Binary adds a binary column.
func (t *Table) Binary(name string) *Column { return t.add(name, kindBinary) }

// Enum adds a text column restricted to values with a CHECK constraint.
func (t *Table) Enum(name string, values ...string) *Column {
	c := t.add(name, kindEnum)
	if len(values) == 0 {
		t.fail(fmt.Errorf("schema: enum %s needs values", name))
	}

	c.enum = values

	return c
}

// ForeignID adds a big integer column meant to reference another table;
// chain Constrained() to add the foreign key.
func (t *Table) ForeignID(name string) *Column { return t.add(name, kindBigInteger) }

// Index adds an index over cols (name derived: table_col1_col2_index).
func (t *Table) Index(cols ...string) { t.addIndex(cols, false) }

// UniqueIndex adds a unique index over cols.
func (t *Table) UniqueIndex(cols ...string) { t.addIndex(cols, true) }

func (t *Table) addIndex(cols []string, unique bool) {
	for _, c := range cols {
		if err := checkIdent("column", c); err != nil {
			t.fail(err)
			return
		}
	}

	suffix := "_index"
	if unique {
		suffix = "_unique"
	}

	t.indexes = append(
		t.indexes,
		index{name: t.name + "_" + strings.Join(cols, "_") + suffix, cols: cols, unique: unique},
	)
}

// RenameColumn renames a column (alter only).
func (t *Table) RenameColumn(from, to string) {
	if err := checkIdent("column", from); err != nil {
		t.fail(err)
	}

	if err := checkIdent("column", to); err != nil {
		t.fail(err)
	}

	t.renames = append(t.renames, [2]string{from, to})
}

// DropColumn drops columns (alter only).
func (t *Table) DropColumn(names ...string) {
	for _, n := range names {
		if err := checkIdent("column", n); err != nil {
			t.fail(err)
		}
	}

	t.drops = append(t.drops, names...)
}

// DropIndex drops an index by name (alter only).
func (t *Table) DropIndex(name string) {
	if err := checkIdent("index", name); err != nil {
		t.fail(err)
	}

	t.dropIdx = append(t.dropIdx, name)
}

// Nullable allows NULL.
func (c *Column) Nullable() *Column { c.nullable = true; return c }

// Default sets the default (Go value or schema.Expr).
func (c *Column) Default(v any) *Column { c.hasDefault, c.def = true, v; return c }

// Unique adds a UNIQUE constraint.
func (c *Column) Unique() *Column { c.unique = true; return c }

// Index adds a single-column index.
func (c *Column) Index() *Column { c.t.Index(c.name); return c }

// Constrained adds a foreign key to table(id); the table defaults to the
// column name without "_id" pluralised ("user_id" → "users").
func (c *Column) Constrained(table ...string) *Column {
	target := strings.TrimSuffix(c.name, "_id") + "s"
	if len(table) > 0 {
		target = table[0]
	}

	if err := checkIdent("table", target); err != nil {
		c.t.fail(err)
	}

	c.ref = &reference{column: c.name, table: target, refCol: "id"}

	if c.t.alter {
		c.refInline = true // ADD COLUMN can only carry an inline REFERENCES
	} else {
		c.t.fks = append(c.t.fks, c.ref)
	}

	return c
}

// CascadeOnDelete deletes children when the parent row is deleted.
func (c *Column) CascadeOnDelete() *Column { return c.onDelete("CASCADE") }

// NullOnDelete sets the column to NULL when the parent row is deleted.
func (c *Column) NullOnDelete() *Column { c.nullable = true; return c.onDelete("SET NULL") }

// RestrictOnDelete prevents deleting a referenced parent.
func (c *Column) RestrictOnDelete() *Column { return c.onDelete("RESTRICT") }

func (c *Column) onDelete(action string) *Column {
	if c.ref == nil {
		c.t.fail(fmt.Errorf("schema: %s: call Constrained() before an ON DELETE action", c.name))
		return c
	}

	c.ref.onDelete = action

	return c
}
