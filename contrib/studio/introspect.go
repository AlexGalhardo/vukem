package studio

import "context"

// Table is what the SQL browser knows about a table. The types in this file
// are Studio's own: the only code that reads the database catalog fills them
// (the implementation of Introspector, which converts schema.Inspect once
// plan 074 lands), so a change to schema.TableInfo or schema.IndexInfo touches
// that one converter and nothing else. Indexes are not mapped on purpose: the
// grid and the console need no more than names, types and keys.
type Table struct {
	Name        string
	Columns     []Column
	PrimaryKey  []string
	ForeignKeys []ForeignKey
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

// Introspector lists the tables the SQL browser may touch. Every table, column
// and sort name the grid builds SQL from must come from it, never from the
// request.
type Introspector interface {
	// Tables returns every base table, sorted by name.
	Tables(ctx context.Context) ([]Table, error)
	// Table returns one table; the second result is false when it does not exist.
	Table(ctx context.Context, name string) (Table, bool, error)
}
