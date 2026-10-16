// Package schema builds DDL with a fluent, dialect-aware API (SQLite and
// PostgreSQL) and runs versioned migrations with batches and checksums.
//
//	s.Create("users", func(t *schema.Table) {
//		t.ID()
//		t.String("email").Unique()
//		t.Timestamps()
//	})
package schema

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/AlexGalhardo/vukem/database"
)

var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func checkIdent(kind, name string) error {
	if !identRe.MatchString(name) {
		return fmt.Errorf("schema: invalid %s name %q", kind, name)
	}

	return nil
}

// Schema runs DDL on an executor. In recording mode (used for checksums)
// statements are collected instead of executed.
type Schema struct {
	ctx    context.Context
	ex     database.Executor
	d      database.Dialect
	record *[]string
}

// New returns a Schema bound to ctx and ex.
func New(ctx context.Context, ex database.Executor) *Schema {
	return &Schema{ctx: ctx, ex: ex, d: ex.Dialect()}
}

func recorder(d database.Dialect) (*Schema, *[]string) {
	var stmts []string
	return &Schema{ctx: context.Background(), d: d, record: &stmts}, &stmts
}

// Dialect returns the dialect name ("sqlite", "postgres").
func (s *Schema) Dialect() string { return s.d.Name() }

func (s *Schema) exec(stmt string, args ...any) error {
	if s.record != nil {
		*s.record = append(*s.record, stmt)
		return nil
	}

	if _, err := s.ex.ExecContext(s.ctx, stmt, args...); err != nil {
		return fmt.Errorf("schema: %w\n%s", err, stmt)
	}

	return nil
}

func (s *Schema) q(ident string) string { return s.d.Quote(ident) }

// Create creates table with the columns and indexes defined by fn.
func (s *Schema) Create(table string, fn func(t *Table)) error {
	if err := checkIdent("table", table); err != nil {
		return err
	}

	t := &Table{name: table, s: s}
	fn(t)

	if t.err != nil {
		return t.err
	}

	defs := make([]string, 0, len(t.cols)+len(t.fks))
	for _, c := range t.cols {
		defs = append(defs, s.columnDef(c))
	}

	for _, fk := range t.fks {
		defs = append(defs, s.foreignKeyDef(fk))
	}

	if err := s.exec("CREATE TABLE " + s.q(table) + " (\n  " + strings.Join(defs, ",\n  ") + "\n)"); err != nil {
		return err
	}

	return s.createIndexes(t)
}

// Table alters table: added columns, renames, drops and indexes.
func (s *Schema) Table(table string, fn func(t *Table)) error {
	if err := checkIdent("table", table); err != nil {
		return err
	}

	t := &Table{name: table, s: s, alter: true}
	fn(t)

	if t.err != nil {
		return t.err
	}

	for _, c := range t.cols {
		if c.kind == kindID {
			return fmt.Errorf("schema: cannot add a primary key column to existing table %s", table)
		}

		if err := s.exec("ALTER TABLE " + s.q(table) + " ADD COLUMN " + s.columnDef(c)); err != nil {
			return err
		}
	}

	for _, r := range t.renames {
		if err := s.exec("ALTER TABLE " + s.q(table) + " RENAME COLUMN " + s.q(r[0]) + " TO " + s.q(r[1])); err != nil {
			return err
		}
	}

	for _, name := range t.dropIdx {
		if err := s.exec("DROP INDEX " + s.q(name)); err != nil {
			return err
		}
	}

	// shortcut: SQLite can only DROP COLUMN for columns without indexes,
	// UNIQUE or foreign keys; table rebuild support lands if needed (P.7.9).
	for _, col := range t.drops {
		if err := s.exec("ALTER TABLE " + s.q(table) + " DROP COLUMN " + s.q(col)); err != nil {
			return err
		}
	}

	return s.createIndexes(t)
}

func (s *Schema) createIndexes(t *Table) error {
	for _, ix := range t.indexes {
		kw := "CREATE INDEX "
		if ix.unique {
			kw = "CREATE UNIQUE INDEX "
		}

		cols := make([]string, len(ix.cols))
		for i, c := range ix.cols {
			cols[i] = s.q(c)
		}

		if err := s.exec(kw + s.q(ix.name) + " ON " + s.q(t.name) + " (" + strings.Join(cols, ", ") + ")"); err != nil {
			return err
		}
	}

	return nil
}

// Drop drops table.
func (s *Schema) Drop(table string) error {
	if err := checkIdent("table", table); err != nil {
		return err
	}

	return s.exec("DROP TABLE " + s.q(table) + s.cascade())
}

// DropIfExists drops table when it exists.
func (s *Schema) DropIfExists(table string) error {
	if err := checkIdent("table", table); err != nil {
		return err
	}

	return s.exec("DROP TABLE IF EXISTS " + s.q(table) + s.cascade())
}

