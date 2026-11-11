package studio

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/database"
)

// fakeCatalog is an Introspector written by hand: the tables the tests
// create below.
type fakeCatalog []Table

func (f fakeCatalog) Tables(context.Context) ([]Table, error) { return f, nil }

func (f fakeCatalog) Table(_ context.Context, name string) (Table, bool, error) {
	for _, t := range f {
		if t.Name == name {
			return t, true, nil
		}
	}

	return Table{}, false, nil
}

var sqlTables = fakeCatalog{
	{
		Name: "users",
		Columns: []Column{
			{Name: "id", Type: "integer"},
			{Name: "name", Type: "text"},
			{Name: "bio", Type: "text", Nullable: true},
		},
		PrimaryKey: []string{"id"},
	},
	{
		Name:       "pairs",
		Columns:    []Column{{Name: "a", Type: "integer"}, {Name: "b", Type: "integer"}, {Name: "v", Type: "text"}},
		PrimaryKey: []string{"a", "b"},
	},
	{Name: "log", Columns: []Column{{Name: "line", Type: "text"}}},
}

type counter struct{ n atomic.Int64 }

func (c *counter) ObserveQuery(context.Context, database.Query) { c.n.Add(1) }

// sqlHarness is a SQLite file with a few rows, opened through the framework.
type sqlHarness struct {
	src   *SQLDB
	db    *database.DB
	count *counter
}

func newSQLHarness(t *testing.T) *sqlHarness {
	t.Helper()

	count := &counter{}

	db, err := database.Open(t.Context(), "file:"+filepath.Join(t.TempDir(), "studio.db"),
		database.WithQueryObserver(count))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	for _, q := range []string{
		`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL, bio TEXT)`,
		`INSERT INTO users (id, name, bio) VALUES (1,'ada',NULL),(2,'linus','50%_off'),(3,'grace','50xxoff'),
			(4,'a!b','<script>alert(1)</script>'),(5,'edsger','x')`,
		`CREATE TABLE pairs (a INTEGER, b INTEGER, v TEXT, PRIMARY KEY (a, b))`,
		`INSERT INTO pairs VALUES (1,1,'a'),(1,2,'b'),(2,1,'c')`,
		`CREATE TABLE log (line TEXT)`,
		`INSERT INTO log VALUES ('one')`,
	} {
		if _, err := db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}

	count.n.Store(0)

	return &sqlHarness{src: NewSQL("db", "sqlite file", db, sqlTables), db: db, count: count}
}

func (h *sqlHarness) users(t *testing.T) int {
	t.Helper()

	var n int
	if err := h.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		t.Fatal(err)
	}

	return n
}

