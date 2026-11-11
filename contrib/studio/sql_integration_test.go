//go:build integration

package studio

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/drivers/mysql"
	"github.com/AlexGalhardo/vukem/drivers/postgres"
	"github.com/AlexGalhardo/vukem/schema"
)

// The SQL explorer runs against the real servers of task services:up. The
// URLs follow drivers/postgres and drivers/mysql: POSTGRES_URL, MYSQL_URL and
// MARIADB_URL, by default the compose services.

type itServer struct {
	name, url string
	driver    database.Option
}

func itServers() []itServer {
	env := func(key, def string) string {
		if v := os.Getenv(key); v != "" {
			return v
		}

		return def
	}

	return []itServer{
		{
			"PostgreSQL",
			env("POSTGRES_URL", "postgres://vukem:vukem@localhost:55432/vukem_test?sslmode=disable"),
			database.WithDriver(postgres.Driver()),
		},
		{
			"MySQL",
			env("MYSQL_URL", "mysql://vukem:vukem@localhost:53306/vukem_test"),
			database.WithDriver(mysql.Driver()),
		},
		{
			"MariaDB",
			env("MARIADB_URL", "mariadb://vukem:vukem@localhost:53307/vukem_test"),
			database.WithDriver(mysql.Driver()),
		},
	}
}

// itEnv is a database with two fresh tables, named apart from every other
// run so that concurrent suites do not meet.
type itEnv struct {
	src            *SQLDB
	db             *database.DB
	fam            string
	items, pairs   string
	suffix         string
	extraToCleanup []string
}

// eachServer runs fn against PostgreSQL, MySQL and MariaDB.
func eachServer(t *testing.T, fn func(t *testing.T, e *itEnv)) {
	t.Helper()

	for _, srv := range itServers() {
		t.Run(srv.name, func(t *testing.T) {
			db, err := database.Open(t.Context(), srv.url, srv.driver)
			if err != nil {
				t.Fatalf("connect: %v (run task services:up)", err)
			}

			t.Cleanup(func() { _ = db.Close() })

			suffix := strings.ToLower(rand.Text()[:8])
			e := &itEnv{
				src:    NewSQL("db", srv.name, db, schemaCatalog{db}),
				db:     db,
				fam:    database.MustCapabilities(db.Dialect()).Family,
				items:  "sit_items_" + suffix,
				pairs:  "sit_pairs_" + suffix,
				suffix: suffix,
			}

			e.setup(t)
			fn(t, e)
		})
	}
}

