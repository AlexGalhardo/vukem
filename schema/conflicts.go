package schema

import (
	"context"
	"fmt"

	"github.com/AlexGalhardo/vukem/database"
)

// Conflict is a problem found by FindConflicts.
type Conflict struct {
	ID      string `json:"id"` // the migration that hits the problem
	Table   string `json:"table"`
	Column  string `json:"column,omitempty"` // "" for table-level problems
	Message string `json:"message"`
}

// FindConflicts replays migrations in order on a model of the schema, without
// a database, and reports operations that cannot apply: a table created
// twice, a column added twice, a column dropped, renamed or changed that
// does not exist, a table altered or dropped before it exists, an index name
// used twice. Statements from Schema.Raw are not analysed. Two branches that
// each add the same column show up as a conflict on the second migration.
func FindConflicts(d database.Dialect, migrations []Migration) ([]Conflict, error) {
	m := &schemaModel{tables: map[string]*modelTable{}}
	s := &Schema{ctx: context.Background(), d: d, model: m}

	for _, mig := range migrations {
		m.cur = mig.ID

		if err := mig.Up(s); err != nil {
			return nil, fmt.Errorf("schema: conflicts: migration %s: %w", mig.ID, err)
		}
	}

	return m.conflicts, nil
}

// schemaModel is the schema as the migrations replayed so far leave it.
type schemaModel struct {
	tables    map[string]*modelTable
	cur       string
	conflicts []Conflict
}

type modelTable struct {
	created string
	cols    map[string]string // column -> migration that added it
	idx     map[string]string // index name -> migration that created it
}

func (m *schemaModel) report(table, column, format string, args ...any) {
	m.conflicts = append(
		m.conflicts,
		Conflict{ID: m.cur, Table: table, Column: column, Message: fmt.Sprintf(format, args...)},
	)
}

func (m *schemaModel) define(name string, t *Table) {
	mt := &modelTable{created: m.cur, cols: map[string]string{}, idx: map[string]string{}}

	for _, c := range t.cols {
		mt.cols[c.name] = m.cur
	}

	for _, ix := range t.indexes {
		if prev, dup := mt.idx[ix.name]; dup {
			m.report(name, "", "index %s already created by %s", ix.name, prev)
		}

		mt.idx[ix.name] = m.cur
	}

	m.tables[name] = mt
}

func (m *schemaModel) create(name string, t *Table) {
	if prev, ok := m.tables[name]; ok {
		m.report(name, "", "table already created by %s", prev.created)
		return
	}

	m.define(name, t)
}

func (m *schemaModel) missing(name string) bool {
	if _, ok := m.tables[name]; ok {
		return false
	}

	m.report(name, "", "table does not exist at this point")

	return true
}

func (m *schemaModel) alter(name string, t *Table) {
	if m.missing(name) {
		return
	}

	mt := m.tables[name]

	for _, c := range t.cols {
		prev, exists := mt.cols[c.name]

		switch {
		case c.change && !exists:
			m.report(name, c.name, "column does not exist, so it cannot be changed")
		case !c.change && exists:
			m.report(name, c.name, "column already added by %s", prev)
		case !c.change:
			mt.cols[c.name] = m.cur
		}
	}

	for _, r := range t.renames {
		switch {
		case !has(mt.cols, r[0]):
			m.report(name, r[0], "column does not exist, so it cannot be renamed to %s", r[1])
		case has(mt.cols, r[1]):
			m.report(name, r[1], "column already added by %s", mt.cols[r[1]])
		default:
			mt.cols[r[1]] = mt.cols[r[0]]
			delete(mt.cols, r[0])
		}
	}

	for _, n := range t.drops {
		if !has(mt.cols, n) {
			m.report(name, n, "column does not exist, so it cannot be dropped")
			continue
		}

		delete(mt.cols, n)
	}

	for _, n := range t.dropIdx {
		if !has(mt.idx, n) {
			m.report(name, "", "index %s does not exist, so it cannot be dropped", n)
			continue
		}

		delete(mt.idx, n)
	}

	for _, ix := range t.indexes {
		if prev, dup := mt.idx[ix.name]; dup {
			m.report(name, "", "index %s already created by %s", ix.name, prev)
			continue
		}

		mt.idx[ix.name] = m.cur
	}
}

func (m *schemaModel) drop(name string, ifExists bool) {
	if _, ok := m.tables[name]; !ok {
		if !ifExists {
			m.report(name, "", "table does not exist, so it cannot be dropped")
		}

		return
	}

	delete(m.tables, name)
}

func (m *schemaModel) rename(from, to string) {
	if m.missing(from) {
		return
	}

	if prev, ok := m.tables[to]; ok {
		m.report(to, "", "table already created by %s", prev.created)
		return
	}

	m.tables[to] = m.tables[from]
	delete(m.tables, from)
}

func (m *schemaModel) rebuild(name string, t *Table) {
	if m.missing(name) {
		return
	}

	m.define(name, t)
}

func has(m map[string]string, k string) bool {
	_, ok := m[k]
	return ok
}
