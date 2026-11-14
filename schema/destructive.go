package schema

import (
	"context"
	"regexp"
	"strings"

	"github.com/AlexGalhardo/vukem/database"
)

var (
	quoteStripper = strings.NewReplacer("\"", "", "`", "", "[", "", "]", "")
	dropTableRe   = regexp.MustCompile(`(?is)^\s*DROP\s+(?:TEMPORARY\s+)?TABLE\s+(?:IF\s+EXISTS\s+)?([^;]+)`)
	alterTableRe  = regexp.MustCompile(`(?is)^\s*ALTER\s+TABLE\s`)
	// dropClauseRe finds each DROP clause of an ALTER TABLE, also in the
	// combined form (ALTER TABLE t ADD ..., DROP COLUMN c).
	dropClauseRe = regexp.MustCompile(`(?is)\bDROP\s+(COLUMN\s+)?(?:IF\s+EXISTS\s+)?(\S+)`)
)

// Destructive lists what Up of mig would destroy on the database db, read
// from the statements it generates without running them: DROP TABLE, DROP
// COLUMN (also inside a combined ALTER TABLE) and, on SQLite, a Rebuild whose
// definition leaves out a column the table has (a table that is rebuilt in
// place is not a drop). Statements written with Schema.Raw count too.
//
// It does not flag TRUNCATE or DELETE FROM, and it records Up for an empty
// database, so a Schema.HasTable guard around a drop is not evaluated: the
// drop is reported. A Rebuild of a table that an earlier pending migration
// creates is not checked, since that table has no rows yet.
func Destructive(ctx context.Context, db *database.DB, mig Migration) ([]string, error) {
	rec, stmts := recorder(db.Dialect())

	var inspectErr error

	rec.liveColumns = func(table string) []string {
		info, ok, err := InspectTable(ctx, db, table)
		if err != nil && inspectErr == nil {
			inspectErr = err
		}

		if !ok {
			return nil
		}

		cols := make([]string, len(info.Columns))
		for i, c := range info.Columns {
			cols[i] = c.Name
		}

		return cols
	}

	if err := mig.Up(rec); err != nil {
		return nil, err
	}

	if inspectErr != nil {
		return nil, inspectErr
	}

	renamedIn := map[string]bool{} // tables a rebuild puts back under their name

	for _, st := range *stmts {
		if strings.HasPrefix(st, "ALTER TABLE") && strings.Contains(st, rebuildPrefix) {
			if _, to, ok := strings.Cut(st, " RENAME TO "); ok {
				renamedIn[strings.Trim(to, "\"`")] = true
			}
		}
	}

	out := rec.lost

	for _, st := range *stmts {
		if m := dropTableRe.FindStringSubmatch(st); m != nil {
			for name := range strings.SplitSeq(m[1], ",") {
				name = quoteStripper.Replace(strings.TrimSpace(name))
				if !renamedIn[name] {
					out = append(out, "drop table "+name)
				}
			}
		} else if dropsColumn(st) {
			out = append(out, strings.TrimSpace(strings.ToLower(strings.Join(strings.Fields(st), " "))))
		}
	}

	return out, nil
}

// nonColumnDrops are what DROP can name instead of a column in an ALTER TABLE:
// constraints, indexes and column attributes. MySQL and MariaDB also accept
// DROP c without COLUMN, so any other word is read as a column.
var nonColumnDrops = map[string]bool{
	"CONSTRAINT": true, "DEFAULT": true, "NOT": true, "IDENTITY": true, "EXPRESSION": true,
	"INDEX": true, "KEY": true, "PRIMARY": true, "FOREIGN": true, "CHECK": true,
}

// dropsColumn reports whether an ALTER TABLE statement drops a column.
func dropsColumn(st string) bool {
	if !alterTableRe.MatchString(st) {
		return false
	}

	for _, m := range dropClauseRe.FindAllStringSubmatch(st, -1) {
		if m[1] != "" || !nonColumnDrops[strings.ToUpper(m[2])] {
			return true
		}
	}

	return false
}
