package schema

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/database"
)

// The PostgreSQL and MySQL catalog queries of Inspect need a server; these
// tests answer them with canned rows through a fake database/sql driver, so
// the row mapping (types, keys, indexes, foreign keys) is checked without one.
// The queries themselves are run against real servers by dbtest.

type fakeAnswer struct {
	match string // a substring of the query
	cols  []string
	rows  [][]driver.Value
	err   error
}

type fakeCatalog struct {
	mu      sync.Mutex
	answers []fakeAnswer
	queries []string
	args    [][]driver.NamedValue
}

var (
	fakeCatalogs   sync.Map // dsn -> *fakeCatalog
	fakeCatalogSeq atomic.Int64
)

func init() { sql.Register("vk_fake_catalog", fakeDriver{}) }

type fakeDriver struct{}

func (fakeDriver) Open(dsn string) (driver.Conn, error) {
	c, ok := fakeCatalogs.Load(dsn)
	if !ok {
		return nil, errors.New("unknown fake catalog")
	}

	return &fakeConn{c: c.(*fakeCatalog)}, nil
}

type fakeConn struct{ c *fakeCatalog }

func (*fakeConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("prepare is not used") }
func (*fakeConn) Close() error                        { return nil }
func (*fakeConn) Begin() (driver.Tx, error)           { return nil, errors.New("no transactions") }

func (f *fakeConn) QueryContext(_ context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	f.c.mu.Lock()
	defer f.c.mu.Unlock()

	f.c.queries = append(f.c.queries, q)
	f.c.args = append(f.c.args, args)

	for _, a := range f.c.answers {
		if strings.Contains(q, a.match) {
			if a.err != nil {
				return nil, a.err
			}

			return &fakeRows{cols: a.cols, rows: a.rows}, nil
		}
	}

	return nil, fmt.Errorf("unexpected query: %s", q)
}

type fakeRows struct {
	cols []string
	rows [][]driver.Value
	i    int
}

func (r *fakeRows) Columns() []string { return r.cols }
func (*fakeRows) Close() error        { return nil }

func (r *fakeRows) Next(dest []driver.Value) error {
	if r.i >= len(r.rows) {
		return io.EOF
	}

	copy(dest, r.rows[r.i])
	r.i++

	return nil
}

// fakeExec is a database.Executor over the fake driver, reporting dialect d.
type fakeExec struct {
	db *sql.DB
	d  database.Dialect
}

func (f fakeExec) ExecContext(ctx context.Context, q string, a ...any) (sql.Result, error) {
	return f.db.ExecContext(ctx, q, a...)
}

func (f fakeExec) QueryContext(ctx context.Context, q string, a ...any) (*sql.Rows, error) {
	return f.db.QueryContext(ctx, q, a...)
}

func (f fakeExec) QueryRowContext(ctx context.Context, q string, a ...any) *sql.Row {
	return f.db.QueryRowContext(ctx, q, a...)
}

func (f fakeExec) Dialect() database.Dialect { return f.d }

func newFakeExec(t *testing.T, d database.Dialect, answers ...fakeAnswer) (fakeExec, *fakeCatalog) {
	t.Helper()

	cat := &fakeCatalog{answers: answers}
	dsn := fmt.Sprintf("cat-%d", fakeCatalogSeq.Add(1))
	fakeCatalogs.Store(dsn, cat)

	db, err := sql.Open("vk_fake_catalog", dsn)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_ = db.Close()

		fakeCatalogs.Delete(dsn)
	})

	return fakeExec{db: db, d: d}, cat
}

func v(vals ...driver.Value) []driver.Value { return vals }