func (e *itEnv) exec(t *testing.T, q string) {
	t.Helper()

	if _, err := e.db.ExecContext(t.Context(), q); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func (e *itEnv) setup(t *testing.T) {
	t.Helper()

	s := schema.New(t.Context(), e.db)

	t.Cleanup(func() {
		// Children first: tables a test added that reference the others.
		for _, n := range slices.Backward(e.extraToCleanup) {
			_ = schema.New(context.Background(), e.db).DropIfExists(n)
		}

		_ = schema.New(context.Background(), e.db).DropIfExists(e.pairs)
		_ = schema.New(context.Background(), e.db).DropIfExists(e.items)
	})

	if err := s.Create(e.items, func(t *schema.Table) {
		t.Integer("id")
		t.String("name")
		t.Text("bio").Nullable()
		t.Integer("n")
		t.Primary("id")
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.Create(e.pairs, func(t *schema.Table) {
		t.Integer("a")
		t.Integer("b")
		t.String("v")
		t.Primary("a", "b")
	}); err != nil {
		t.Fatal(err)
	}

	e.exec(t, `INSERT INTO `+e.items+` (id, name, bio, n) VALUES
		(1, 'ada', NULL, 1),
		(2, 'linus', '50%_off', 10),
		(3, 'grace', '50xxoff', 21),
		(4, 'a!b', '<b>bold</b>', 100),
		(5, 'under_score', 'x', 5),
		(6, 'underXscore', 'y', 6),
		(7, 'pct%sign', 'z', 7),
		(8, 'pctXsign', 'w', 8)`)
	e.exec(t, `INSERT INTO `+e.pairs+` (a, b, v) VALUES
		(1, 1, 'a'), (1, 2, 'b'), (1, 3, 'c'), (2, 1, 'd'), (2, 2, 'e'), (3, 1, 'f'), (3, 2, 'g')`)
}

func (e *itEnv) count(t *testing.T, table string) int {
	t.Helper()

	var n int
	if err := e.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
		t.Fatal(err)
	}

	return n
}

// ids returns the cells of column col of every row, as text.
func cellsOf(p RowPage, col int) []string {
	out := make([]string, len(p.Rows))
	for i, r := range p.Rows {
		out[i] = r[col].Text
	}

	return out
}

func TestIntegrationGridLikeFilterEscapesWildcards(t *testing.T) {
	eachServer(t, func(t *testing.T, e *itEnv) {
		t.Helper()

		tests := []struct {
			name string
			f    Filter
			want []string // ids
		}{
			{"percent and underscore are literal in contains", Filter{"bio", "contains", "%_"}, []string{"2"}},
			{"percent is literal in starts", Filter{"bio", "starts", "50%"}, []string{"2"}},
			{"a bang is literal", Filter{"name", "contains", "!"}, []string{"4"}},
			{"an underscore does not match any character", Filter{"name", "contains", "_"}, []string{"5"}},
			{"underscore in starts", Filter{"name", "starts", "under_"}, []string{"5"}},
			{"a percent is not a wildcard", Filter{"name", "contains", "%"}, []string{"7"}},
			{"the escape character itself", Filter{"name", "contains", "a!"}, []string{"4"}},
			// PostgreSQL has no LIKE on integers: the grid casts to text.
			{"a number column is searched as text", Filter{"n", "contains", "1"}, []string{"1", "2", "3", "4"}},
			{"a number column, starts", Filter{"n", "starts", "1"}, []string{"1", "2", "4"}},
			{"equality binds the value", Filter{"name", "eq", "ada"}, []string{"1"}},
			{"null", Filter{"bio", "null", ""}, []string{"1"}},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				page, err := e.src.Rows(t.Context(), RowQuery{Table: e.items, Filters: []Filter{tt.f}})
				if err != nil {
					t.Fatalf("Rows: %v", err)
				}

				if diff := cmp.Diff(tt.want, cellsOf(page, 0)); diff != "" {
					t.Errorf("ids (-want +got):\n%s", diff)
				}
			})
		}
	})
}

func TestIntegrationGridSortAndPaging(t *testing.T) {
	eachServer(t, func(t *testing.T, e *itEnv) {
		t.Helper()

		// n ascending: 1(1) 5(5) 6(6) 7(7) 8(8) 2(10) 3(21) 4(100).
		asc, err := e.src.Rows(t.Context(), RowQuery{Table: e.items, Sort: "n", PerPage: 3, Page: 2})
		if err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff([]string{"7", "8", "2"}, cellsOf(asc, 0)); diff != "" || !asc.HasMore || asc.Next != nil {
			t.Errorf("sort n page 2: ids %v more %v next %v\n%s", cellsOf(asc, 0), asc.HasMore, asc.Next, diff)
		}

		desc, err := e.src.Rows(t.Context(), RowQuery{Table: e.items, Sort: "n", Desc: true, PerPage: 3})
		if err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff([]string{"4", "3", "2"}, cellsOf(desc, 0)); diff != "" {
			t.Errorf("sort n desc (-want +got):\n%s", diff)
		}

		// A filter and a sort together.
		both, err := e.src.Rows(t.Context(), RowQuery{
			Table: e.items, Sort: "n", Desc: true, Filters: []Filter{{"n", "gt", "5"}, {"name", "contains", "p"}},
		})
		if err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff([]string{"8", "7"}, cellsOf(both, 0)); diff != "" {
			t.Errorf("filtered sort (-want +got):\n%s", diff)
		}
	})
}

