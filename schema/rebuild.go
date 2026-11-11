package schema

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/AlexGalhardo/vukem/database"
)

// RebuildOption tunes Rebuild.
type RebuildOption func(*rebuildOptions)

type rebuildOptions struct {
	renames      map[string]string // new column -> old column
	dropTriggers bool
}

// DropTriggers lets Rebuild proceed on a table that has triggers: dropping
// the old table drops them, and Rebuild does not recreate them, so write them
// again after it. Without this option Rebuild refuses such a table.
func DropTriggers() RebuildOption {
	return func(o *rebuildOptions) { o.dropTriggers = true }
}

// RenamedFrom copies the old column from into the new column to.
func RenamedFrom(from, to string) RebuildOption {
	return func(o *rebuildOptions) {
		if o.renames == nil {
			o.renames = map[string]string{}
		}

		o.renames[to] = from
	}
}

const rebuildPrefix = "_vk_rebuild_"

// Rebuild replaces table with the definition built by fn, keeping the rows:
// the columns of the new definition that exist in the old table are copied
// by name (a renamed one through RenamedFrom). It is the way to do on SQLite
// what ALTER TABLE cannot: drop a column that has an index, a UNIQUE or a
// foreign key, add or drop a CHECK, change a type or nullability.
//
// fn describes the whole new table, as Create does: only what it declares
// survives, so indexes the old table had must be declared again. Views and
// triggers that mention the table are not recreated.
//
// SQLite only (on PostgreSQL and MySQL use Table and Column.Change). It
// follows the procedure of https://www.sqlite.org/lang_altertable.html: with
// foreign keys off, and in one transaction, it creates a table under a
// temporary name, copies the rows, drops the old table, renames the new one,
// recreates the indexes and runs PRAGMA foreign_key_check before it commits.
// Foreign keys have to be off because DROP TABLE would otherwise delete the
// rows first and fire ON DELETE CASCADE on the tables that reference it, and
// the pragma has no effect inside a transaction. That is why the migration
// must set Migration.NoTransaction: Rebuild owns the transaction. Back up
// the database before a migration that rebuilds a table in production.
func (s *Schema) Rebuild(table string, fn func(t *Table), opts ...RebuildOption) error {
	if err := checkIdent("table", table); err != nil {
		return err
	}

	if s.caps().Family != "sqlite" {
		return fmt.Errorf("schema: Rebuild is for sqlite; use Table and Column.Change on %s", s.d.Name())
	}

	var o rebuildOptions
	for _, opt := range opts {
		opt(&o)
	}

	t := &Table{name: table, s: s}
	fn(t)

	if t.err != nil {
		return t.err
	}

	if err := t.checkPrimary(); err != nil {
		return err
	}

	for to, from := range o.renames {
		for _, n := range []string{from, to} {
			if err := checkIdent("column", n); err != nil {
				return err
			}
		}
	}

	if s.model != nil {
		s.model.rebuild(table, t)
		return nil
	}

	if s.record != nil {
		cols := rebuildCopy(t, o.renames, nil)
		s.noteLostColumns(t, o.renames)
		*s.record = append(*s.record, s.rebuildStatements(t, cols, true)...)

		return nil
	}

	db, ok := s.ex.(*database.DB)
	if !ok {
		return errors.New(
			"schema: Rebuild needs Migration.NoTransaction on sqlite (it turns foreign keys off outside a transaction)",
		)
	}

	old, exists, err := InspectTable(s.ctx, db, table)
	if err != nil {
		return err
	}

	if !exists {
		return fmt.Errorf("schema: rebuild %s: no such table", table)
	}

	if !o.dropTriggers {
		if err := s.checkNoTriggers(db, table); err != nil {
			return err
		}
	}

	have := map[string]bool{}
	for _, c := range old.Columns {
		have[c.Name] = true
	}

	cols := rebuildCopy(t, o.renames, have)

	if err := s.checkNewColumns(db, t, o.renames, have, table); err != nil {
		return err
	}

	return s.rebuildRun(db, t, cols)
}

// noteLostColumns records, for Destructive, the columns the table has today
// that the new definition neither declares nor renames.
func (s *Schema) noteLostColumns(t *Table, renames map[string]string) {
	if s.liveColumns == nil {
		return
	}

	kept := map[string]bool{}

	for _, c := range t.cols {
		kept[c.name] = true
		if from, ok := renames[c.name]; ok {
			kept[from] = true
		}
	}

	for _, c := range s.liveColumns(t.name) {
		if !kept[c] {
			s.lost = append(s.lost, "rebuild "+t.name+" drops column "+c)
		}
	}
}