func TestInspectPostgresMapsTheCatalog(t *testing.T) {
	t.Parallel()

	ex, cat := newFakeExec(t, database.Postgres,
		fakeAnswer{match: "COUNT(*)", cols: []string{"n"}, rows: [][]driver.Value{v(int64(1))}},
		fakeAnswer{match: "c.relkind", cols: []string{"relname"}, rows: [][]driver.Value{v("posts")}},
		fakeAnswer{
			match: "FROM pg_attribute a",
			cols:  []string{"name", "type", "nullable", "default", "generated", "identity"},
			rows: [][]driver.Value{
				v("id", "bigint", false, nil, false, true),
				v("title", "character varying(120)", false, nil, false, false),
				v("price", "numeric(10,2)", true, "0", false, false),
				v("code", "character(26)", true, nil, false, false),
				v("flag", "boolean", false, "false", false, false),
				v("meta", "jsonb", true, nil, false, false),
				v("at", "timestamp with time zone", true, nil, false, false),
				v("raw", "bytea", true, nil, false, false),
				v("ref", "uuid", true, nil, false, false),
				v("ratio", "double precision", true, nil, false, false),
				v("small", "smallint", true, nil, false, false),
				v("count", "integer", true, nil, false, false),
				v("body", "text", true, nil, false, false),
				v("day", "date", true, nil, false, false),
				v("fixed", "character(3)", true, nil, false, false),
				v("search", "tsvector", true, nil, true, false),
			},
		},
		fakeAnswer{
			match: "FROM pg_index ix",
			cols:  []string{"name", "unique", "primary", "cols", "where", "expr"},
			rows: [][]driver.Value{
				v("posts_pkey", true, true, "id", "", false),
				v("posts_title_at_idx", false, false, "title,at", "", false),
				v("posts_slug_key", true, false, "code", "(flag)", false),
				v("posts_lower_idx", false, false, "title", "", true),
			},
		},
		fakeAnswer{
			match: "FROM pg_constraint con",
			cols:  []string{"cols", "ref", "refcols", "del"},
			rows: [][]driver.Value{
				v("ref", "users", "id", "c"),
				v("title,code", "pairs", "a,b", "n"),
			},
		},
	)

	snap, err := Inspect(t.Context(), ex)
	if err != nil {
		t.Fatal(err)
	}

	posts, ok := snap.Table("posts")
	if !ok || len(snap.Tables) != 1 {
		t.Fatalf("tables = %+v", snap.Tables)
	}

	kinds := map[string]string{}
	for _, c := range posts.Columns {
		kinds[c.Name] = c.Kind
	}

	want := map[string]string{
		"id": "ID", "title": "String", "price": "Decimal", "code": "ULID", "flag": "Boolean", "meta": "JSON",
		"at": "Timestamp", "raw": "Binary", "ref": "UUID", "ratio": "Float", "small": "SmallInteger",
		"count": "Integer", "body": "Text", "day": "Date", "fixed": "", "search": "",
	}

	if diff := cmp.Diff(want, kinds); diff != "" {
		t.Errorf("kinds (-want +got):\n%s", diff)
	}

	title, price := posts.Columns[1], posts.Columns[2]
	if title.Length != 120 || title.Nullable || price.Precision != 10 || price.Scale != 2 || price.Default.V != "0" {
		t.Errorf("title = %+v, price = %+v", title, price)
	}

	if !posts.Columns[15].Generated {
		t.Error("the generated column is not marked")
	}

	if diff := cmp.Diff([]string{"id"}, posts.PrimaryKey); diff != "" {
		t.Errorf("primary key (-want +got):\n%s", diff)
	}

	wantIdx := []IndexInfo{
		{Name: "posts_title_at_idx", Columns: []string{"title", "at"}},
		{Name: "posts_slug_key", Columns: []string{"code"}, Unique: true, Where: "(flag)"},
		{Name: "posts_lower_idx", Columns: []string{"title"}, Expression: true},
	}

	if diff := cmp.Diff(wantIdx, posts.Indexes); diff != "" {
		t.Errorf("indexes (-want +got):\n%s", diff)
	}

	wantFK := []ForeignKeyInfo{
		{Columns: []string{"ref"}, RefTable: "users", RefColumns: []string{"id"}, OnDelete: "CASCADE"},
		{Columns: []string{"title", "code"}, RefTable: "pairs", RefColumns: []string{"a", "b"}, OnDelete: "SET NULL"},
	}

	if diff := cmp.Diff(wantFK, posts.ForeignKeys); diff != "" {
		t.Errorf("foreign keys (-want +got):\n%s", diff)
	}

	// The table name is a bound parameter of every per-table query.
	for i, q := range cat.queries {
		if strings.Contains(q, "relname = $1") && (len(cat.args[i]) != 1 || cat.args[i][0].Value != "posts") {
			t.Errorf("query %d binds %v, want the table name", i, cat.args[i])
		}
	}
}

