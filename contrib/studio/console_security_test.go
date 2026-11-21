package studio

import (
	"net/http"
	"os"
	"strings"
	"testing"
)

// disguisedWrites are statements that change something, or reach for something a read must not, behind a
// harmless look. Each must be refused (422) before it reaches the database, in a console that is read-only.
var disguisedWrites = map[string]string{ //nolint:gochecknoglobals // read-only
	"stacked statements":                "SELECT 1; DELETE FROM users",
	"stacked, second is a read":         "SELECT 1; SELECT 2",
	"stacked after a comment":           "SELECT 1; -- harmless\nDELETE FROM users",
	"stacked after a block comment":     "SELECT 1 /* ; */; DELETE FROM users",
	"stacked after a line comment text": "SELECT 1 -- \n; DELETE FROM users",
	"stacked with a no-break space":     "SELECT 1; DELETE FROM users",
	"leading block comment":             "/* a read */ DELETE FROM users",
	"leading line comment":              "-- a read\nDELETE FROM users",
	"mixed case":                        "dElEtE FROM users",
	"leading whitespace":                "  \n\t DELETE FROM users",
	"leading parenthesis":               "(DELETE FROM users)",
	"cte that deletes":                  "WITH x AS (SELECT 1) DELETE FROM users",
	"cte that inserts":                  "WITH x AS (SELECT 1) INSERT INTO users (id, name) VALUES (9, 'z')",
	"cte that updates":                  "WITH x AS (SELECT 1) UPDATE users SET name = 'x'",
	"explain analyze of a write":        "EXPLAIN ANALYZE DELETE FROM users",
	"explain analyze with options":      "EXPLAIN (ANALYZE, BUFFERS) UPDATE users SET name = 'x'",
	"select into a table":               "SELECT * INTO users_copy FROM users",
	"select into a file":                "SELECT 1 INTO OUTFILE '/tmp/studio-out'",
	"attach database":                   "ATTACH DATABASE 'studio-attached.db' AS other",
	"attach in memory":                  "ATTACH DATABASE ':memory:' AS other",
	"detach":                            "DETACH DATABASE main",
	"pragma sets user_version":          "PRAGMA user_version = 7",
	"pragma sets with a call":           "PRAGMA user_version(7)",
	"pragma journal_mode":               "PRAGMA journal_mode = DELETE",
	"pragma writable_schema":            "PRAGMA writable_schema = 1",
	"pragma query_only off":             "PRAGMA query_only = 0",
	"pragma foreign_keys":               "PRAGMA foreign_keys = OFF",
	"pragma checkpoint":                 "PRAGMA wal_checkpoint(TRUNCATE)",
	"pragma optimize":                   "PRAGMA optimize",
	"vacuum":                            "VACUUM",
	"vacuum into":                       "VACUUM INTO 'studio-vacuumed.db'",
	"load_extension":                    "SELECT load_extension('studio-ext')",
	"readfile":                          "SELECT readfile('/etc/passwd')",
	"writefile":                         "SELECT writefile('/tmp/studio-out', 'x')",
	"create table":                      "CREATE TABLE evil (id INTEGER)",
	"drop table":                        "DROP TABLE users",
	"alter table":                       "ALTER TABLE users ADD COLUMN evil TEXT",
	"insert":                            "INSERT INTO users (id, name) VALUES (9, 'z')",
	"replace":                           "REPLACE INTO users (id, name) VALUES (1, 'z')",
	"truncate":                          "TRUNCATE users",
	"begin":                             "BEGIN",
	"commit":                            "COMMIT",
	"an unterminated string":            "SELECT 'a; DELETE FROM users",
	"an unterminated comment":           "SELECT 1 /* ; DELETE FROM users",
}

// consoleRefusesDisguisedWrites runs every disguised write through the API of a Studio and checks that each is
// refused and that nothing in the database changed.
func consoleRefusesDisguisedWrites(t *testing.T, name string, sh *sqlHarness, h *harness) {
	t.Helper()

	for what, sql := range disguisedWrites {
		rec := apiDo(h.s, h.login, "POST", "/api/v1/sources/db/query", queryBodyJSON(t, sql, false))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %s: %q = %d, want 422 (%s)", name, what, sql, rec.Code, rec.Body)
		}
	}

	// The same texts through EXPLAIN stay read-only: they are never run as written.
	for _, sql := range []string{"DELETE FROM users", "UPDATE users SET name = 'x'", "INSERT INTO users (id, name) VALUES (9, 'z')"} {
		rec := apiDo(h.s, h.login, "POST", "/api/v1/sources/db/explain", apiJSON(t, map[string]string{"sql": sql}))
		if rec.Code != http.StatusOK {
			t.Errorf("%s: explain %q = %d, want a plan (%s)", name, sql, rec.Code, rec.Body)
		}
	}

	if n := sh.users(t); n != 5 {
		t.Errorf("%s: users has %d rows, want 5", name, n)
	}

	for _, check := range []struct{ query, what string }{
		{"SELECT COUNT(*) FROM sqlite_master WHERE name IN ('evil', 'users_copy')", "a table was created"},
		{"SELECT COUNT(*) FROM pragma_user_version WHERE user_version <> 0", "user_version changed"},
		{"SELECT COUNT(*) FROM pragma_database_list WHERE name <> 'main'", "a database was attached"},
		{"SELECT COUNT(*) FROM users WHERE name = 'x' OR name = 'z'", "a row was changed or added"},
	} {
		if sh.count1(t, check.query) != 0 {
			t.Errorf("%s: %s", name, check.what)
		}
	}

	for _, file := range []string{"studio-attached.db", "studio-vacuumed.db", "studio-ext"} {
		if _, err := os.Stat(file); err == nil {
			t.Errorf("%s: the refused statement created %s", name, file)
		}
	}

	// What a read may do still works: a comment after the statement and a read-only PRAGMA.
	for _, sql := range []string{
		"SELECT 1; -- done", "SELECT 1 /* x */", "PRAGMA table_info(users)", "PRAGMA user_version", "EXPLAIN QUERY PLAN SELECT 1",
	} {
		rec := apiDo(h.s, h.login, "POST", "/api/v1/sources/db/query", queryBodyJSON(t, sql, false))
		if rec.Code != http.StatusOK {
			t.Errorf("%s: read %q = %d, want 200 (%s)", name, sql, rec.Code, rec.Body)
		}
	}
}