func TestBuildRows(t *testing.T) {
	t.Parallel()

	users := sqlTables[0]
	pairs := sqlTables[1]

	tests := []struct {
		name  string
		d     database.Dialect
		t     Table
		q     RowQuery
		query string
		args  []any
	}{
		{
			"sqlite first page", database.SQLite, users,
			RowQuery{},
			`SELECT "id", "name", "bio" FROM "users" ORDER BY "id" LIMIT 51`, nil,
		},
		{
			"postgres keyset", database.Postgres, users,
			RowQuery{After: []string{"7"}, PerPage: 10},
			`SELECT "id", "name", "bio" FROM "users" WHERE ("id") > ($1) ORDER BY "id" LIMIT 11`,
			[]any{"7"},
		},
		{
			"mysql composite keyset desc", database.MySQL, pairs,
			RowQuery{After: []string{"1", "2"}, Desc: true, PerPage: 2},
			"SELECT `a`, `b`, `v` FROM `pairs` WHERE (`a`, `b`) < (?, ?) ORDER BY `a` DESC, `b` DESC LIMIT 3",
			[]any{"1", "2"},
		},
		{
			"sort by a column uses offset", database.SQLite, users,
			RowQuery{Sort: "name", Page: 3, PerPage: 20},
			`SELECT "id", "name", "bio" FROM "users" ORDER BY "name", "id" LIMIT 21 OFFSET 40`, nil,
		},
		{
			"page is capped", database.SQLite, users,
			RowQuery{Sort: "id", Page: 99999, PerPage: 9999},
			`SELECT "id", "name", "bio" FROM "users" ORDER BY "id" LIMIT 201 OFFSET 199800`, nil,
		},
		{
			"no primary key is offset", database.SQLite, sqlTables[2],
			RowQuery{Page: 2, PerPage: 5},
			`SELECT "line" FROM "log" LIMIT 6 OFFSET 5`, nil,
		},
		{
			"every operator",
			database.Postgres,
			users,
			RowQuery{Filters: []Filter{
				{"id", "eq", "1"},
				{"id", "ne", "2"},
				{"id", "lt", "3"},
				{"id", "lte", "4"},
				{"id", "gt", "5"},
				{"id", "gte", "6"},
				{"bio", "null", ""},
				{"bio", "notnull", ""},
				{"name", "contains", "a%b"},
				{"name", "starts", "x_"},
			}},
			`SELECT "id", "name", "bio" FROM "users" WHERE "id" = $1 AND "id" <> $2 AND "id" < $3 AND "id" <= $4 AND "id" > $5 ` +
				`AND "id" >= $6 AND "bio" IS NULL AND "bio" IS NOT NULL AND CAST("name" AS TEXT) LIKE $7 ESCAPE '!' ` +
				`AND CAST("name" AS TEXT) LIKE $8 ESCAPE '!' ORDER BY "id" LIMIT 51`,
			[]any{"1", "2", "3", "4", "5", "6", "%a!%b%", "x!_%"},
		},
		{
			"like in mysql", database.MySQL, users,
			RowQuery{Filters: []Filter{{"name", "contains", "!"}}},
			"SELECT `id`, `name`, `bio` FROM `users` WHERE `name` LIKE ? ESCAPE '!' ORDER BY `id` LIMIT 51",
			[]any{"%!!%"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			query, args, err := buildRows(tt.d, tt.t, tt.q)
			if err != nil {
				t.Fatal(err)
			}

			if query != tt.query {
				t.Errorf("query = %s\nwant    %s", query, tt.query)
			}

			if diff := cmp.Diff(tt.args, args); diff != "" {
				t.Errorf("args (-want +got):\n%s", diff)
			}
		})
	}
}