func TestInspectMySQLMapsTheCatalog(t *testing.T) {
	t.Parallel()

	ex, _ := newFakeExec(t, database.MySQL,
		fakeAnswer{match: "COUNT(*)", cols: []string{"n"}, rows: [][]driver.Value{v(int64(1))}},
		fakeAnswer{match: "SELECT table_name", cols: []string{"n"}, rows: [][]driver.Value{v("orders")}},
		fakeAnswer{
			match: "information_schema.columns",
			cols:  []string{"name", "type", "nullable", "default", "extra", "key"},
			rows: [][]driver.Value{
				v("id", "bigint(20)", false, nil, "auto_increment", "PRI"),
				v("ref", "char(36)", false, nil, "", ""),
				v("code", "char(26)", true, nil, "", ""),
				v("name", "varchar(255)", false, nil, "", ""),
				v("total", "decimal(10,2)", false, "0.00", "", ""),
				v("paid", "tinyint(1)", false, "0", "", ""),
				v("at", "datetime(6)", true, nil, "", ""),
				v("doc", "json", true, nil, "", ""),
				v("notes", "longtext", true, nil, "", ""),
				v("blob", "longblob", true, nil, "", ""),
				v("ratio", "double", true, nil, "", ""),
				v("qty", "int(11)", false, nil, "", ""),
				v("small", "smallint(6)", true, nil, "", ""),
				v("day", "date", true, nil, "", ""),
				v("shape", "geometry", true, nil, "", ""),
				v("double_total", "int", true, nil, "VIRTUAL GENERATED", ""),
				v("big", "bigint", false, nil, "", ""),
			},
		},
		fakeAnswer{
			match: "information_schema.statistics",
			cols:  []string{"index", "non_unique", "col"},
			rows: [][]driver.Value{
				v("PRIMARY", int64(0), "id"),
				v("idx_name_at", int64(1), "name"),
				v("idx_name_at", int64(1), "at"),
				v("uq_code", int64(0), "code"),
				v("fn_idx", int64(1), nil),
			},
		},
		fakeAnswer{
			match: "key_column_usage",
			cols:  []string{"constraint", "col", "ref", "refcol", "rule"},
			rows: [][]driver.Value{
				v("fk_a", "ref", "users", "uuid", "cascade"),
				v("fk_b", "name", "pairs", "a", "set null"),
				v("fk_b", "code", "pairs", "b", "set null"),
			},
		},
	)

	tbl, ok, err := InspectTable(t.Context(), ex, "orders")
	if err != nil || !ok {
		t.Fatalf("InspectTable = %v, %v", ok, err)
	}

	kinds := map[string]string{}
	for _, c := range tbl.Columns {
		kinds[c.Name] = c.Kind
	}

	want := map[string]string{
		"id": "ID", "ref": "UUID", "code": "ULID", "name": "String", "total": "Decimal", "paid": "Boolean",
		"at": "Timestamp", "doc": "JSON", "notes": "Text", "blob": "Binary", "ratio": "Float", "qty": "Integer",
		"small": "SmallInteger", "day": "Date", "shape": "", "double_total": "Integer", "big": "BigInteger",
	}

	if diff := cmp.Diff(want, kinds); diff != "" {
		t.Errorf("kinds (-want +got):\n%s", diff)
	}

	if !tbl.Columns[15].Generated || tbl.Columns[0].Type != "bigint(20)" && tbl.Columns[0].Type != "bigint" {
		t.Errorf("columns = %+v", tbl.Columns)
	}

	if diff := cmp.Diff([]string{"id"}, tbl.PrimaryKey); diff != "" {
		t.Errorf("primary key (-want +got):\n%s", diff)
	}

	wantIdx := []IndexInfo{
		{Name: "idx_name_at", Columns: []string{"name", "at"}},
		{Name: "uq_code", Columns: []string{"code"}, Unique: true},
		{Name: "fn_idx", Expression: true},
	}

	if diff := cmp.Diff(wantIdx, tbl.Indexes); diff != "" {
		t.Errorf("indexes (-want +got):\n%s", diff)
	}

	wantFK := []ForeignKeyInfo{
		{Columns: []string{"ref"}, RefTable: "users", RefColumns: []string{"uuid"}, OnDelete: "CASCADE"},
		{Columns: []string{"name", "code"}, RefTable: "pairs", RefColumns: []string{"a", "b"}, OnDelete: "SET NULL"},
	}

	if diff := cmp.Diff(wantFK, tbl.ForeignKeys); diff != "" {
		t.Errorf("foreign keys (-want +got):\n%s", diff)
	}
}