// checkNoTriggers refuses a table with triggers: the rebuild would drop them.
func (s *Schema) checkNoTriggers(db *database.DB, table string) error {
	rows, err := db.QueryContext(
		s.ctx,
		"SELECT name FROM sqlite_master WHERE type = 'trigger' AND tbl_name = ? ORDER BY name",
		table,
	)
	if err != nil {
		return fmt.Errorf("schema: rebuild %s: %w", table, err)
	}
	defer rows.Close()

	var names []string

	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return fmt.Errorf("schema: rebuild %s: %w", table, err)
		}

		names = append(names, n)
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("schema: rebuild %s: %w", table, err)
	}

	if len(names) > 0 {
		return fmt.Errorf(
			"schema: rebuild %s would drop its trigger(s) %s; recreate them afterwards and pass DropTriggers() to accept",
			table,
			strings.Join(names, ", "),
		)
	}

	return nil
}

type copyCol struct{ from, to string }

// rebuildCopy lists the columns copied from the old table: those of the new
// definition that are not generated and exist in the old table (have == nil
// while recording: every one does).
func rebuildCopy(t *Table, renames map[string]string, have map[string]bool) []copyCol {
	var out []copyCol

	for _, c := range t.cols {
		if c.generated != "" {
			continue
		}

		from := c.name
		if r, ok := renames[c.name]; ok {
			from = r
		}

		if have == nil || have[from] {
			out = append(out, copyCol{from, c.name})
		}
	}

	return out
}

// checkNewColumns refuses a new NOT NULL column without a default when the
// table has rows: the copy could not fill it.
func (s *Schema) checkNewColumns(
	db *database.DB,
	t *Table,
	renames map[string]string,
	have map[string]bool,
	table string,
) error {
	var rows int64

	counted := false

	for _, c := range t.cols {
		from := c.name
		if r, ok := renames[c.name]; ok {
			from = r
		}

		if have[from] || c.nullable || c.hasDefault || c.generated != "" || c.kind == kindID {
			continue
		}

		if !counted {
			if err := db.QueryRowContext(s.ctx, "SELECT COUNT(*) FROM "+s.q(table)).Scan(&rows); err != nil {
				return fmt.Errorf("schema: rebuild %s: %w", table, err)
			}

			counted = true
		}

		if rows > 0 {
			return fmt.Errorf("schema: rebuild %s: new column %s is NOT NULL without a default", table, c.name)
		}
	}

	return nil
}

// rebuildStatements is the statement sequence of the procedure; withPragmas
// adds the foreign key pragmas and the transaction around it (the recorded
// form).
func (s *Schema) rebuildStatements(t *Table, cols []copyCol, withPragmas bool) []string {
	tmp := rebuildPrefix + t.name

	var stmts []string

	if withPragmas {
		stmts = append(stmts, "PRAGMA foreign_keys = OFF", "BEGIN IMMEDIATE")
	}

	stmts = append(stmts, s.createTableSQL(t, tmp))

	if len(cols) > 0 {
		to := make([]string, len(cols))
		from := make([]string, len(cols))

		for i, c := range cols {
			to[i], from[i] = s.q(c.to), s.q(c.from)
		}

		stmts = append(stmts, "INSERT INTO "+s.q(tmp)+" ("+strings.Join(to, ", ")+") SELECT "+
			strings.Join(from, ", ")+" FROM "+s.q(t.name))
	}

	stmts = append(stmts, "DROP TABLE "+s.q(t.name), "ALTER TABLE "+s.q(tmp)+" RENAME TO "+s.q(t.name))
	stmts = append(stmts, s.indexStmts(t)...)

	if withPragmas {
		stmts = append(stmts, "PRAGMA foreign_key_check", "COMMIT", "PRAGMA foreign_keys = ON")
	}

	return stmts
}