// walk follows the keyset cursor of a table to its end.
func walk(t *testing.T, e *itEnv, q RowQuery, cols int) [][]string {
	t.Helper()

	var out [][]string

	for range 20 {
		page, err := e.src.Rows(t.Context(), q)
		if err != nil {
			t.Fatalf("Rows(after=%v): %v", q.After, err)
		}

		for _, r := range page.Rows {
			row := make([]string, cols)
			for i := range cols {
				row[i] = r[i].Text
			}

			out = append(out, row)
		}

		if !page.HasMore {
			if page.Next != nil {
				t.Errorf("the last page has a cursor: %v", page.Next)
			}

			return out
		}

		if page.Next == nil {
			t.Fatalf("a page with more rows has no cursor (after=%v)", q.After)
		}

		q.After = page.Next
	}

	t.Fatal("the cursor never ended")

	return nil
}

func TestIntegrationGridKeysetOnASingleAndACompositeKey(t *testing.T) {
	eachServer(t, func(t *testing.T, e *itEnv) {
		t.Helper()

		singles := walk(t, e, RowQuery{Table: e.items, PerPage: 3}, 1)
		if diff := cmp.Diff([][]string{{"1"}, {"2"}, {"3"}, {"4"}, {"5"}, {"6"}, {"7"}, {"8"}}, singles); diff != "" {
			t.Errorf("single key, ascending (-want +got):\n%s", diff)
		}

		pairs := walk(t, e, RowQuery{Table: e.pairs, PerPage: 2}, 2)
		want := [][]string{{"1", "1"}, {"1", "2"}, {"1", "3"}, {"2", "1"}, {"2", "2"}, {"3", "1"}, {"3", "2"}}

		if diff := cmp.Diff(want, pairs); diff != "" {
			t.Errorf("composite key, ascending (-want +got):\n%s", diff)
		}

		down := walk(t, e, RowQuery{Table: e.pairs, PerPage: 2, Desc: true}, 2)

		slices.Reverse(want)

		if diff := cmp.Diff(want, down); diff != "" {
			t.Errorf("composite key, descending (-want +got):\n%s", diff)
		}

		// The cursor of a page whose first column ties with the next one:
		// (1,3) is the last row of page 2 and (2,1) must come next.
		page, err := e.src.Rows(t.Context(), RowQuery{Table: e.pairs, PerPage: 2, After: []string{"1", "2"}})
		if err != nil {
			t.Fatal(err)
		}

		if got := cellsOf(page, 1); !slices.Equal(got, []string{"3", "1"}) {
			t.Errorf("after (1,2): b column = %v, want [3 1]", got)
		}

		// Rows(sort=...) on a composite key pages by offset and keeps the
		// key as tiebreaker.
		byV, err := e.src.Rows(t.Context(), RowQuery{Table: e.pairs, Sort: "v", Desc: true, PerPage: 3})
		if err != nil {
			t.Fatal(err)
		}

		if got := cellsOf(byV, 2); !slices.Equal(got, []string{"g", "f", "e"}) {
			t.Errorf("sort v desc = %v", got)
		}
	})
}

func TestIntegrationRowByCompositeKey(t *testing.T) {
	eachServer(t, func(t *testing.T, e *itEnv) {
		t.Helper()

		d, err := e.src.Row(t.Context(), e.pairs, []string{"2", "2"})
		if err != nil {
			t.Fatal(err)
		}

		if d.Cells[2].Text != "e" {
			t.Errorf("row (2,2) = %+v", d.Cells)
		}

		if _, err := e.src.Row(t.Context(), e.pairs, []string{"9", "9"}); !errors.Is(err, ErrNotFound) {
			t.Errorf("a missing row = %v, want ErrNotFound", err)
		}
	})
}