func TestSecurity_A03_StudioRejectsUnknownIdentifiers(t *testing.T) {
	t.Parallel()

	h := newSQLHarness(t)

	tests := []struct {
		name string
		q    RowQuery
		want error
	}{
		{"table", RowQuery{Table: "users; DROP TABLE users"}, ErrNotFound},
		{"quoted table", RowQuery{Table: `"users" --`}, ErrNotFound},
		{"sort", RowQuery{Table: "users", Sort: `"id" OR 1=1`}, errInput},
		{
			"filter column",
			RowQuery{Table: "users", Filters: []Filter{{Column: "id) OR (1=1", Op: "eq", Value: "1"}}},
			errInput,
		},
		{
			"filter operator",
			RowQuery{Table: "users", Filters: []Filter{{Column: "id", Op: "= 1 OR 1=1 --", Value: "1"}}},
			errInput,
		},
		{"cursor", RowQuery{Table: "users", After: []string{"1", "2"}}, errInput},
	}

	for _, tt := range tests {
		if _, err := h.src.Rows(t.Context(), tt.q); !errors.Is(err, tt.want) {
			t.Errorf("%s: err = %v, want %v", tt.name, err, tt.want)
		}
	}

	if _, err := h.src.Row(t.Context(), "users; DROP TABLE users", []string{"1"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("Row of an unknown table = %v", err)
	}

	if n := h.count.n.Load(); n != 0 {
		t.Errorf("%d statements were sent for refused requests, want none", n)
	}

	if h.users(t) != 5 {
		t.Error("the users table changed")
	}
}

func TestSecurity_A03_StudioEscapesLikeWildcards(t *testing.T) {
	t.Parallel()

	h := newSQLHarness(t)

	tests := []struct {
		col, op, val string
		want         []string
	}{
		{"bio", "contains", "50%_off", []string{"2"}},
		{"bio", "starts", "50", []string{"2", "3"}},
		{"bio", "starts", "50%", []string{"2"}},
		{"bio", "starts", "50_", nil},
		{"name", "contains", "!", []string{"4"}},
		{"name", "contains", "a!b", []string{"4"}},
	}

	for _, tt := range tests {
		pg, err := h.src.Rows(t.Context(), RowQuery{Table: "users", Filters: []Filter{{tt.col, tt.op, tt.val}}})
		if err != nil {
			t.Fatalf("%s %s %q: %v", tt.col, tt.op, tt.val, err)
		}

		var ids []string
		for _, r := range pg.Rows {
			ids = append(ids, r[0].Text)
		}

		if diff := cmp.Diff(tt.want, ids); diff != "" {
			t.Errorf("%s %s %q (-want +got):\n%s", tt.col, tt.op, tt.val, diff)
		}
	}
}

func TestRowsKeysetPaging(t *testing.T) {
	t.Parallel()

	h := newSQLHarness(t)

	var got []string

	var after []string

	for range 5 {
		pg, err := h.src.Rows(t.Context(), RowQuery{Table: "users", PerPage: 2, After: after})
		if err != nil {
			t.Fatal(err)
		}

		for _, r := range pg.Rows {
			got = append(got, r[0].Text)
		}

		if !pg.HasMore {
			break
		}

		after = pg.Next
	}

	if diff := cmp.Diff([]string{"1", "2", "3", "4", "5"}, got); diff != "" {
		t.Errorf("ids (-want +got):\n%s", diff)
	}

	pg, err := h.src.Rows(t.Context(), RowQuery{Table: "pairs", PerPage: 1, After: []string{"1", "1"}})
	if err != nil || pg.Rows[0][2].Text != "b" {
		t.Errorf("composite keyset = %+v, %v", pg, err)
	}
}

func TestCellsAreBounded(t *testing.T) {
	t.Parallel()

	if c := cellOf(strings.Repeat("é", 300), gridCellRunes); !c.Truncated || len([]rune(c.Text)) != gridCellRunes {
		t.Errorf("long text cell = %+v", c)
	}

	if c := cellOf([]byte{0xff, 0xfe}, gridCellRunes); !c.Binary || c.Text != "<binary 2 bytes>" {
		t.Errorf("binary cell = %+v", c)
	}

	if c := cellOf(nil, gridCellRunes); !c.Null {
		t.Errorf("nil cell = %+v", c)
	}
}

func TestSecurity_A01_StudioConsoleIsReadOnly(t *testing.T) {
	t.Parallel()

	h := newSQLHarness(t)

	statements := []string{
		`DELETE FROM users`,
		`INSERT INTO users (id, name) VALUES (9, 'x')`,
		`INSERT INTO users (id, name) VALUES (9, 'x') RETURNING id`,
		`DELETE FROM users WHERE id = 1 RETURNING id`,
		`UPDATE users SET name = 'x' RETURNING id`,
		`DROP TABLE users`,
		// DDL: MySQL and MariaDB commit implicitly before it, which would end a READ ONLY transaction.
		`CREATE TABLE made (id INTEGER)`,
		`ALTER TABLE users ADD COLUMN extra INTEGER`,
		`TRUNCATE TABLE users`,
		`/* a read */ DROP TABLE users`,
		`(DROP TABLE users)`,
		`SELECT id INTO made FROM users`,
		`SELECT id FROM users INTO OUTFILE '/tmp/studio'`,
		`VACUUM`,
		`SELECT load_extension('x')`,
		`SELECT LOAD_EXTENSION ('x')`,
		`SELECT readfile('/etc/passwd')`,
		`SELECT "writefile"('/tmp/x', 'y')`,
		`SELECT 1 /* a */ , writefile /* b */ ('/tmp/x', 'y')`,
		`REPLACE INTO users (id, name) VALUES (1, 'x')`,
	}

	for _, q := range statements {
		_, err := h.src.Query(t.Context(), q, false)
		if !errors.Is(err, errInput) {
			t.Errorf("%q = %v, want a refusal before it runs", q, err)
		}
	}

	// The verb check is the first barrier; the read-only connection is the second, and holds alone.
	for _, q := range statements[:6] {
		if _, err := h.src.exec(t.Context(), q, false); err == nil {
			t.Errorf("%q ran on the read-only connection", q)
		}
	}

	if n := h.users(t); n != 5 {
		t.Fatalf("users has %d rows after the read-only console, want 5", n)
	}

	for _, q := range []string{
		`SELECT id, name FROM users WHERE id < 3 ORDER BY id`,
		`WITH x AS (SELECT 1 AS n) SELECT n FROM x`,
		`  -- comment` + "\n" + `SELECT 1 UNION SELECT 2`,
		`EXPLAIN QUERY PLAN SELECT * FROM users`,
		`VALUES (1), (2)`,
		`PRAGMA table_info(users)`,
		`SELECT 'INTO' AS "into"`,
	} {
		if _, err := h.src.Query(t.Context(), q, false); err != nil {
			t.Errorf("%q = %v, want it to run", q, err)
		}
	}

	rs, err := h.src.Query(t.Context(), `SELECT id, name FROM users WHERE id < 3 ORDER BY id`, false)
	if err != nil || len(rs.Rows) != 2 || rs.Columns[1] != "name" || rs.Rows[1][1].Text != "linus" {
		t.Errorf("SELECT = %+v, %v", rs, err)
	}
}

func TestReadOnlyStatement(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		text, family string
		want         bool
	}{
		{"SELECT 1", "mysql", true},
		{"select 1", "postgres", true},
		{"  WITH a AS (SELECT 1) SELECT * FROM a", "mysql", true},
		{"SHOW TABLES", "mysql", true},
		{"EXPLAIN SELECT 1", "postgres", true},
		{"DESCRIBE users", "mysql", true},
		{"DESC users", "mysql", true},
		{"TABLE users", "postgres", true},
		{"VALUES (1)", "postgres", true},
		{"/* c */ SELECT 1 -- c", "mysql", true},
		{"(SELECT 1)", "mysql", true},
		{"SELECT 'into'", "mysql", true},
		{"SELECT `into` FROM t", "mysql", true},
		{"SELECT 1 /* INTO */", "postgres", true},
		{"CREATE TABLE t (id int)", "mysql", false},
		{"alter table t add c int", "mysql", false},
		{"TRUNCATE t", "mysql", false},
		{"DROP TABLE t", "postgres", false},
		{"RENAME TABLE a TO b", "mysql", false},
		{"INSERT INTO t VALUES (1)", "mysql", false},
		{"CALL p()", "mysql", false},
		{"SET @a = 1", "mysql", false},
		{"USE other", "mysql", false},
		{"LOCK TABLES t WRITE", "mysql", false},
		{"GRANT ALL ON *.* TO x", "mysql", false},
		{"SELECT * INTO t2 FROM t", "postgres", false},
		{"SELECT 1 INTO @v", "mysql", false},
		{"SELECT 1 INTO OUTFILE '/x'", "mysql", false},
		{"SELECT 1 /*! INTO t */", "mysql", false},
		{"", "mysql", false},
		{"-- only a comment", "mysql", false},
		{"PRAGMA table_info(t)", "sqlite", true},
		{"PRAGMA table_info(t)", "mysql", false},
		// Functions with a side effect or a server-side read.
		{"SELECT pg_sleep(1)", "postgres", false},
		{"select PG_SLEEP_FOR('1s')", "postgres", false},
		{"SELECT pg_catalog.pg_terminate_backend(1)", "postgres", false},
		{`SELECT "pg_sleep"(1)`, "postgres", false},
		{"SELECT pg_sleep /* c */ (1)", "postgres", false},
		{"SELECT dblink_exec('a', 'b')", "postgres", false},
		{"SELECT pg_advisory_lock(1)", "postgres", false},
		{"SELECT pg_try_advisory_xact_lock(1)", "postgres", false},
		{"SELECT pg_advisory_unlock_all()", "postgres", false},
		{"SELECT set_config('a', 'b', false)", "postgres", false},
		{"SELECT nextval('s')", "postgres", false},
		{"SELECT setval('s', 1)", "postgres", false},
		{"SELECT pg_read_file('/etc/passwd')", "postgres", false},
		{"SELECT lo_import('/etc/passwd')", "postgres", false},
		{"SELECT pg_notify('c', 'p')", "postgres", false},
		{"SELECT txid_current()", "postgres", false},
		{"SELECT SLEEP(5)", "mysql", false},
		{"SELECT sleep (5)", "mysql", false},
		{"SELECT `sleep`(5)", "mysql", false},
		{"SELECT GET_LOCK('a', 1)", "mysql", false},
		{"SELECT BENCHMARK(1000000, MD5('x'))", "mysql", false},
		{"SELECT LOAD_FILE('/etc/passwd')", "mysql", false},
		{"SELECT RELEASE_ALL_LOCKS()", "mysql", false},
		{"SELECT sys_exec('id')", "mysql", false},
		{"SELECT load_extension('x')", "sqlite", false},
		{"SELECT readfile('/etc/passwd')", "sqlite", false},
		{"SELECT writefile('/tmp/x', 'y')", "sqlite", false},
		// A name that is not called is not a call.
		{"SELECT pg_sleep FROM t", "postgres", true},
		{"SELECT 1 AS pg_sleep", "postgres", true},
		{"SELECT 'pg_sleep(1)'", "postgres", true},
		{"SELECT sleep FROM t", "mysql", true},
		{"SELECT `sleep` FROM t", "mysql", true},
		{"SELECT 1 -- sleep(1)", "mysql", true},
		{"SELECT sleep(1)", "postgres", true},
		{"SELECT now(), count(*), lower(x) FROM t", "postgres", true},
	} {
		if got := readOnlyStatement(tt.text, tt.family); got != tt.want {
			t.Errorf("readOnlyStatement(%q, %s) = %v, want %v", tt.text, tt.family, got, tt.want)
		}
	}

	// In MySQL "--1" is arithmetic, not a comment, so the semicolon after it counts.
	if singleStatement("SELECT 1--1; DROP TABLE t", true) {
		t.Error("a MySQL --1 hid the second statement")
	}

	if !singleStatement("SELECT 1 -- ; DROP TABLE t", true) || !singleStatement("SELECT 1--1", true) {
		t.Error("a real MySQL comment, or arithmetic, was refused")
	}
}