// rebuildRun executes the procedure on one pinned connection: foreign keys
// off, one transaction, foreign_key_check before the commit. Whatever
// happens, the transaction is rolled back unless committed and the foreign
// keys are turned back on, with a context that outlives a cancelled one.
func (s *Schema) rebuildRun(db *database.DB, t *Table, cols []copyCol) (err error) {
	ctx := s.ctx

	conn, err := db.SQL().Conn(ctx)
	if err != nil {
		return fmt.Errorf("schema: rebuild %s: %w", t.name, err)
	}
	defer conn.Close()

	var fkOn int
	if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fkOn); err != nil {
		return fmt.Errorf("schema: rebuild %s: %w", t.name, err)
	}

	run := func(stmt string) error {
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("schema: rebuild %s: %w\n%s", t.name, err, stmt)
		}

		return nil
	}

	if fkOn == 1 {
		if err := run("PRAGMA foreign_keys = OFF"); err != nil {
			return err
		}
	}

	if err := run("BEGIN IMMEDIATE"); err != nil {
		return errors.Join(err, restoreConn(ctx, conn, true, fkOn == 1, t.name))
	}

	committed := false

	defer func() {
		err = errors.Join(err, restoreConn(ctx, conn, committed, fkOn == 1, t.name))
	}()

	// A leftover of an interrupted run.
	if err := run("DROP TABLE IF EXISTS " + s.q(rebuildPrefix+t.name)); err != nil {
		return err
	}

	seq, hasSeq, err := sequenceOf(ctx, conn, t.name)
	if err != nil {
		return fmt.Errorf("schema: rebuild %s: %w", t.name, err)
	}

	for _, stmt := range s.rebuildStatements(t, cols, false) {
		if err := run(stmt); err != nil {
			return err
		}
	}

	if hasSeq && slices.ContainsFunc(t.cols, func(c *Column) bool { return c.kind == kindID }) {
		// The copy leaves the counter at the largest id left; the old one also
		// remembers ids that were used and deleted, which must not be reused.
		if _, err := conn.ExecContext(ctx, "DELETE FROM sqlite_sequence WHERE name = ?", t.name); err != nil {
			return fmt.Errorf("schema: rebuild %s: %w", t.name, err)
		}

		if _, err := conn.ExecContext(
			ctx,
			"INSERT INTO sqlite_sequence (name, seq) VALUES (?, ?)",
			t.name,
			seq,
		); err != nil {
			return fmt.Errorf("schema: rebuild %s: %w", t.name, err)
		}
	}

	if fkOn == 1 {
		violations, err := foreignKeyViolations(ctx, conn)
		if err != nil {
			return fmt.Errorf("schema: rebuild %s: %w", t.name, err)
		}

		if violations > 0 {
			return fmt.Errorf("schema: rebuild %s leaves %d foreign key violation(s)", t.name, violations)
		}
	}

	if err := run("COMMIT"); err != nil {
		return err
	}

	committed = true
	s.executed++

	return nil
}

// restoreConn rolls back an open transaction and turns foreign keys back on,
// with a context that outlives a cancelled one. A connection that cannot be
// put back is discarded rather than returned to the pool with foreign keys
// off or a transaction open.
func restoreConn(ctx context.Context, conn *sql.Conn, committed, fkWasOn bool, table string) error {
	undo := context.WithoutCancel(ctx)

	var err error

	if !committed {
		if _, e := conn.ExecContext(undo, "ROLLBACK"); e != nil {
			err = errors.Join(err, e)
		}
	}

	if fkWasOn {
		if _, e := conn.ExecContext(undo, "PRAGMA foreign_keys = ON"); e != nil {
			err = errors.Join(err, e)
		}
	}

	if err == nil {
		return nil
	}

	_ = conn.Raw(func(any) error { return driver.ErrBadConn })

	return fmt.Errorf("schema: rebuild %s: connection discarded: %w", table, err)
}

// sequenceOf reads the AUTOINCREMENT counter of table, if it has one.
func sequenceOf(ctx context.Context, conn *sql.Conn, table string) (seq int64, ok bool, err error) {
	var n int

	if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE name = 'sqlite_sequence'").
		Scan(&n); err != nil ||
		n == 0 {
		return 0, false, err
	}

	err = conn.QueryRowContext(ctx, "SELECT seq FROM sqlite_sequence WHERE name = ?", table).Scan(&seq)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}

	return seq, err == nil, err
}

func foreignKeyViolations(ctx context.Context, conn *sql.Conn) (int, error) {
	rows, err := conn.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	n := 0
	for rows.Next() {
		n++
	}

	return n, rows.Err()
}
