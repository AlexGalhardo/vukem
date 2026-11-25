package schema

import (
	"errors"
	"fmt"
	"strings"

	"github.com/AlexGalhardo/vukem/database"
)

// ReplaceCheck rewrites one CHECK constraint of a SQLite table, keeping
// everything else the table has: its columns, constraints, indexes and
// triggers (with their original SQL) and AUTOINCREMENT counter. old is the expression of
// the CHECK to replace, as [SQLiteInfo.Checks] lists it (whitespace and case
// of keywords are not compared loosely: copy it from there); expr is the new
// one, without the CHECK keyword and the outer parentheses:
//
//	s.ReplaceCheck("recordings",
//		"status IN ('draft', 'recording', 'done')",
//		"status IN ('draft', 'recording', 'paused', 'done')")
//
// It is the common case of [Schema.Rebuild], adding a value to a closed
// list, without describing the whole table again. The table is rebuilt from
// its own CREATE TABLE text, so the rows are copied as they are and must
// satisfy expr: a row that does not fails the migration and changes nothing.
// Like Rebuild it needs Migration.NoTransaction and runs PRAGMA
// foreign_key_check before it commits. It fails when no CHECK, or more than one, has the
// expression old.
//
// SQLite only: on PostgreSQL and MySQL drop and add the named constraint
// with Raw (ALTER TABLE ... DROP CONSTRAINT / ADD CONSTRAINT ... CHECK).
func (s *Schema) ReplaceCheck(table, old, expr string) error {
	if err := checkIdent("table", table); err != nil {
		return err
	}

	if s.caps().Family != "sqlite" {
		return fmt.Errorf("schema: ReplaceCheck is for sqlite; on %s drop and add the constraint with Raw", s.d.Name())
	}

	old, expr = strings.TrimSpace(old), strings.TrimSpace(expr)
	if old == "" || expr == "" {
		return errors.New("schema: ReplaceCheck needs the old and the new expression")
	}

	if s.model != nil {
		s.model.missing(table)
		return nil
	}

	// The checksum hashes this line: editing either expression of an
	// applied migration is reported like any other change.
	if s.record != nil {
		*s.record = append(*s.record, fmt.Sprintf("-- replace check on %s: (%s) with (%s)", table, old, expr))
		return nil
	}

	db, ok := s.ex.(*database.DB)
	if !ok {
		return errors.New(
			"schema: ReplaceCheck needs Migration.NoTransaction on sqlite (it turns foreign keys off outside a transaction)",
		)
	}

	info, exists, err := InspectTable(s.ctx, db, table)
	if err != nil {
		return err
	}

	if !exists {
		return fmt.Errorf("schema: replace check on %s: no such table", table)
	}

	var createSQL string
	if err := db.SQL().QueryRowContext(
		s.ctx, "SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?", table,
	).Scan(&createSQL); err != nil {
		return fmt.Errorf("schema: replace check on %s: %w", table, err)
	}

	tmpSQL, err := replaceCheckSQL(createSQL, s.q(rebuildPrefix+table), old, expr)
	if err != nil {
		return fmt.Errorf("schema: replace check on %s: %w", table, err)
	}

	indexes, err := storedObjects(s, db, table)
	if err != nil {
		return err
	}

	var cols []string

	for _, c := range info.Columns {
		if !c.Generated {
			cols = append(cols, s.q(c.Name))
		}
	}

	tmp := s.q(rebuildPrefix + table)
	list := strings.Join(cols, ", ")
	stmts := []string{
		tmpSQL,
		"INSERT INTO " + tmp + " (" + list + ") SELECT " + list + " FROM " + s.q(table),
		"DROP TABLE " + s.q(table),
		"ALTER TABLE " + tmp + " RENAME TO " + s.q(table),
	}
	stmts = append(stmts, indexes...)

	return s.rebuildRun(db, table, stmts, true)
}

// replaceCheckSQL returns createSQL creating the table quotedTmp, with the
// CHECK whose expression is old replaced by expr.
func replaceCheckSQL(createSQL, quotedTmp, old, expr string) (string, error) {
	spans := sqliteCheckSpans(createSQL)
	found := -1

	for i, sp := range spans {
		if strings.TrimSpace(createSQL[sp.start:sp.end]) != old {
			continue
		}

		if found >= 0 {
			return "", fmt.Errorf("more than one CHECK has the expression %q", old)
		}

		found = i
	}

	if found < 0 {
		have := make([]string, len(spans))
		for i, sp := range spans {
			have[i] = strings.TrimSpace(createSQL[sp.start:sp.end])
		}

		return "", fmt.Errorf("no CHECK has the expression %q (the table has %q)", old, have)
	}

	sp := spans[found]
	edited := createSQL[:sp.start] + expr + createSQL[sp.end:]

	// The new expression must stay one CHECK expression: one that closes
	// its parenthesis early would smuggle columns or constraints in.
	after := sqliteCheckSpans(edited)
	if len(after) != len(spans) || strings.TrimSpace(edited[after[found].start:after[found].end]) != expr {
		return "", fmt.Errorf("the expression %q is not one balanced CHECK expression", expr)
	}

	open := strings.IndexByte(edited, '(')
	if open < 0 || !strings.HasPrefix(strings.ToUpper(strings.TrimSpace(edited)), "CREATE TABLE") {
		return "", errors.New("unexpected CREATE TABLE text")
	}

	return "CREATE TABLE " + quotedTmp + " " + edited[open:], nil
}

// storedObjects returns the SQL text of every explicit index and trigger of
// table, indexes first, to run again after the table is replaced.
func storedObjects(s *Schema, db *database.DB, table string) ([]string, error) {
	rows, err := db.SQL().QueryContext(s.ctx,
		"SELECT sql FROM sqlite_master WHERE type IN ('index', 'trigger') AND tbl_name = ? AND sql IS NOT NULL "+
			"ORDER BY type, name", table)
	if err != nil {
		return nil, fmt.Errorf("schema: replace check on %s: %w", table, err)
	}
	defer rows.Close()

	var out []string

	for rows.Next() {
		var stmt string
		if err := rows.Scan(&stmt); err != nil {
			return nil, fmt.Errorf("schema: replace check on %s: %w", table, err)
		}

		out = append(out, stmt)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("schema: replace check on %s: %w", table, err)
	}

	return out, nil
}