// The refusals that need a server the tests do not have (PostgreSQL and MySQL) run before any connection is
// used, so they are tested on the text.
func TestConsoleRefusesReachingStatementsInEveryDialect(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct{ family, sql string }{
		{"postgres", "SELECT pg_sleep(10)"},
		{"postgres", "SELECT pg_sleep_for('1 minute')"},
		{"postgres", "SELECT 1 FROM generate_series(1, 2), pg_sleep(1)"},
		{"postgres", "SELECT pg_read_file('/etc/passwd')"},
		{"postgres", "SELECT lo_import('/etc/passwd')"},
		{"postgres", "SELECT dblink_exec('host=x', 'DROP TABLE users')"},
		{"postgres", "SELECT * INTO copy FROM users"},
		{"postgres", "WITH d AS (DELETE FROM users RETURNING *) SELECT * FROM d"},
		{"postgres", "WITH d AS (INSERT INTO users DEFAULT VALUES RETURNING *) SELECT * FROM d"},
		{"postgres", "EXPLAIN ANALYZE DELETE FROM users"},
		{"postgres", "COPY users TO '/tmp/x'"},
		{"postgres", "DO $$ BEGIN DELETE FROM users; END $$"},
		{"mysql", "SELECT SLEEP(10)"},
		{"mysql", "SELECT LOAD_FILE('/etc/passwd')"},
		{"mysql", "SELECT BENCHMARK(100000000, MD5('x'))"},
		{"mysql", "SELECT GET_LOCK('a', 100)"},
		{"mysql", "SELECT 1 INTO OUTFILE '/tmp/x'"},
		{"mysql", "SELECT 1 INTO DUMPFILE '/tmp/x'"},
		{"mysql", "SELECT 1 /*! INTO OUTFILE '/tmp/x' */"},
		{"mysql", "LOAD DATA INFILE '/etc/passwd' INTO TABLE users"},
		{"mysql", "EXPLAIN ANALYZE DELETE FROM users"},
		{"sqlite", "ATTACH DATABASE 'x.db' AS x"},
		{"sqlite", "PRAGMA user_version = 1"},
		{"sqlite", "SELECT load_extension('x')"},
	} {
		if readOnlyStatement(tt.sql, tt.family) {
			t.Errorf("%s: %q passes the read-only check", tt.family, tt.sql)
		}
	}

	// Several statements are refused by their own check, in every dialect.
	for _, tt := range []struct {
		backslash bool
		sql       string
	}{
		{false, "SELECT 1; SELECT pg_sleep(1)"},
		{true, "SELECT 1; DROP TABLE users"},
		{true, "SELECT 1 -- x\n; DELETE FROM users"},
		{false, "SELECT 1 /* a */ ; /* b */ SELECT 2"},
	} {
		if singleStatement(tt.sql, tt.backslash) {
			t.Errorf("%q counts as one statement", tt.sql)
		}
	}
}

// The log carries the size of a statement and the class of a failure, never the text or a value: a driver's
// error can quote the very token that broke the statement.
func TestConsoleLogsNeverHoldTheStatementOrAValue(t *testing.T) {
	t.Parallel()

	_, s, logs, c := writeStudio(t, true)

	const secret = "secret_token_abc"

	for name, call := range map[string]struct {
		path, sql string
		write     bool
	}{
		"a write that works":          {"query", "UPDATE users SET name = '" + secret + "' WHERE id = 1", true},
		"a write that fails":          {"query", "UPDATE users SET name = 'x' WHERE id = " + secret + " " + secret, true},
		"a read that fails":           {"query", "SELECT " + secret + " " + secret + " FROM users", false},
		"a read of a value":           {"query", "SELECT '" + secret + "' AS v", false},
		"a refused write":             {"query", "DELETE FROM users WHERE name = '" + secret + "'", false},
		"two statements":              {"query", "SELECT 1; SELECT '" + secret + "'", false},
		"an explain that fails":       {"explain", "SELECT " + secret + " " + secret, false},
		"a statement that is too big": {"query", "SELECT '" + secret + strings.Repeat("x", maxSQLBytes) + "'", false},
	} {
		body := queryBodyJSON(t, call.sql, call.write)
		if call.path == "explain" {
			body = apiJSON(t, map[string]string{"sql": call.sql})
		}

		rec := apiDo(s, c, http.MethodPost, "/api/v1/sources/db/"+call.path, body)

		if strings.Contains(rec.Body.String(), secret) && name != "a read of a value" {
			t.Errorf("%s: the response repeats the statement: %s", name, rec.Body)
		}

		if strings.Contains(logs.String(), secret) {
			t.Fatalf("%s: the log holds a value of the statement:\n%s", name, logs)
		}
	}

	if !strings.Contains(logs.String(), "bytes=") {
		t.Errorf("the audit of a write has no size:\n%s", logs)
	}

	// A failed write is logged by the class of its error.
	if !strings.Contains(logs.String(), "error=") {
		t.Errorf("a failed write is logged without its class:\n%s", logs)
	}
}
