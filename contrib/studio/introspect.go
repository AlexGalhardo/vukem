package studio

import "context"

// Table is what the SQL browser knows about a table. The types in this file
// are Studio's own: the only code that reads the database catalog fills them
// (the implementation of Introspector, which converts schema.Inspect once
// plan 074 lands), so a change to schema.TableInfo or schema.IndexInfo touches
// that one converter and nothing else. Indexes are mapped for the schema view
// only (names, columns in order and the unique and primary flags); the grid
// and the console still need no more than names, types and keys.
type Table struct {
	Name        string
	Columns     []Column
	PrimaryKey  []string
	ForeignKeys []ForeignKey
	Indexes     []Index
	// RowEstimate is set by SQLSource.Tables only: the row count when it was
	// cheap to get, -1 when it was not. A catalog leaves it zero.
	RowEstimate int64
}

// Column is one column of a Table.
type Column struct {
	Name     string
	Type     string // as the database reports it, lower-cased
	Nullable bool
	Default  string // the default expression as text, "" when none
}

// ForeignKey links Columns of a table to RefColumns of RefTable.
type ForeignKey struct {
	Columns    []string
	RefTable   string
	RefColumns []string
}

// Index is one index of a Table. The primary key appears once, first, with
// Primary set (the catalog does not list its index; the converter adds it).
type Index struct {
	// Name is "" for the index a database creates itself for a UNIQUE column.
	Name    string
	Columns []string
	Unique  bool
	Primary bool
	// Expression is true when a part of the index is an expression, so Columns
	// lists only its plain columns.
	Expression bool
}

// Introspector lists the tables the SQL browser may touch. Every table, column
// and sort name the grid builds SQL from must come from it, never from the
// request.
type Introspector interface {
	// Tables returns every base table, sorted by name.
	Tables(ctx context.Context) ([]Table, error)
	// Table returns one table; the second result is false when it does not
	// exist, which includes a name the catalog cannot represent (the browser
	// answers 404 for it, never a server error).
	Table(ctx context.Context, name string) (Table, bool, error)
}