func (s *Schema) cascade() string {
	if s.d.Name() == "postgres" {
		return " CASCADE"
	}

	return ""
}

// Rename renames a table.
func (s *Schema) Rename(from, to string) error {
	if err := checkIdent("table", from); err != nil {
		return err
	}

	if err := checkIdent("table", to); err != nil {
		return err
	}

	return s.exec("ALTER TABLE " + s.q(from) + " RENAME TO " + s.q(to))
}

// Raw runs any SQL statement (data migrations, views, triggers).
func (s *Schema) Raw(sql string, args ...any) error { return s.exec(sql, args...) }

// HasTable reports whether table exists (false while recording).
func (s *Schema) HasTable(table string) (bool, error) {
	if s.record != nil {
		return false, nil
	}

	return hasTable(s.ctx, s.ex, table)
}

func hasTable(ctx context.Context, ex database.Executor, table string) (bool, error) {
	var q string

	switch ex.Dialect().Name() {
	case "postgres":
		q = "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = $1"
	default:
		q = "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?"
	}

	var n int
	if err := ex.QueryRowContext(ctx, q, table).Scan(&n); err != nil {
		return false, fmt.Errorf("schema: has table: %w", err)
	}

	return n > 0, nil
}

// ------------------------------------------------------------------ DDL

func (s *Schema) columnDef(c *Column) string {
	var b strings.Builder

	b.WriteString(s.q(c.name) + " " + s.sqlType(c))

	if c.kind == kindID {
		return b.String()
	}

	if !c.nullable {
		b.WriteString(" NOT NULL")
	}

	if c.hasDefault {
		b.WriteString(" DEFAULT " + literal(c.def))
	}

	if c.unique {
		b.WriteString(" UNIQUE")
	}

	if c.kind == kindEnum {
		vals := make([]string, len(c.enum))
		for i, v := range c.enum {
			vals[i] = literal(v)
		}

		b.WriteString(" CHECK (" + s.q(c.name) + " IN (" + strings.Join(vals, ", ") + "))")
	}

	if c.ref != nil && c.refInline {
		b.WriteString(" " + s.referenceClause(c.ref))
	}

	return b.String()
}

func (s *Schema) foreignKeyDef(fk *reference) string {
	return "FOREIGN KEY (" + s.q(fk.column) + ") " + s.referenceClause(fk)
}

func (s *Schema) referenceClause(r *reference) string {
	out := "REFERENCES " + s.q(r.table) + " (" + s.q(r.refCol) + ")"
	if r.onDelete != "" {
		out += " ON DELETE " + r.onDelete
	}

	return out
}

func (s *Schema) sqlType(c *Column) string {
	pg := s.d.Name() == "postgres"

	switch c.kind {
	case kindID:
		if pg {
			return "BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY"
		}

		return "INTEGER PRIMARY KEY AUTOINCREMENT"
	case kindString:
		return "VARCHAR(" + strconv.Itoa(c.length) + ")"
	case kindText, kindEnum:
		return "TEXT"
	case kindInteger:
		return "INTEGER"
	case kindBigInteger:
		if pg {
			return "BIGINT"
		}

		return "INTEGER"
	case kindBoolean:
		return "BOOLEAN"
	case kindDecimal:
		return "NUMERIC(" + strconv.Itoa(c.precision) + ", " + strconv.Itoa(c.scale) + ")"
	case kindFloat:
		if pg {
			return "DOUBLE PRECISION"
		}

		return "REAL"
	case kindDate:
		return "DATE"
	case kindTimestamp:
		if pg {
			return "TIMESTAMPTZ"
		}

		return "TIMESTAMP"
	case kindJSON:
		if pg {
			return "JSONB"
		}

		return "TEXT"
	case kindUUID:
		if pg {
			return "UUID"
		}

		return "TEXT"
	case kindULID:
		return "CHAR(26)"
	case kindBinary:
		if pg {
			return "BYTEA"
		}

		return "BLOB"
	default:
		return "TEXT"
	}
}

// Expr is a raw SQL default such as schema.Expr("CURRENT_TIMESTAMP").
type Expr string

func literal(v any) string {
	switch x := v.(type) {
	case nil:
		return "NULL"
	case Expr:
		return string(x)
	case string:
		return "'" + strings.ReplaceAll(x, "'", "''") + "'"
	case bool:
		if x {
			return "TRUE"
		}

		return "FALSE"
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return fmt.Sprint(x)
	case float32, float64:
		return strconv.FormatFloat(toFloat(x), 'f', -1, 64)
	default:
		return "'" + strings.ReplaceAll(fmt.Sprint(x), "'", "''") + "'"
	}
}

func toFloat(v any) float64 {
	switch x := v.(type) {
	case float32:
		return float64(x)
	case float64:
		return x
	default:
		return 0
	}
}
