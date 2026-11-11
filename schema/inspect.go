package schema

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/AlexGalhardo/vukem/database"
)

// Snapshot is the schema of a database as its catalog reports it.
type Snapshot struct {
	Tables []TableInfo // sorted by name
}

// Table returns the table called name.
func (s Snapshot) Table(name string) (TableInfo, bool) {
	for _, t := range s.Tables {
		if t.Name == name {
			return t, true
		}
	}

	return TableInfo{}, false
}

// TableInfo describes one table.
type TableInfo struct {
	Name        string
	Columns     []ColumnInfo // in declaration order
	PrimaryKey  []string
	Indexes     []IndexInfo
	ForeignKeys []ForeignKeyInfo
	// SQLite is set on SQLite only (nil elsewhere); a pointer keeps
	// TableInfo cheap to copy.
	SQLite *SQLiteInfo
}

// SQLiteInfo holds what the builder cannot reproduce on a SQLite table, so a
// Rebuild of it drops them: its triggers, and whether its definition
// mentions CHECK.
type SQLiteInfo struct {
	Triggers []string
	HasCheck bool
}

// ColumnInfo describes one column.
type ColumnInfo struct {
	Name string
	// Type is the type as the database reports it, lower-cased
	// ("varchar(255)", "bigint", "text").
	Type string
	// Kind is the schema builder method that produces Type ("String",
	// "BigInteger", "Timestamp"...), or "" when none does. A type several
	// methods share maps to the most common one: on SQLite INTEGER is
	// "BigInteger" and TEXT is "Text" (SQLite keeps no difference between
	// Integer and BigInteger, or between Text, JSON, UUID and Enum).
	Kind      string
	Length    int // String and ULID only
	Precision int // Decimal only
	Scale     int // Decimal only
	Nullable  bool
	Default   sql.Null[string] // the default expression as text
	Generated bool
}

// IndexInfo describes one index (the primary key's index is not listed).
type IndexInfo struct {
	// Name is the index name; "" for the unnamed index SQLite creates for a
	// UNIQUE column constraint.
	Name    string
	Columns []string
	Unique  bool
	Where   string // partial index predicate, "" when none
	// Expression is true when a part of the index is an expression, not a
	// column (CREATE INDEX ON t (lower(email))). The builder cannot write it,
	// so Columns lists only its plain columns and the index is not
	// representable.
	Expression bool
}

// ForeignKeyInfo describes one foreign key.
type ForeignKeyInfo struct {
	Columns    []string
	RefTable   string
	RefColumns []string
	OnDelete   string // "CASCADE", "SET NULL", "RESTRICT", "NO ACTION"
}

// Inspect reads the schema of the database behind ex: every base table of
// the current schema (PostgreSQL), database (MySQL) or file (SQLite).
func Inspect(ctx context.Context, ex database.Executor) (Snapshot, error) {
	names, err := listTables(ctx, ex)
	if err != nil {
		return Snapshot{}, err
	}

	var snap Snapshot

	for _, n := range names {
		t, err := inspectTable(ctx, ex, n)
		if err != nil {
			return Snapshot{}, err
		}

		snap.Tables = append(snap.Tables, t)
	}

	return snap, nil
}

// InspectTable reads one table; the second result is false when it does
// not exist.
func InspectTable(ctx context.Context, ex database.Executor, table string) (TableInfo, bool, error) {
	if err := checkIdent("table", table); err != nil {
		return TableInfo{}, false, err
	}

	has, err := hasTable(ctx, ex, table)
	if err != nil || !has {
		return TableInfo{}, false, err
	}

	t, err := inspectTable(ctx, ex, table)

	return t, err == nil, err
}

func family(ex database.Executor) string { return database.MustCapabilities(ex.Dialect()).Family }

func listTables(ctx context.Context, ex database.Executor) ([]string, error) {
	var q string

	switch family(ex) {
	case "postgres":
		q = `SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = current_schema() AND c.relkind IN ('r', 'p') ORDER BY c.relname`
	case "mysql":
		q = `SELECT table_name FROM information_schema.tables
			WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE' ORDER BY table_name`
	default:
		q = `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`
	}

	rows, err := ex.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("schema: inspect: list tables: %w", err)
	}
	defer rows.Close()

	var names []string

	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, fmt.Errorf("schema: inspect: list tables: %w", err)
		}

		names = append(names, n)
	}

	return names, rows.Err()
}