func TestIntegrationCatalogThroughSchemaInspect(t *testing.T) {
	eachServer(t, func(t *testing.T, e *itEnv) {
		t.Helper()

		tbl, err := e.src.Table(t.Context(), e.pairs)
		if err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff([]string{"a", "b"}, tbl.PrimaryKey); diff != "" {
			t.Errorf("primary key (-want +got):\n%s", diff)
		}

		items, err := e.src.Table(t.Context(), e.items)
		if err != nil {
			t.Fatal(err)
		}

		var bio Column

		for _, c := range items.Columns {
			if c.Name == "bio" {
				bio = c
			}
		}

		if !bio.Nullable || bio.Type == "" {
			t.Errorf("bio = %+v, want a nullable column with a type", bio)
		}

		if _, err := e.src.Table(t.Context(), "sit_absent_"+e.suffix); !errors.Is(err, ErrNotFound) {
			t.Errorf("an unknown table = %v, want ErrNotFound", err)
		}
	})
}

// refused runs st on the read-only console and checks it did not run: the
// verb check refuses it (an input error) before it reaches the server.
func refused(t *testing.T, e *itEnv, st string) {
	t.Helper()

	_, err := e.src.Query(t.Context(), st, false)
	if !errors.Is(err, errInput) {
		t.Errorf("the read-only console did not refuse %q before running it: %v", st, err)
	}
}

// refusedByServer runs st past the verb check, on the read-only barrier of the
// dialect alone, and checks the database refused it with the masked error.
func refusedByServer(t *testing.T, e *itEnv, st string) {
	t.Helper()

	_, err := e.src.exec(t.Context(), st, false)
	if err == nil {
		t.Errorf("the read-only transaction ran %q", st)
		return
	}

	if _, ok := errors.AsType[*queryError](err); !ok {
		t.Errorf("%q: error = %v, want a queryError", st, err)
	}
}

func TestIntegrationConsoleIsReadOnly(t *testing.T) {
	eachServer(t, func(t *testing.T, e *itEnv) {
		t.Helper()

		statements := []string{
			"INSERT INTO " + e.items + " (id, name, n) VALUES (900, 'w', 1)",
			"UPDATE " + e.items + " SET name = 'changed'",
			"DELETE FROM " + e.items,
		}

		if e.fam == "postgres" {
			// It starts with WITH, so only the read-only transaction can stop it.
			st := "WITH d AS (DELETE FROM " + e.items + " RETURNING id) SELECT * FROM d"
			if _, err := e.src.Query(t.Context(), st, false); err == nil {
				t.Errorf("the read-only console ran %q", st)
			}
		}

		for _, st := range statements {
			refused(t, e, st)
			refusedByServer(t, e, st)
		}

		if n := e.count(t, e.items); n != 8 {
			t.Errorf("items has %d rows, want 8", n)
		}

		rs, err := e.src.Query(t.Context(), "SELECT COUNT(*) FROM "+e.items, false)
		if err != nil || len(rs.Rows) != 1 || rs.Rows[0][0].Text != "8" {
			t.Errorf("SELECT COUNT(*) = %+v, %v", rs, err)
		}
	})
}

