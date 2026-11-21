package studio

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAPIQueryErrorsHideTheDatabase(t *testing.T) {
	t.Parallel()

	_, s, logs, c := sqlStudio(t, false)

	rec := apiDo(s, c, http.MethodPost, "/api/v1/sources/db/query",
		queryBodyJSON(t, "SELECT * FROM secret_ledger_xyz", false))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("a failing statement = %d, want 500 (%s)", rec.Code, rec.Body)
	}

	for _, leak := range []string{"secret_ledger_xyz", "no such table", "SQLITE"} {
		if strings.Contains(rec.Body.String(), leak) {
			t.Errorf("the database error reached the client (%q): %s", leak, rec.Body)
		}
	}

	if !strings.Contains(rec.Body.String(), `"query_failed"`) {
		t.Errorf("the answer has no query_failed code: %s", rec.Body)
	}

	// The log holds the class of the failure, never the statement or the driver's text.
	if !strings.Contains(logs.String(), "cause=query_failed") || strings.Contains(logs.String(), "secret_ledger_xyz") {
		t.Errorf("the log = %s, want the error class and no statement text", logs.String())
	}
}

func TestAPIQueryWriteIsAtomicAndNeedsTheSwitch(t *testing.T) {
	t.Parallel()

	h, s, logs, c := sqlStudio(t, true)
	post := func(sql string, write bool) *httptest.ResponseRecorder {
		return apiDo(s, c, http.MethodPost, "/api/v1/sources/db/query", queryBodyJSON(t, sql, write))
	}

	// Without the write flag in the body the console stays read-only even with --write.
	if rec := post(
		"DELETE FROM users WHERE id = 1",
		false,
	); rec.Code != http.StatusUnprocessableEntity ||
		h.users(t) != 5 {
		t.Fatalf("the console wrote without the write switch: %d, users = %d", rec.Code, h.users(t))
	}

	if rec := post("DELETE FROM users WHERE id = 1 RETURNING id", true); rec.Code != http.StatusOK || h.users(t) != 4 {
		t.Errorf("write mode did not commit: %d, users = %d", rec.Code, h.users(t))
	}

	// A statement that fails leaves nothing behind, and the audit says it failed.
	post("INSERT INTO users (id, name) VALUES (50, NULL)", true)

	if h.users(t) != 4 {
		t.Errorf("a failed write changed the table: users = %d", h.users(t))
	}

	for _, want := range []string{`msg="studio write"`, "action=console", "ok=true", "ok=false"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("the audit lacks %q: %s", want, logs.String())
		}
	}
}

func TestAPIQueryReportsRowsAffected(t *testing.T) {
	t.Parallel()

	h, s, _, c := sqlStudio(t, true)

	rec := apiDo(s, c, http.MethodPost, "/api/v1/sources/db/query",
		queryBodyJSON(t, "UPDATE users SET name = 'x' WHERE id <= 2", true))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"affected":2`) {
		t.Errorf("update = %d, want affected 2:\n%s", rec.Code, rec.Body)
	}

	if h.users(t) != 5 {
		t.Errorf("users = %d, the update must not change the count", h.users(t))
	}

	// A statement that returns rows reports them as rows.
	rec = apiDo(s, c, http.MethodPost, "/api/v1/sources/db/query",
		queryBodyJSON(t, "DELETE FROM users WHERE id = 5 RETURNING id", true))
	if !strings.Contains(rec.Body.String(), `"rows":[[{"t":"5","l":true}]]`) ||
		strings.Contains(rec.Body.String(), `"affected"`) {
		t.Errorf("delete returning = %s", rec.Body)
	}
}

// TestAPICellsHoldingHTMLAreJSONText: cells and column names that hold HTML
// travel as JSON text, never as markup; the interface renders them as text.
func TestAPICellsHoldingHTMLAreJSONText(t *testing.T) {
	t.Parallel()

	_, s, _, c := sqlStudio(t, false)

	const payload = "<script>alert(1)</script>"

	rec := apiDo(s, c, http.MethodPost, "/api/v1/sources/db/query",
		queryBodyJSON(t, `SELECT bio, 1 AS "`+payload+`" FROM users WHERE id = 4`, false))

	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want JSON", ct)
	}

	if strings.Contains(rec.Body.String(), "<script>") {
		t.Errorf("raw markup in the JSON body: %s", rec.Body)
	}

	var got struct {
		Columns []string `json:"columns"`
		Rows    [][]struct {
			T string `json:"t"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got.Rows) != 1 {
		t.Fatalf("answer = %s, %v", rec.Body, err)
	}

	if got.Columns[1] != payload || got.Rows[0][0].T != payload {
		t.Errorf("column %q, cell %q; want the payload as text in both", got.Columns[1], got.Rows[0][0].T)
	}
}

func TestAPIRedisConsoleAuditsOnlyTheCommandName(t *testing.T) {
	t.Parallel()

	_, h := mixedStudio(t, true)

	for _, line := range []string{`SET session:1 "S3cret-value"`, "GET session:1"} {
		body := apiJSON(t, map[string]string{"line": line})
		if rec := apiDo(h.s, h.login, "POST", "/api/v1/sources/redis/command", body); rec.Code != http.StatusOK {
			t.Fatalf("%s = %d %s", line, rec.Code, rec.Body)
		}
	}

	out := h.logs.String()
	if !strings.Contains(out, "action=console") || !strings.Contains(out, "target=SET") ||
		!strings.Contains(out, "args=2") {
		t.Errorf("the audit lacks the command name and the argument count: %s", out)
	}

	if strings.Contains(out, "S3cret") || strings.Contains(out, "session:1") {
		t.Errorf("an argument reached the log: %s", out)
	}

	if strings.Count(out, "action=console") != 1 {
		t.Errorf("a read command was audited as a write: %s", out)
	}
}