func inspectTable(ctx context.Context, ex database.Executor, table string) (TableInfo, error) {
	t := TableInfo{Name: table}

	var err error

	switch family(ex) {
	case "postgres":
		err = inspectPostgres(ctx, ex, &t)
	case "mysql":
		err = inspectMySQL(ctx, ex, &t)
	default:
		err = inspectSQLite(ctx, ex, &t)
	}

	if err != nil {
		return TableInfo{}, fmt.Errorf("schema: inspect %s: %w", table, err)
	}

	return t, nil
}

// collect runs a catalog query with the table name bound and calls fn for
// every row.
func collect(ctx context.Context, ex database.Executor, q string, table string, fn func(*sql.Rows) error) error {
	rows, err := ex.QueryContext(ctx, q, table)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}

	return rows.Err()
}

func splitList(s string) []string {
	if s == "" {
		return nil
	}

	return strings.Split(s, ",")
}

// ------------------------------------------------------------------ SQLite

var whereRe = regexp.MustCompile(`(?is)\swhere\s(.+)$`)

func inspectSQLite(ctx context.Context, ex database.Executor, t *TableInfo) error {
	type pkCol struct {
		name string
		pos  int
	}

	var pks []pkCol

	err := collect(ctx, ex,
		`SELECT name, type, "notnull", dflt_value, pk, hidden FROM pragma_table_xinfo(?) ORDER BY cid`, t.Name,
		func(r *sql.Rows) error {
			var (
				c       ColumnInfo
				typ     string
				notNull int
				pk      int
				hidden  int
			)

			if err := r.Scan(&c.Name, &typ, &notNull, &c.Default, &pk, &hidden); err != nil {
				return err
			}

			c.Type = strings.ToLower(typ)
			c.Nullable = notNull == 0 && pk == 0
			c.Generated = hidden == 2 || hidden == 3

			if pk > 0 {
				pks = append(pks, pkCol{c.Name, pk})
			}

			t.Columns = append(t.Columns, c)

			return nil
		})
	if err != nil {
		return err
	}

	slices.SortFunc(pks, func(a, b pkCol) int { return a.pos - b.pos })

	for _, p := range pks {
		t.PrimaryKey = append(t.PrimaryKey, p.name)
	}

	var autoinc bool

	t.SQLite = &SQLiteInfo{}

	if err := ex.QueryRowContext(
		ctx,
		`SELECT COALESCE(INSTR(LOWER(sql), 'autoincrement'), 0) > 0, COALESCE(INSTR(LOWER(sql), 'check'), 0) > 0
		FROM sqlite_master WHERE type = 'table' AND name = ?`,
		t.Name,
	).Scan(&autoinc, &t.SQLite.HasCheck); err != nil {
		return err
	}

	err = collect(
		ctx,
		ex,
		`SELECT name FROM sqlite_master WHERE type = 'trigger' AND tbl_name = ? ORDER BY name`,
		t.Name,
		func(r *sql.Rows) error {
			var n string
			if err := r.Scan(&n); err != nil {
				return err
			}

			t.SQLite.Triggers = append(t.SQLite.Triggers, n)

			return nil
		},
	)
	if err != nil {
		return err
	}

	for i := range t.Columns {
		c := &t.Columns[i]
		c.Kind, c.Length, c.Precision, c.Scale = kindOf(
			"sqlite",
			c.Type,
			autoinc && len(t.PrimaryKey) == 1 && t.PrimaryKey[0] == c.Name,
		)
	}

	if err := sqliteIndexes(ctx, ex, t); err != nil {
		return err
	}

	return sqliteForeignKeys(ctx, ex, t)
}

