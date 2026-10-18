package testutil

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"testing"
)

// SQLiteSchema returns one normalized line per column and per index of
// tables, read from SQLite's own catalog instead of the statements that
// created them. Two databases built by different code paths (a store's
// boot-time CREATE TABLE and its migration) have the same schema when
// their lines are equal.
func SQLiteSchema(tb testing.TB, db *sql.DB, tables ...string) []string {
	tb.Helper()

	var out []string

	for _, t := range tables {
		out = append(out, columnLines(tb, db, t)...)
		out = append(out, indexLines(tb, db, t)...)
	}

	return out
}

func columnLines(tb testing.TB, db *sql.DB, table string) []string {
	tb.Helper()

	rows, err := db.QueryContext(context.Background(),
		`SELECT name, type, "notnull", COALESCE(dflt_value, ''), pk FROM pragma_table_info(?) ORDER BY name`, table)
	if err != nil {
		tb.Fatalf("read columns of %s: %v", table, err)
	}
	defer rows.Close()

	var out []string

	for rows.Next() {
		var (
			name, typ, def string
			notNull, pk    int
		)

		if err := rows.Scan(&name, &typ, &notNull, &def, &pk); err != nil {
			tb.Fatalf("scan column of %s: %v", table, err)
		}

		out = append(
			out,
			fmt.Sprintf("column %s.%s %s notnull=%d default=%q pk=%d", table, name, typ, notNull, def, pk),
		)
	}

	if err := rows.Err(); err != nil {
		tb.Fatalf("read columns of %s: %v", table, err)
	}

	if out == nil {
		tb.Fatalf("table %s has no columns: it does not exist", table)
	}

	return out
}

func indexLines(tb testing.TB, db *sql.DB, table string) []string {
	tb.Helper()

	rows, err := db.QueryContext(context.Background(),
		`SELECT il.name, il."unique", ii.name FROM pragma_index_list(?) il
			JOIN pragma_index_info(il.name) ii ORDER BY il.name, ii.seqno`, table)
	if err != nil {
		tb.Fatalf("read indexes of %s: %v", table, err)
	}
	defer rows.Close()

	cols := map[string][]string{}
	unique := map[string]int{}

	var names []string

	for rows.Next() {
		var (
			index, column string
			uniq          int
		)

		if err := rows.Scan(&index, &uniq, &column); err != nil {
			tb.Fatalf("scan index of %s: %v", table, err)
		}

		if _, seen := unique[index]; !seen {
			names = append(names, index)
		}

		unique[index] = uniq
		cols[index] = append(cols[index], column)
	}

	if err := rows.Err(); err != nil {
		tb.Fatalf("read indexes of %s: %v", table, err)
	}

	slices.Sort(names)

	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, fmt.Sprintf("index %s.%s unique=%d cols=%v", table, n, unique[n], cols[n]))
	}

	return out
}