func TestInspectReportsCatalogErrors(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")

	tests := []struct {
		name    string
		d       database.Dialect
		answers []fakeAnswer
		want    string
	}{
		{"listing postgres tables", database.Postgres, []fakeAnswer{{match: "c.relkind", err: boom}}, "list tables"},
		{"listing mysql tables", database.MySQL, []fakeAnswer{{match: "SELECT table_name", err: boom}}, "list tables"},
		{"postgres columns", database.Postgres, []fakeAnswer{
			{match: "c.relkind", cols: []string{"n"}, rows: [][]driver.Value{v("t")}},
			{match: "FROM pg_attribute a", err: boom},
		}, "inspect t"},
		{"postgres indexes", database.Postgres, []fakeAnswer{
			{match: "c.relkind", cols: []string{"n"}, rows: [][]driver.Value{v("t")}},
			{match: "FROM pg_attribute a", cols: []string{"a", "b", "c", "d", "e", "f"}},
			{match: "FROM pg_index ix", err: boom},
		}, "inspect t"},
		{"postgres foreign keys", database.Postgres, []fakeAnswer{
			{match: "c.relkind", cols: []string{"n"}, rows: [][]driver.Value{v("t")}},
			{match: "FROM pg_attribute a", cols: []string{"a", "b", "c", "d", "e", "f"}},
			{match: "FROM pg_index ix", cols: []string{"a", "b", "c", "d", "e", "f"}},
			{match: "FROM pg_constraint con", err: boom},
		}, "inspect t"},
		{"mysql columns", database.MySQL, []fakeAnswer{
			{match: "SELECT table_name", cols: []string{"n"}, rows: [][]driver.Value{v("t")}},
			{match: "information_schema.columns", err: boom},
		}, "inspect t"},
		{"mysql statistics", database.MySQL, []fakeAnswer{
			{match: "SELECT table_name", cols: []string{"n"}, rows: [][]driver.Value{v("t")}},
			{match: "information_schema.columns", cols: []string{"a", "b", "c", "d", "e", "f"}},
			{match: "information_schema.statistics", err: boom},
		}, "inspect t"},
		{"a postgres table name that does not scan", database.Postgres, []fakeAnswer{
			{match: "c.relkind", cols: []string{"a", "b"}, rows: [][]driver.Value{v("t", "u")}},
		}, "list tables"},
		{"a mysql table name that does not scan", database.MySQL, []fakeAnswer{
			{match: "SELECT table_name", cols: []string{"a", "b"}, rows: [][]driver.Value{v("t", "u")}},
		}, "list tables"},
		{"a postgres index row that does not scan", database.Postgres, []fakeAnswer{
			{match: "c.relkind", cols: []string{"n"}, rows: [][]driver.Value{v("t")}},
			{match: "FROM pg_attribute a", cols: []string{"a", "b", "c", "d", "e", "f"}},
			{match: "FROM pg_index ix", cols: []string{"a"}, rows: [][]driver.Value{v("x")}},
		}, "inspect t"},
		{"a postgres foreign key row that does not scan", database.Postgres, []fakeAnswer{
			{match: "c.relkind", cols: []string{"n"}, rows: [][]driver.Value{v("t")}},
			{match: "FROM pg_attribute a", cols: []string{"a", "b", "c", "d", "e", "f"}},
			{match: "FROM pg_index ix", cols: []string{"a", "b", "c", "d", "e", "f"}},
			{match: "FROM pg_constraint con", cols: []string{"a"}, rows: [][]driver.Value{v("x")}},
		}, "inspect t"},
		{"a mysql column row that does not scan", database.MySQL, []fakeAnswer{
			{match: "SELECT table_name", cols: []string{"n"}, rows: [][]driver.Value{v("t")}},
			{match: "information_schema.columns", cols: []string{"a"}, rows: [][]driver.Value{v("x")}},
		}, "inspect t"},
		{"a mysql index row that does not scan", database.MySQL, []fakeAnswer{
			{match: "SELECT table_name", cols: []string{"n"}, rows: [][]driver.Value{v("t")}},
			{match: "information_schema.columns", cols: []string{"a", "b", "c", "d", "e", "f"}},
			{match: "information_schema.statistics", cols: []string{"a"}, rows: [][]driver.Value{v("x")}},
		}, "inspect t"},
		{"a mysql foreign key row that does not scan", database.MySQL, []fakeAnswer{
			{match: "SELECT table_name", cols: []string{"n"}, rows: [][]driver.Value{v("t")}},
			{match: "information_schema.columns", cols: []string{"a", "b", "c", "d", "e", "f"}},
			{match: "information_schema.statistics", cols: []string{"a", "b", "c"}},
			{match: "key_column_usage", cols: []string{"a"}, rows: [][]driver.Value{v("x")}},
		}, "inspect t"},
		{"a mysql foreign key query that fails", database.MySQL, []fakeAnswer{
			{match: "SELECT table_name", cols: []string{"n"}, rows: [][]driver.Value{v("t")}},
			{match: "information_schema.columns", cols: []string{"a", "b", "c", "d", "e", "f"}},
			{match: "information_schema.statistics", cols: []string{"a", "b", "c"}},
			{match: "key_column_usage", err: boom},
		}, "inspect t"},
		{"a row that does not scan", database.Postgres, []fakeAnswer{
			{match: "c.relkind", cols: []string{"n"}, rows: [][]driver.Value{v("t")}},
			{
				match: "FROM pg_attribute a", cols: []string{"a", "b", "c", "d", "e", "f"},
				rows: [][]driver.Value{v("x", "text", "not a bool", nil, false, false)},
			},
		}, "inspect t"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ex, _ := newFakeExec(t, tt.d, tt.answers...)

			_, err := Inspect(t.Context(), ex)
			if err == nil || !strings.Contains(err.Error(), tt.want) ||
				(strings.Contains(tt.name, "fails") || strings.Contains(tt.name, "listing")) && !errors.Is(err, boom) {
				t.Errorf("Inspect error = %v, want one naming %q wrapping the cause", err, tt.want)
			}
		})
	}
}