// MySQL and MariaDB commit implicitly before a DDL statement, which ends the
// read-only transaction the console opened, so the read-only console refuses
// everything that is not a read before it reaches the server.
func TestIntegrationConsoleIsReadOnlyForDDL(t *testing.T) {
	eachServer(t, func(t *testing.T, e *itEnv) {
		t.Helper()

		ddl := "sit_ddl_" + e.suffix

		e.extraToCleanup = append(e.extraToCleanup, ddl)

		// DROP last: the statements before it must fail on their own account.
		for _, st := range []string{
			"CREATE TABLE " + ddl + " (id INTEGER)",
			"ALTER TABLE " + e.items + " ADD COLUMN extra INTEGER",
			"TRUNCATE TABLE " + e.pairs,
			"DROP TABLE " + e.pairs,
		} {
			refused(t, e, st)
		}

		if n := e.count(t, e.pairs); n != 7 {
			t.Errorf("pairs has %d rows, want 7", n)
		}

		if _, err := e.src.Table(t.Context(), ddl); !errors.Is(err, ErrNotFound) {
			t.Errorf("the read-only console created a table: %v", err)
		}

		tbl, err := e.src.Table(t.Context(), e.items)
		if err != nil {
			t.Fatal(err)
		}

		if slices.ContainsFunc(tbl.Columns, func(c Column) bool { return c.Name == "extra" }) {
			t.Error("the read-only console altered a table")
		}
	})
}

func TestIntegrationConsoleWriteCommitsAndRollsBack(t *testing.T) {
	eachServer(t, func(t *testing.T, e *itEnv) {
		t.Helper()

		// A write commits: another connection sees it.
		if _, err := e.src.Query(t.Context(),
			"INSERT INTO "+e.items+" (id, name, n) VALUES (901, 'committed', 1)", true); err != nil {
			t.Fatal(err)
		}

		if n := e.count(t, e.items); n != 9 {
			t.Errorf("after a committed insert: %d rows, want 9", n)
		}

		if _, err := e.src.Query(t.Context(), "UPDATE "+e.items+" SET n = n + 1 WHERE id = 901", true); err != nil {
			t.Fatal(err)
		}

		var n int
		if err := e.db.QueryRowContext(t.Context(), "SELECT n FROM "+e.items+" WHERE id = 901").
			Scan(&n); err != nil ||
			n != 2 {
			t.Errorf("after the update n = %d, %v; want 2", n, err)
		}

		// A statement that fails half-way leaves nothing: the second row has
		// no name, which the column refuses.
		_, err := e.src.Query(t.Context(),
			"INSERT INTO "+e.items+" (id, name, n) VALUES (910, 'half', 1), (911, NULL, 1)", true)
		if err == nil {
			t.Fatal("the failing insert succeeded")
		}

		if n := e.count(t, e.items); n != 9 {
			t.Errorf("a failed write changed the table: %d rows, want 9", n)
		}

		if _, ok := errors.AsType[*queryError](err); !ok {
			t.Errorf("error = %v, want a queryError", err)
		}
	})
}

// A deferred foreign key is checked at COMMIT: the statement succeeds and the
// transaction does not, so the rollback must come from the commit error.
func TestIntegrationConsoleWriteRollsBackWhenTheCommitFails(t *testing.T) {
	eachServer(t, func(t *testing.T, e *itEnv) {
		t.Helper()

		if e.fam != "postgres" {
			t.Skip("deferred constraints are a PostgreSQL feature")
		}

		child := "sit_child_" + e.suffix
		e.extraToCleanup = append(e.extraToCleanup, child)
		e.exec(t, "CREATE TABLE "+child+" (id INTEGER PRIMARY KEY, pid INTEGER, CONSTRAINT "+child+
			"_fk FOREIGN KEY (pid) REFERENCES "+e.items+" (id) DEFERRABLE INITIALLY DEFERRED)")

		_, err := e.src.Query(t.Context(), "INSERT INTO "+child+" (id, pid) VALUES (1, 999999)", true)
		if err == nil {
			t.Fatal("the commit of a violated deferred foreign key succeeded")
		}

		if n := e.count(t, child); n != 0 {
			t.Errorf("the child table has %d rows after a failed commit", n)
		}
	})
}

