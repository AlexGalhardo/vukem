package studio

import (
	"bytes"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// elapsed is the one value of a response that changes between runs.
var elapsed = regexp.MustCompile(`"elapsedMs": \d+`)

var update = flag.Bool("update", false, "rewrite the golden files in testdata/")

// apiDo sends a request to the API with the session cookie.
func apiDo(s *Studio, c *http.Cookie, method, target, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1:4984"+target, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:50000"

	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}

	if c != nil {
		r.AddCookie(c)
	}

	return serve(s, r)
}

func apiGet(s *Studio, c *http.Cookie, target string) *httptest.ResponseRecorder {
	return apiDo(s, c, http.MethodGet, target, "")
}

// golden compares the response with testdata/api/<name>.json (indented).
func golden(t *testing.T, name string, rec *httptest.ResponseRecorder, wantStatus int) {
	t.Helper()

	if rec.Code != wantStatus {
		t.Fatalf("%s: status = %d, want %d (body %s)", name, rec.Code, wantStatus, rec.Body)
	}

	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("%s: Content-Type = %q, want application/json", name, ct)
	}

	var buf bytes.Buffer
	if err := json.Indent(&buf, bytes.TrimSpace(rec.Body.Bytes()), "", "  "); err != nil {
		t.Fatalf("%s: body is not JSON: %v\n%s", name, err, rec.Body)
	}

	buf = *bytes.NewBuffer(elapsed.ReplaceAll(buf.Bytes(), []byte(`"elapsedMs": 0`)))
	buf.WriteByte('\n')

	file := filepath.Join("testdata", "api", name+".json")

	if *update {
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(file, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	want, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("%s: %v (run with -update to create it)", name, err)
	}

	if diff := cmp.Diff(strings.ReplaceAll(string(want), "\r\n", "\n"), buf.String()); diff != "" {
		t.Errorf("%s: response differs from %s (-want +got):\n%s", name, file, diff)
	}
}

func TestAPIBootstrap(t *testing.T) {
	t.Parallel()

	_, s, _, c := writeStudio(t, false)
	golden(t, "bootstrap", apiGet(s, c, "/api/v1/bootstrap"), http.StatusOK)

	_, sw, _, cw := writeStudio(t, true)

	var got struct {
		Write bool `json:"write"`
	}
	if err := json.Unmarshal(apiGet(sw, cw, "/api/v1/bootstrap").Body.Bytes(), &got); err != nil || !got.Write {
		t.Errorf("bootstrap with --write = %+v, %v, want write true", got, err)
	}
}

func TestAPISQLRead(t *testing.T) {
	t.Parallel()

	_, s, _, c := writeStudio(t, false)

	page1 := apiGet(s, c, "/api/v1/sources/db/tables/users/rows?perPage=2")

	var p struct {
		Next    string `json:"next"`
		HasMore bool   `json:"hasMore"`
	}
	if err := json.Unmarshal(page1.Body.Bytes(), &p); err != nil || p.Next == "" || !p.HasMore {
		t.Fatalf("page 1 = %s, want a next cursor", page1.Body)
	}

	tests := []struct {
		name   string
		target string
		status int
	}{
		{"tables", "/api/v1/sources/db/tables", http.StatusOK},
		{"table_pets", "/api/v1/sources/db/tables/pets", http.StatusOK},
		{"rows_users_page1", "/api/v1/sources/db/tables/users/rows?perPage=2", http.StatusOK},
		{
			"rows_users_page2",
			"/api/v1/sources/db/tables/users/rows?perPage=2&after=" + url.QueryEscape(p.Next),
			http.StatusOK,
		},
		{
			"rows_users_filtered",
			"/api/v1/sources/db/tables/users/rows?filter=name:starts:a&sort=name&desc=1",
			http.StatusOK,
		},
		{"rows_items_cells", "/api/v1/sources/db/tables/items/rows", http.StatusOK},
		{"row_users_1", "/api/v1/sources/db/tables/users/rows/1", http.StatusOK},
		{"row_pairs_composite", "/api/v1/sources/db/tables/pairs/rows/" + url.PathEscape("1,2"), http.StatusOK},
		{"references_owners", "/api/v1/sources/db/tables/owners/references", http.StatusOK},
		{"error_unknown_table", "/api/v1/sources/db/tables/nope", http.StatusNotFound},
		{"error_unknown_sort", "/api/v1/sources/db/tables/users/rows?sort=nope", http.StatusUnprocessableEntity},
		{"error_bad_filter", "/api/v1/sources/db/tables/users/rows?filter=name", http.StatusUnprocessableEntity},
		{"error_no_route", "/api/v1/nothing", http.StatusNotFound},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			golden(t, tc.name, apiGet(s, c, tc.target), tc.status)
		})
	}
}