func TestInspectTableRefusesBadNamesAndMissingTables(t *testing.T) {
	t.Parallel()

	ex, _ := newFakeExec(t, database.Postgres,
		fakeAnswer{match: "COUNT(*)", cols: []string{"n"}, rows: [][]driver.Value{v(int64(0))}})

	if _, _, err := InspectTable(t.Context(), ex, "bad name"); err == nil {
		t.Error("InspectTable accepted an invalid table name")
	}

	if _, ok, err := InspectTable(t.Context(), ex, "ghost"); ok || err != nil {
		t.Errorf("InspectTable(missing) = %v, %v; want false, nil", ok, err)
	}

	broken, _ := newFakeExec(t, database.Postgres, fakeAnswer{match: "COUNT(*)", err: errors.New("down")})
	if _, _, err := InspectTable(t.Context(), broken, "t"); err == nil {
		t.Error("InspectTable hid a failing catalog")
	}
}

func TestKindOfAndSplitList(t *testing.T) {
	t.Parallel()

	tests := []struct {
		fam, typ string
		special  bool
		kind     string
		length   int
	}{
		{"sqlite", "integer", true, "ID", 0},
		{"sqlite", "integer", false, "BigInteger", 0},
		{"sqlite", "smallint", false, "SmallInteger", 0},
		{"sqlite", "boolean", false, "Boolean", 0},
		{"sqlite", "text", false, "Text", 0},
		{"sqlite", "real", false, "Float", 0},
		{"sqlite", "date", false, "Date", 0},
		{"sqlite", "timestamp", false, "Timestamp", 0},
		{"sqlite", "blob", false, "Binary", 0},
		{"sqlite", "varchar(40)", false, "String", 40},
		{"sqlite", "char(26)", false, "ULID", 26},
		{"sqlite", "char(36)", false, "", 0},
		{"mysql", "char(36)", false, "UUID", 0},
		{"mysql", "int(10)", false, "Integer", 0},
		{"mysql", "bigint(20)", true, "ID", 0},
		{"postgres", "bigint", true, "ID", 0},
		{"postgres", "bigint", false, "BigInteger", 0},
		{"postgres", "character varying(9)", false, "String", 9},
		{"postgres", "interval", false, "", 0},
		{"mysql", "enum('a','b')", false, "", 0},
	}

	for _, tt := range tests {
		kind, length, _, _ := kindOf(tt.fam, tt.typ, tt.special)
		if kind != tt.kind || length != tt.length {
			t.Errorf(
				"kindOf(%s, %q, %v) = %q, %d; want %q, %d",
				tt.fam,
				tt.typ,
				tt.special,
				kind,
				length,
				tt.kind,
				tt.length,
			)
		}
	}

	if got := splitList(""); got != nil {
		t.Errorf("splitList(\"\") = %v, want nil", got)
	}

	if diff := cmp.Diff([]string{"a", "b"}, splitList("a,b")); diff != "" {
		t.Errorf("splitList (-want +got):\n%s", diff)
	}
}