func TestSecurity_A01_StudioConsoleRefusesMultipleStatements(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		text      string
		backslash bool
		want      bool
	}{
		{"SELECT 1", false, true},
		{"SELECT 1;", false, true},
		{"SELECT 1 ; -- done", false, true},
		{"SELECT 1; /* done */", false, true},
		{"SELECT ';'", false, true},
		{`SELECT ";"`, false, true},
		{"SELECT 'a'';b'", false, true},
		{"SELECT 1 -- ; DROP TABLE users", false, true},
		{"SELECT $$;$$", false, true},
		{"SELECT $tag$ ; $tag$", false, true},
		{"SELECT $1", false, true},
		{"SELECT 1; SELECT 2", false, false},
		{"SELECT 1; DELETE FROM users", false, false},
		{"SELECT 1;;DELETE FROM users", false, false},
		{"SELECT 1 /* x */ ; /* y */ DELETE FROM users", false, false},
		{"SELECT 'a", false, false},
		{"SELECT /* open", false, false},
		{"SELECT $$ open", false, false},
		{`SELECT '\'; DELETE FROM users; --'`, false, false},
		{`SELECT '\'; DELETE FROM users; --'`, true, true},
		{`SELECT '\'' ; DELETE FROM users`, true, false},
		{"SELECT 1 /*! ; DELETE FROM users */", true, false},
	} {
		if got := singleStatement(tt.text, tt.backslash); got != tt.want {
			t.Errorf("singleStatement(%q, %v) = %v, want %v", tt.text, tt.backslash, got, tt.want)
		}
	}

	h := newSQLHarness(t)

	for _, write := range []bool{false, true} {
		if _, err := h.src.Query(t.Context(), "SELECT 1; DELETE FROM users", write); !errors.Is(err, errInput) {
			t.Errorf("write=%v: err = %v, want errInput", write, err)
		}
	}

	if h.users(t) != 5 {
		t.Error("the second statement ran")
	}
}