func apiJSON(t *testing.T, v any) string {
	t.Helper()

	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}

	return string(b)
}

func queryBodyJSON(t *testing.T, sql string, write bool) string {
	t.Helper()

	return apiJSON(t, map[string]any{"sql": sql, "write": write})
}

func TestAPIQueryReadOnly(t *testing.T) {
	t.Parallel()

	h, s, _, c := writeStudio(t, false)

	golden(
		t,
		"query_select",
		apiDo(
			s,
			c,
			http.MethodPost,
			"/api/v1/sources/db/query",
			queryBodyJSON(t, "SELECT id, name FROM users WHERE id < 3 ORDER BY id", false),
		),
		http.StatusOK,
	)

	for name, sql := range map[string]string{
		"update":     "UPDATE users SET name = 'x'",
		"delete":     "DELETE FROM users",
		"drop":       "DROP TABLE users",
		"two":        "SELECT 1; SELECT 2",
		"two writes": "SELECT 1; DELETE FROM users",
		"empty":      "  ",
	} {
		rec := apiDo(s, c, http.MethodPost, "/api/v1/sources/db/query", queryBodyJSON(t, sql, false))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("query %q = %d, want 422 (%s)", name, rec.Code, rec.Body)
		}
	}

	if n := h.users(t); n != 5 {
		t.Errorf("users has %d rows after the refused statements, want 5", n)
	}
}

func TestAPIQueryRejectsWriteWithoutFlag(t *testing.T) {
	t.Parallel()

	h, s, _, c := writeStudio(t, false)

	rec := apiDo(s, c, http.MethodPost, "/api/v1/sources/db/query", queryBodyJSON(t, "DELETE FROM users", true))
	if rec.Code != http.StatusForbidden {
		t.Errorf("write:true without --write = %d, want 403 (%s)", rec.Code, rec.Body)
	}

	long := "SELECT '" + strings.Repeat("x", 200<<10) + "'"

	rec = apiDo(s, c, http.MethodPost, "/api/v1/sources/db/query", queryBodyJSON(t, long, false))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("a 200 KB statement = %d, want 422", rec.Code)
	}

	if n := h.users(t); n != 5 {
		t.Errorf("users has %d rows, want 5", n)
	}
}

func TestAPIQueryWriteWithFlagIsAuditedWithoutTheStatement(t *testing.T) {
	t.Parallel()

	h, s, logs, c := writeStudio(t, true)

	rec := apiDo(s, c, http.MethodPost, "/api/v1/sources/db/query",
		queryBodyJSON(t, "UPDATE users SET name = 'secret-value' WHERE id = 1", true))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"affected":1`) {
		t.Fatalf("write query = %d %s, want 200 with affected 1", rec.Code, rec.Body)
	}

	if h.count1(t, "SELECT COUNT(*) FROM users WHERE name = 'secret-value'") != 1 {
		t.Error("the update did not run")
	}

	if !strings.Contains(logs.String(), "studio write") || strings.Contains(logs.String(), "secret-value") {
		t.Errorf("audit log = %q, want an entry without the statement text", logs)
	}
}

func TestAPIExplain(t *testing.T) {
	t.Parallel()

	h, s, _, c := writeStudio(t, false)

	rec := apiDo(
		s,
		c,
		http.MethodPost,
		"/api/v1/sources/db/explain",
		apiJSON(t, map[string]string{"sql": "SELECT * FROM users WHERE id = 1"}),
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("explain = %d %s, want 200", rec.Code, rec.Body)
	}

	var got struct {
		Rows [][]apiCell `json:"rows"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got.Rows) == 0 {
		t.Errorf("explain rows = %v, %v, want a plan", got.Rows, err)
	}

	// Explaining a write shows its plan and changes nothing.
	apiDo(
		s,
		c,
		http.MethodPost,
		"/api/v1/sources/db/explain",
		apiJSON(t, map[string]string{"sql": "DELETE FROM users"}),
	)

	if n := h.users(t); n != 5 {
		t.Errorf("users has %d rows after explain, want 5", n)
	}
}