// sqliteCatalog answers the SQLite catalog queries of one table, the way an
// engine does whose pragma_table_xinfo reports a declared type without its
// size (Turso: "VARCHAR" for VARCHAR(40)) while sqlite_master keeps the
// CREATE TABLE text.
func sqliteCatalog(t *testing.T, createSQL string, cols ...[]driver.Value) fakeExec {
	t.Helper()

	ex, _ := newFakeExec(t, database.SQLite,
		fakeAnswer{
			match: "INSTR(LOWER(sql), 'autoincrement')",
			cols:  []string{"autoinc", "check", "sql"},
			rows:  [][]driver.Value{v(false, false, createSQL)},
		},
		fakeAnswer{match: "COUNT(*)", cols: []string{"n"}, rows: [][]driver.Value{v(int64(1))}},
		fakeAnswer{
			match: "pragma_table_xinfo",
			cols:  []string{"name", "type", "notnull", "dflt", "pk", "hidden"}, rows: cols,
		},
		fakeAnswer{match: "type = 'trigger'", cols: []string{"name"}},
		fakeAnswer{match: "pragma_index_list", cols: []string{"name", "unique", "origin", "partial"}},
		fakeAnswer{match: "pragma_foreign_key_list", cols: []string{"id", "table", "from", "to", "on_delete"}},
	)

	return ex
}

func TestInspectSQLiteRecoversTheDeclaredSizeFromTheCreateTable(t *testing.T) {
	t.Parallel()

	create := "CREATE TABLE \"t\" (\n" +
		"  id INTEGER PRIMARY KEY,\n" +
		"  `label` VARCHAR (40) NOT NULL DEFAULT ('a,b'),\n" +
		"  [price] NUMERIC (10, 2) CHECK (price > (1 + 2)),\n" +
		"  \"code\" CHAR (26),\n" +
		"  note TEXT DEFAULT 'x(y',\n" +
		"  CONSTRAINT note_ck CHECK (length(note) < 10),\n" +
		"  label2 VARCHAR(7)\n)"

	// The pragma drops the size; label2 already carries it.
	ex := sqliteCatalog(t, create,
		v("id", "INTEGER", int64(1), nil, int64(1), int64(0)),
		v("label", "VARCHAR", int64(1), nil, int64(0), int64(0)),
		v("price", "NUMERIC", int64(0), nil, int64(0), int64(0)),
		v("code", "CHAR", int64(0), nil, int64(0), int64(0)),
		v("note", "TEXT", int64(0), nil, int64(0), int64(0)),
		v("label2", "VARCHAR(7)", int64(0), nil, int64(0), int64(0)),
	)

	tbl, ok, err := InspectTable(t.Context(), ex, "t")
	if err != nil || !ok {
		t.Fatalf("InspectTable = %v, %v", ok, err)
	}

	type got struct {
		typ, kind        string
		length, prec, sc int
	}

	have := map[string]got{}
	for _, c := range tbl.Columns {
		have[c.Name] = got{c.Type, c.Kind, c.Length, c.Precision, c.Scale}
	}

	want := map[string]got{
		"id":     {"integer", "BigInteger", 0, 0, 0},
		"label":  {"varchar(40)", "String", 40, 0, 0},
		"price":  {"numeric(10,2)", "Decimal", 0, 10, 2},
		"code":   {"char(26)", "ULID", 26, 0, 0},
		"note":   {"text", "Text", 0, 0, 0},
		"label2": {"varchar(7)", "String", 7, 0, 0},
	}

	for name, w := range want {
		if have[name] != w {
			t.Errorf("%s = %+v, want %+v", name, have[name], w)
		}
	}
}