func TestSecurity_A01_StudioConsoleRefusesInMemoryReadOnly(t *testing.T) {
	t.Parallel()

	db, err := database.Open(t.Context(), "file::memory:")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	src := NewSQL("mem", "memory", db, sqlTables)
	src.memory = true

	if _, err := src.Query(t.Context(), "SELECT 1", false); !errors.Is(err, errInput) {
		t.Errorf("read-only console on :memory: = %v, want a refusal", err)
	}

	if _, err := src.Query(t.Context(), "SELECT 1", true); err != nil {
		t.Errorf("write mode on :memory: = %v", err)
	}
}

func TestConsoleTruncatesAt1000Rows(t *testing.T) {
	t.Parallel()

	h := newSQLHarness(t)

	rs, err := h.src.Query(t.Context(),
		`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i < 1500) SELECT i FROM n`, false)
	if err != nil {
		t.Fatal(err)
	}

	if len(rs.Rows) != consoleMaxRows || !rs.Truncate {
		t.Errorf("rows = %d, truncate = %v, want %d and true", len(rs.Rows), rs.Truncate, consoleMaxRows)
	}
}

// sqlStudio serves a Studio over the harness database.
func sqlStudio(t *testing.T, write bool) (*sqlHarness, *Studio, *bytes.Buffer, *http.Cookie) {
	t.Helper()

	h, logs := newSQLHarness(t), &bytes.Buffer{}

	s, err := New(Config{
		Sources: []Source{h.src}, Version: "t", Write: write, Log: slog.New(slog.NewTextHandler(logs, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}

	return h, s, logs, session(t, s)
}

func get(s *Studio, c *http.Cookie, target string) (int, string) {
	r := request(http.MethodGet, target)
	r.AddCookie(c)
	rec := serve(s, r)

	return rec.Code, rec.Body.String()
}

func postConsole(s *Studio, c *http.Cookie, form url.Values) (int, string) {
	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:4984/s/db/console", strings.NewReader(form.Encode()))
	r.RemoteAddr = "127.0.0.1:50000"
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(c)
	rec := serve(s, r)

	return rec.Code, rec.Body.String()
}

func TestSQLPages(t *testing.T) {
	t.Parallel()

	_, s, _, c := sqlStudio(t, false)

	if code, body := get(
		s,
		c,
		"/s/db",
	); code != 200 || !strings.Contains(body, "pairs") ||
		!strings.Contains(body, "no primary key") {
		t.Errorf("tables = %d %q", code, body)
	}

	code, body := get(s, c, "/s/db/t/users?per=2")
	if code != 200 || !strings.Contains(body, "linus") || strings.Contains(body, "grace") ||
		!strings.Contains(body, "after=2") {
		t.Errorf("rows page 1 = %d %q", code, body)
	}

	if code, body := get(
		s,
		c,
		"/s/db/t/users?per=2&after=2",
	); code != 200 || !strings.Contains(body, "grace") ||
		strings.Contains(body, "linus") {
		t.Errorf("rows page 2 = %d", code)
	}

	if code, body := get(
		s,
		c,
		"/s/db/t/pairs/r?k=1&k=2",
	); code != 200 ||
		!strings.Contains(body, ">b<") && !strings.Contains(body, "b</pre>") {
		t.Errorf("row detail = %d %q", code, body)
	}

	if code, _ := get(s, c, "/s/db/t/nope"); code != http.StatusNotFound {
		t.Errorf("unknown table = %d, want 404", code)
	}

	if code, _ := get(s, c, "/s/db/t/users/r?k=99"); code != http.StatusNotFound {
		t.Errorf("unknown row = %d, want 404", code)
	}

	if code, _ := get(s, c, "/s/db/t/users?f=bogus:eq:1"); code != http.StatusUnprocessableEntity {
		t.Errorf("unknown filter column = %d, want 422", code)
	}

	if code, body := get(s, c, "/s/db/t/users?f=bio:contains:script"); code != 200 ||
		strings.Contains(body, "<script>alert") || !strings.Contains(body, "&lt;script&gt;") {
		t.Errorf("cell HTML is not escaped: %d %q", code, body)
	}
}

func TestSecurity_A10_StudioHidesDatabaseErrors(t *testing.T) {
	t.Parallel()

	_, s, logs, c := sqlStudio(t, false)

	code, body := postConsole(s, c, url.Values{"sql": {"SELECT * FROM secret_ledger_xyz"}})
	if code != 200 {
		t.Fatalf("console = %d", code)
	}

	// The textarea echoes what the developer typed; the alert is what the server says.
	_, alert, _ := strings.Cut(body, `role="alert"`)

	for _, leak := range []string{"secret_ledger_xyz", "no such table", "SQLITE"} {
		if strings.Contains(alert, leak) {
			t.Errorf("the database error reached the client (%q): %s", leak, body)
		}
	}

	if !strings.Contains(logs.String(), "secret_ledger_xyz") {
		t.Errorf("the cause is not in the log: %s", logs.String())
	}
}

func TestSecurity_A01_StudioConsoleWriteNeedsTheFlag(t *testing.T) {
	t.Parallel()

	h, s, _, c := sqlStudio(t, false)

	// write=1 in the form means nothing when Studio runs without --write.
	_, body := postConsole(s, c, url.Values{"sql": {"DELETE FROM users"}, "write": {"1"}})
	if h.users(t) != 5 || !strings.Contains(body, "role=\"alert\"") {
		t.Errorf("a write ran without --write (users = %d)", h.users(t))
	}

	if strings.Contains(body, "Write mode") {
		t.Error("the page offers write mode without --write")
	}
}

func TestSecurity_A09_StudioConsoleWriteIsAuditedAndAtomic(t *testing.T) {
	t.Parallel()

	h, s, logs, c := sqlStudio(t, true)

	// Without the switch the console stays read-only even with --write.
	postConsole(s, c, url.Values{"sql": {"DELETE FROM users WHERE id = 1"}})

	if h.users(t) != 5 {
		t.Fatal("the console wrote without the write switch")
	}

	postConsole(s, c, url.Values{"sql": {"DELETE FROM users WHERE id = 1 RETURNING id"}, "write": {"1"}})

	if h.users(t) != 4 {
		t.Errorf("write mode did not commit: users = %d", h.users(t))
	}

	if !strings.Contains(logs.String(), `msg="studio write"`) || !strings.Contains(logs.String(), "action=console") {
		t.Errorf("the write was not audited: %s", logs.String())
	}

	// A statement that fails leaves nothing behind.
	postConsole(s, c, url.Values{"sql": {"INSERT INTO users (id, name) VALUES (50, NULL)"}, "write": {"1"}})

	if h.users(t) != 4 {
		t.Errorf("a failed write changed the table: users = %d", h.users(t))
	}

	if !strings.Contains(logs.String(), "ok=false") {
		t.Errorf("the refused write was not audited: %s", logs.String())
	}
}

// TestGridSQL runs the same cases on the three dialects: the filter operators,
// keyset and offset paging and the page-size limits.
func TestGridSQL(t *testing.T) {
	t.Parallel()

	users := sqlTables[0]

	for _, d := range []database.Dialect{database.SQLite, database.Postgres, database.MySQL} {
		q := d.Quote
		ph := func(n int) string { return d.Placeholder(n) }
		sel := "SELECT " + q("id") + ", " + q("name") + ", " + q("bio") + " FROM " + q("users")
		like := func(col string, n int) string {
			if database.MustCapabilities(d).Family == "postgres" {
				col = "CAST(" + col + " AS TEXT)"
			}

			return col + " LIKE " + ph(n) + " ESCAPE '!'"
		}

		tests := []struct {
			name  string
			q     RowQuery
			query string
			args  []any
		}{
			{"no filter", RowQuery{}, sel + " ORDER BY " + q("id") + " LIMIT 51", nil},
			{
				"eq",
				RowQuery{Filters: []Filter{{"name", "eq", "a"}}},
				sel + " WHERE " + q("name") + " = " + ph(1) + " ORDER BY " + q("id") + " LIMIT 51",
				[]any{"a"},
			},
			{
				"ne",
				RowQuery{Filters: []Filter{{"id", "ne", "1"}}},
				sel + " WHERE " + q("id") + " <> " + ph(1) + " ORDER BY " + q("id") + " LIMIT 51",
				[]any{"1"},
			},
			{
				"lt",
				RowQuery{Filters: []Filter{{"id", "lt", "1"}}},
				sel + " WHERE " + q("id") + " < " + ph(1) + " ORDER BY " + q("id") + " LIMIT 51",
				[]any{"1"},
			},
			{
				"lte",
				RowQuery{Filters: []Filter{{"id", "lte", "1"}}},
				sel + " WHERE " + q("id") + " <= " + ph(1) + " ORDER BY " + q("id") + " LIMIT 51",
				[]any{"1"},
			},
			{
				"gt",
				RowQuery{Filters: []Filter{{"id", "gt", "1"}}},
				sel + " WHERE " + q("id") + " > " + ph(1) + " ORDER BY " + q("id") + " LIMIT 51",
				[]any{"1"},
			},
			{
				"gte",
				RowQuery{Filters: []Filter{{"id", "gte", "1"}}},
				sel + " WHERE " + q("id") + " >= " + ph(1) + " ORDER BY " + q("id") + " LIMIT 51",
				[]any{"1"},
			},
			{
				"contains",
				RowQuery{Filters: []Filter{{"name", "contains", "50%_!"}}},
				sel + " WHERE " + like(q("name"), 1) + " ORDER BY " + q("id") + " LIMIT 51",
				[]any{"%50!%!_!!%"},
			},
			{
				"starts",
				RowQuery{Filters: []Filter{{"name", "starts", "a"}}},
				sel + " WHERE " + like(q("name"), 1) + " ORDER BY " + q("id") + " LIMIT 51",
				[]any{"a%"},
			},
			{
				"null",
				RowQuery{Filters: []Filter{{"bio", "null", ""}}},
				sel + " WHERE " + q("bio") + " IS NULL ORDER BY " + q("id") + " LIMIT 51",
				nil,
			},
			{
				"notnull",
				RowQuery{Filters: []Filter{{"bio", "notnull", ""}}},
				sel + " WHERE " + q("bio") + " IS NOT NULL ORDER BY " + q("id") + " LIMIT 51",
				nil,
			},
			{
				"keyset",
				RowQuery{After: []string{"7"}},
				sel + " WHERE (" + q("id") + ") > (" + ph(1) + ") ORDER BY " + q("id") + " LIMIT 51",
				[]any{"7"},
			},
			{
				"keyset desc",
				RowQuery{After: []string{"7"}, Desc: true},
				sel + " WHERE (" + q("id") + ") < (" + ph(1) + ") ORDER BY " + q("id") + " DESC LIMIT 51",
				[]any{"7"},
			},
			{
				"sort by a column",
				RowQuery{Sort: "name", Page: 2},
				sel + " ORDER BY " + q("name") + ", " + q("id") + " LIMIT 51 OFFSET 50",
				nil,
			},
			{"per page 0", RowQuery{PerPage: 0}, sel + " ORDER BY " + q("id") + " LIMIT 51", nil},
			{"per page 50", RowQuery{PerPage: 50}, sel + " ORDER BY " + q("id") + " LIMIT 51", nil},
			{"per page 9999", RowQuery{PerPage: 9999}, sel + " ORDER BY " + q("id") + " LIMIT 201", nil},
		}

		for _, tt := range tests {
			query, args, err := buildRows(d, users, tt.q)
			if err != nil {
				t.Fatalf("%s/%s: %v", database.MustCapabilities(d).Family, tt.name, err)
			}

			if query != tt.query {
				t.Errorf("%s/%s:\n got  %s\n want %s", database.MustCapabilities(d).Family, tt.name, query, tt.query)
			}

			if diff := cmp.Diff(tt.args, args); diff != "" {
				t.Errorf("%s/%s args (-want +got):\n%s", database.MustCapabilities(d).Family, tt.name, diff)
			}
		}
	}
}

// TestSecurity_A03_StudioEscapesCellHTML: a value, a column name and a console
// result that hold HTML are shown as text on every page that prints them.
func TestSecurity_A03_StudioEscapesCellHTML(t *testing.T) {
	t.Parallel()

	_, s, _, c := sqlStudio(t, false)

	const payload = "<script>alert(1)</script>"

	check := func(name, body string) {
		t.Helper()

		if strings.Contains(body, payload) || !strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt;") {
			t.Errorf("%s does not escape the HTML: %q", name, body)
		}
	}

	// users.bio of row 4 holds the payload.
	_, body := get(s, c, "/s/db/t/users?f=bio:contains:alert")
	check("the grid", body)

	_, body = get(s, c, "/s/db/t/users/r?k=4")
	check("the row page", body)

	code, body := postConsole(
		s,
		c,
		url.Values{"sql": {"SELECT bio, 1 AS \"<script>alert(1)</script>\" FROM users WHERE id = 4"}},
	)
	if code != 200 {
		t.Fatalf("console = %d", code)
	}

	check("the console result", body)
}