func itStudio(t *testing.T, e *itEnv, write bool) (*bytes.Buffer, func(url.Values) (int, string)) {
	t.Helper()

	logs := &bytes.Buffer{}

	s, err := New(Config{
		Sources: []Source{e.src}, Version: "t", Write: write, Log: slog.New(slog.NewTextHandler(logs, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}

	c := session(t, s)

	return logs, func(form url.Values) (int, string) { return postConsole(s, c, form) }
}

func TestIntegrationConsoleWriteIsAudited(t *testing.T) {
	eachServer(t, func(t *testing.T, e *itEnv) {
		t.Helper()

		logs, post := itStudio(t, e, true)

		stmt := "UPDATE " + e.items + " SET n = 77 WHERE id = 1"
		if code, body := post(
			url.Values{"sql": {stmt}, "write": {"1"}},
		); code != 200 ||
			strings.Contains(body, "role=\"alert\"") {
			t.Fatalf("write = %d %q", code, body)
		}

		var n int
		if err := e.db.QueryRowContext(t.Context(), "SELECT n FROM "+e.items+" WHERE id = 1").
			Scan(&n); err != nil ||
			n != 77 {
			t.Errorf("the committed update: n = %d, %v", n, err)
		}

		log := logs.String()
		for _, want := range []string{`msg="studio write"`, "action=console", "target=sql", "ok=true", "statement="} {
			if !strings.Contains(log, want) {
				t.Errorf("the audit line lacks %q: %s", want, log)
			}
		}

		logs.Reset()

		bad := "UPDATE " + e.items + " SET name = NULL WHERE id = 1"
		if code, _ := post(url.Values{"sql": {bad}, "write": {"1"}}); code != 200 {
			t.Fatalf("failed write = %d", code)
		}

		if !strings.Contains(logs.String(), "ok=false") {
			t.Errorf("the refused write is not audited as a failure: %s", logs.String())
		}

		var name string
		if err := e.db.QueryRowContext(t.Context(), "SELECT name FROM "+e.items+" WHERE id = 1").
			Scan(&name); err != nil ||
			name != "ada" {
			t.Errorf("a refused write changed the row: %q, %v", name, err)
		}

		// A read-mode statement is not an audited write.
		logs.Reset()
		post(url.Values{"sql": {"SELECT 1"}})

		if strings.Contains(logs.String(), `msg="studio write"`) {
			t.Errorf("a read was audited as a write: %s", logs.String())
		}
	})
}

func TestIntegrationDatabaseErrorsAreMasked(t *testing.T) {
	eachServer(t, func(t *testing.T, e *itEnv) {
		t.Helper()

		logs, post := itStudio(t, e, false)

		missing := "sit_missing_" + e.suffix

		code, body := post(url.Values{"sql": {"SELECT * FROM " + missing}})
		if code != 200 {
			t.Fatalf("console = %d", code)
		}

		_, alert, _ := strings.Cut(body, `role="alert"`)
		if alert == "" {
			t.Fatalf("no alert in the page: %s", body)
		}

		leaks := []string{
			missing, "does not exist", "doesn't exist", "relation", "SQLSTATE", "42P01", "1146",
			"pgx", "pq:", "mysql", "mariadb", "vukem_test",
		}

		for _, leak := range leaks {
			if strings.Contains(strings.ToLower(alert), strings.ToLower(leak)) {
				t.Errorf("the database error reached the client (%q): %s", leak, alert)
			}
		}

		if !strings.Contains(logs.String(), missing) {
			t.Errorf("the cause is not in the Studio log: %s", logs.String())
		}

		// The grid: a value the column's type refuses.
		_, err := e.src.Rows(t.Context(), RowQuery{Table: e.items, Filters: []Filter{{"n", "eq", "not-a-number"}}})
		if err != nil {
			if strings.Contains(err.Error(), "not-a-number") ||
				strings.Contains(strings.ToLower(err.Error()), "invalid input") {
				t.Errorf("a grid error carries the database text: %v", err)
			}

			if _, ok := errors.AsType[*queryError](err); !ok {
				t.Errorf("grid error = %v, want a queryError", err)
			}
		} else if e.fam == "postgres" {
			t.Error("PostgreSQL accepted a text value for an integer column")
		}
	})
}