func TestInspectSQLiteKeepsThePragmaTypeWhenTheColumnIsNotInTheSQL(t *testing.T) {
	t.Parallel()

	for name, create := range map[string]string{
		"a column the text does not mention": "CREATE TABLE t (other TEXT)",
		"a broken statement":                 "CREATE TABLE t (label VARCHAR(40",
		"no statement at all":                "",
		"a view-like text without a list":    "CREATE TABLE t AS SELECT 1",
	} {
		ex := sqliteCatalog(t, create, v("label", "VARCHAR", int64(0), nil, int64(0), int64(0)))

		tbl, _, err := InspectTable(t.Context(), ex, "t")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		if c := tbl.Columns[0]; c.Type != "varchar" || c.Kind != "" || c.Length != 0 {
			t.Errorf("%s: column = %+v, want the pragma type unchanged", name, c)
		}
	}
}

// Turso names an expression in pragma_index_info instead of returning NULL,
// so a name that is not a column of the table is an expression.
func TestInspectSQLiteTakesAnIndexedNameThatIsNotAColumnForAnExpression(t *testing.T) {
	t.Parallel()

	ex, _ := newFakeExec(t, database.SQLite,
		fakeAnswer{
			match: "INSTR(LOWER(sql), 'autoincrement')",
			cols:  []string{"autoinc", "check", "sql"},
			rows:  [][]driver.Value{v(false, false, "CREATE TABLE t (id INTEGER, label TEXT)")},
		},
		fakeAnswer{match: "COUNT(*)", cols: []string{"n"}, rows: [][]driver.Value{v(int64(1))}},
		fakeAnswer{
			match: "pragma_table_xinfo",
			cols:  []string{"name", "type", "notnull", "dflt", "pk", "hidden"},
			rows: [][]driver.Value{
				v("id", "INTEGER", int64(0), nil, int64(0), int64(0)),
				v("label", "TEXT", int64(0), nil, int64(0), int64(0)),
			},
		},
		fakeAnswer{match: "type = 'trigger'", cols: []string{"name"}},
		fakeAnswer{
			match: "pragma_index_list",
			cols:  []string{"name", "unique", "origin", "partial"},
			rows:  [][]driver.Value{v("t_expr", int64(0), "c", int64(0)), v("t_plain", int64(0), "c", int64(0))},
		},
		fakeAnswer{
			match: "pragma_index_info",
			cols:  []string{"name"},
			rows:  [][]driver.Value{v("lower (label)"), v("id")},
		},
		fakeAnswer{match: "pragma_foreign_key_list", cols: []string{"id", "table", "from", "to", "on_delete"}},
	)

	tbl, _, err := InspectTable(t.Context(), ex, "t")
	if err != nil {
		t.Fatal(err)
	}

	for _, ix := range tbl.Indexes {
		if !ix.Expression || len(ix.Columns) != 1 || ix.Columns[0] != "id" {
			t.Errorf("index %s = %+v, want an expression index with only the plain column id", ix.Name, ix)
		}
	}
}