func sqliteIndexes(ctx context.Context, ex database.Executor, t *TableInfo) error {
	type idx struct {
		name    string
		unique  bool
		origin  string
		partial bool
	}

	var list []idx

	err := collect(ctx, ex, `SELECT name, "unique", origin, partial FROM pragma_index_list(?) ORDER BY seq`, t.Name,
		func(r *sql.Rows) error {
			var (
				i             idx
				unique, partl int
			)

			if err := r.Scan(&i.name, &unique, &i.origin, &partl); err != nil {
				return err
			}

			i.unique, i.partial = unique == 1, partl == 1
			list = append(list, i)

			return nil
		})
	if err != nil {
		return err
	}

	for _, i := range list {
		if i.origin == "pk" {
			continue
		}

		info := IndexInfo{Name: i.name, Unique: i.unique}
		if i.origin == "u" {
			info.Name = "" // a UNIQUE column constraint: SQLite names it itself
		}

		err := collect(
			ctx,
			ex,
			`SELECT name FROM pragma_index_info(?) ORDER BY seqno`,
			i.name,
			func(r *sql.Rows) error {
				var c sql.NullString
				if err := r.Scan(&c); err != nil {
					return err
				}

				if !c.Valid {
					info.Expression = true
					return nil
				}

				info.Columns = append(info.Columns, c.String)

				return nil
			},
		)
		if err != nil {
			return err
		}

		if i.partial {
			var def sql.NullString
			if err := ex.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type = 'index' AND name = ?`, i.name).
				Scan(&def); err != nil {
				return err
			}

			if m := whereRe.FindStringSubmatch(def.String); m != nil {
				info.Where = strings.TrimSpace(m[1])
			}
		}

		t.Indexes = append(t.Indexes, info)
	}

	slices.SortFunc(t.Indexes, func(a, b IndexInfo) int {
		if c := strings.Compare(a.Name, b.Name); c != 0 {
			return c
		}

		return strings.Compare(strings.Join(a.Columns, ","), strings.Join(b.Columns, ","))
	})

	return nil
}

func sqliteForeignKeys(ctx context.Context, ex database.Executor, t *TableInfo) error {
	fks := map[int]*ForeignKeyInfo{}

	var order []int

	err := collect(
		ctx,
		ex,
		`SELECT id, "table", "from", "to", on_delete FROM pragma_foreign_key_list(?) ORDER BY id, seq`,
		t.Name,
		func(r *sql.Rows) error {
			var (
				id            int
				ref, from, to string
				del           string
			)

			if err := r.Scan(&id, &ref, &from, &to, &del); err != nil {
				return err
			}

			fk := fks[id]
			if fk == nil {
				fk = &ForeignKeyInfo{RefTable: ref, OnDelete: strings.ToUpper(del)}
				fks[id] = fk
				order = append(order, id)
			}

			fk.Columns = append(fk.Columns, from)
			fk.RefColumns = append(fk.RefColumns, to)

			return nil
		},
	)
	if err != nil {
		return err
	}

	for _, id := range order {
		t.ForeignKeys = append(t.ForeignKeys, *fks[id])
	}

	return nil
}

// ------------------------------------------------------------------ PostgreSQL

func inspectPostgres(ctx context.Context, ex database.Executor, t *TableInfo) error {
	err := collect(ctx, ex, `SELECT a.attname, format_type(a.atttypid, a.atttypmod), NOT a.attnotnull,
			pg_get_expr(d.adbin, d.adrelid), a.attgenerated <> '', a.attidentity <> ''
		FROM pg_attribute a
		JOIN pg_class c ON c.oid = a.attrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
		WHERE c.relname = $1 AND n.nspname = current_schema() AND a.attnum > 0 AND NOT a.attisdropped
		ORDER BY a.attnum`, t.Name, func(r *sql.Rows) error {
		var (
			c        ColumnInfo
			identity bool
		)

		if err := r.Scan(&c.Name, &c.Type, &c.Nullable, &c.Default, &c.Generated, &identity); err != nil {
			return err
		}

		c.Type = strings.ToLower(c.Type)
		c.Kind, c.Length, c.Precision, c.Scale = kindOf("postgres", c.Type, identity)
		t.Columns = append(t.Columns, c)

		return nil
	})
	if err != nil {
		return err
	}

	err = collect(ctx, ex, `SELECT i.relname, ix.indisunique, ix.indisprimary,
			string_agg(a.attname, ',' ORDER BY k.ord), COALESCE(pg_get_expr(ix.indpred, ix.indrelid), ''),
			bool_or(k.attnum = 0)
		FROM pg_index ix
		JOIN pg_class t ON t.oid = ix.indrelid
		JOIN pg_class i ON i.oid = ix.indexrelid
		JOIN pg_namespace n ON n.oid = t.relnamespace
		CROSS JOIN LATERAL unnest(ix.indkey::int2[]) WITH ORDINALITY AS k(attnum, ord)
		LEFT JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = k.attnum
		WHERE t.relname = $1 AND n.nspname = current_schema()
		GROUP BY i.relname, ix.indisunique, ix.indisprimary, ix.indpred, ix.indrelid
		ORDER BY i.relname`, t.Name, func(r *sql.Rows) error {
		var (
			i       IndexInfo
			primary bool
			cols    string
		)

		if err := r.Scan(&i.Name, &i.Unique, &primary, &cols, &i.Where, &i.Expression); err != nil {
			return err
		}

		i.Columns = splitList(cols)
		if primary {
			t.PrimaryKey = i.Columns
			return nil
		}

		t.Indexes = append(t.Indexes, i)

		return nil
	})
	if err != nil {
		return err
	}

	return collect(ctx, ex, `SELECT
			(SELECT string_agg(a.attname, ',' ORDER BY k.ord) FROM unnest(con.conkey) WITH ORDINALITY AS k(attnum, ord)
				JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = k.attnum),
			ref.relname,
			(SELECT string_agg(a.attname, ',' ORDER BY k.ord) FROM unnest(con.confkey) WITH ORDINALITY AS k(attnum, ord)
				JOIN pg_attribute a ON a.attrelid = con.confrelid AND a.attnum = k.attnum),
			con.confdeltype::text
		FROM pg_constraint con
		JOIN pg_class rel ON rel.oid = con.conrelid
		JOIN pg_namespace n ON n.oid = rel.relnamespace
		JOIN pg_class ref ON ref.oid = con.confrelid
		WHERE con.contype = 'f' AND rel.relname = $1 AND n.nspname = current_schema()
		ORDER BY con.conname`, t.Name, func(r *sql.Rows) error {
		var cols, refCols, del string

		fk := ForeignKeyInfo{}
		if err := r.Scan(&cols, &fk.RefTable, &refCols, &del); err != nil {
			return err
		}

		fk.Columns, fk.RefColumns = splitList(cols), splitList(refCols)
		fk.OnDelete = map[string]string{"a": "NO ACTION", "r": "RESTRICT", "c": "CASCADE", "n": "SET NULL", "d": "SET DEFAULT"}[del]
		t.ForeignKeys = append(t.ForeignKeys, fk)

		return nil
	})
}

// ------------------------------------------------------------------ MySQL

func inspectMySQL(ctx context.Context, ex database.Executor, t *TableInfo) error {
	err := collect(ctx, ex, `SELECT column_name, column_type, is_nullable = 'YES', column_default, extra, column_key
		FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = ? ORDER BY ordinal_position`,
		t.Name, func(r *sql.Rows) error {
			var (
				c               ColumnInfo
				extra, columnKy sql.NullString
			)

			if err := r.Scan(&c.Name, &c.Type, &c.Nullable, &c.Default, &extra, &columnKy); err != nil {
				return err
			}

			c.Type = strings.ToLower(c.Type)
			low := strings.ToLower(extra.String)
			c.Generated = strings.Contains(low, "generated")
			c.Kind, c.Length, c.Precision, c.Scale = kindOf("mysql", c.Type, strings.Contains(low, "auto_increment"))
			t.Columns = append(t.Columns, c)

			return nil
		})
	if err != nil {
		return err
	}

	type stat struct {
		name   string
		unique bool
		cols   []string
		expr   bool
	}

	var (
		stats []*stat
		by    = map[string]*stat{}
	)

	err = collect(ctx, ex, `SELECT index_name, non_unique, column_name FROM information_schema.statistics
		WHERE table_schema = DATABASE() AND table_name = ? ORDER BY index_name, seq_in_index`, t.Name,
		func(r *sql.Rows) error {
			var (
				name      string
				nonUnique int
				col       sql.NullString
			)

			if err := r.Scan(&name, &nonUnique, &col); err != nil {
				return err
			}

			s := by[name]
			if s == nil {
				s = &stat{name: name, unique: nonUnique == 0}
				by[name] = s
				stats = append(stats, s)
			}

			if col.Valid {
				s.cols = append(s.cols, col.String)
			} else {
				s.expr = true
			}

			return nil
		})
	if err != nil {
		return err
	}

	for _, s := range stats {
		if s.name == "PRIMARY" {
			t.PrimaryKey = s.cols
			continue
		}

		t.Indexes = append(t.Indexes, IndexInfo{Name: s.name, Columns: s.cols, Unique: s.unique, Expression: s.expr})
	}

	byName := map[string]*ForeignKeyInfo{}

	var names []string

	return collect(
		ctx,
		ex,
		`SELECT k.constraint_name, k.column_name, k.referenced_table_name, k.referenced_column_name, r.delete_rule
		FROM information_schema.key_column_usage k
		JOIN information_schema.referential_constraints r
			ON r.constraint_schema = k.constraint_schema AND r.constraint_name = k.constraint_name AND r.table_name = k.table_name
		WHERE k.table_schema = DATABASE() AND k.table_name = ? AND k.referenced_table_name IS NOT NULL
		ORDER BY k.constraint_name, k.ordinal_position`,
		t.Name,
		func(r *sql.Rows) error {
			var name, col, ref, refCol, del string
			if err := r.Scan(&name, &col, &ref, &refCol, &del); err != nil {
				return err
			}

			fk := byName[name]
			if fk == nil {
				fk = &ForeignKeyInfo{RefTable: ref, OnDelete: strings.ToUpper(del)}
				byName[name] = fk
				names = append(names, name)
				t.ForeignKeys = append(t.ForeignKeys, ForeignKeyInfo{})
			}

			fk.Columns = append(fk.Columns, col)
			fk.RefColumns = append(fk.RefColumns, refCol)
			t.ForeignKeys[slices.Index(names, name)] = *fk

			return nil
		},
	)
}

// ------------------------------------------------------------------ type mapping

var intWidthRe = regexp.MustCompile(`^(bigint|int|smallint|mediumint)\(\d+\)`)

var (
	lenRe   = regexp.MustCompile(`^(?:varchar|character varying|char|character)\((\d+)\)$`)
	scaleRe = regexp.MustCompile(`^(?:numeric|decimal)\((\d+),\s*(\d+)\)$`)
)

// kindOf maps a catalog type back to the schema builder method that writes
// it (the inverse of sqlType and mysqlType); special marks an auto-increment
// primary key (ID).
func kindOf(fam, typ string, special bool) (kind string, length, precision, scale int) {
	if fam == "mysql" {
		// MariaDB (and MySQL before 8.0.19) report integer display widths.
		typ = intWidthRe.ReplaceAllString(typ, "$1")
	}

	if m := lenRe.FindStringSubmatch(typ); m != nil {
		n, _ := strconv.Atoi(m[1])

		fixed := strings.HasPrefix(typ, "char(") || strings.HasPrefix(typ, "character(")

		switch {
		case fixed && n == 26:
			return "ULID", 26, 0, 0
		case fixed && n == 36 && fam == "mysql":
			return "UUID", 0, 0, 0
		case fixed:
			return "", 0, 0, 0
		default:
			return "String", n, 0, 0
		}
	}

	if m := scaleRe.FindStringSubmatch(typ); m != nil {
		p, _ := strconv.Atoi(m[1])
		s, _ := strconv.Atoi(m[2])

		return "Decimal", 0, p, s
	}

	switch fam {
	case "postgres":
		switch typ {
		case "bigint":
			return pick(special, "ID", "BigInteger"), 0, 0, 0
		case "integer":
			return "Integer", 0, 0, 0
		case "smallint":
			return "SmallInteger", 0, 0, 0
		case "boolean":
			return "Boolean", 0, 0, 0
		case "text":
			return "Text", 0, 0, 0
		case "double precision":
			return "Float", 0, 0, 0
		case "date":
			return "Date", 0, 0, 0
		case "timestamp with time zone":
			return "Timestamp", 0, 0, 0
		case "jsonb":
			return "JSON", 0, 0, 0
		case "uuid":
			return "UUID", 0, 0, 0
		case "bytea":
			return "Binary", 0, 0, 0
		}
	case "mysql":
		switch typ {
		case "bigint":
			return pick(special, "ID", "BigInteger"), 0, 0, 0
		case "int":
			return "Integer", 0, 0, 0
		case "smallint":
			return "SmallInteger", 0, 0, 0
		case "tinyint(1)":
			return "Boolean", 0, 0, 0
		case "longtext":
			return "Text", 0, 0, 0
		case "double":
			return "Float", 0, 0, 0
		case "date":
			return "Date", 0, 0, 0
		case "datetime(6)":
			return "Timestamp", 0, 0, 0
		case "json":
			return "JSON", 0, 0, 0
		case "longblob":
			return "Binary", 0, 0, 0
		}
	default:
		switch typ {
		case "integer":
			return pick(special, "ID", "BigInteger"), 0, 0, 0
		case "smallint":
			return "SmallInteger", 0, 0, 0
		case "boolean":
			return "Boolean", 0, 0, 0
		case "text":
			return "Text", 0, 0, 0
		case "real":
			return "Float", 0, 0, 0
		case "date":
			return "Date", 0, 0, 0
		case "timestamp":
			return "Timestamp", 0, 0, 0
		case "blob":
			return "Binary", 0, 0, 0
		}
	}

	return "", 0, 0, 0
}

func pick(first bool, a, b string) string {
	if first {
		return a
	}

	return b
}
