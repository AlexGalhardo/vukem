package studio

import (
	"bytes"
	"encoding/json"
	"flag"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// elapsed and sessionKey are the values of a response that change between runs.
var (
	elapsed    = regexp.MustCompile(`"elapsedMs": \d+`)
	sessionKey = regexp.MustCompile(`"sessionKey": "[0-9a-f]{16}"`)
)

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

	buf = *bytes.NewBuffer(sessionKey.ReplaceAll(elapsed.ReplaceAll(buf.Bytes(), []byte(`"elapsedMs": 0`)), []byte(`"sessionKey": "0000000000000000"`)))
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

func TestAPIMongoRead(t *testing.T) {
	// Not parallel inside: the fake MongoDB sorts its collection list in place.
	h := newHarness(t, false)

	tests := []struct {
		name, method, target, body string
		status                     int
	}{
		{"mongo_collections", "GET", "/api/v1/sources/mongo/collections", "", 200},
		{"mongo_documents", "GET", "/api/v1/sources/mongo/collections/users/documents?perPage=1", "", 200},
		{
			"mongo_document", "GET",
			"/api/v1/sources/mongo/collections/users/documents/" + url.PathEscape(`{"$numberInt":"1"}`), "", 200,
		},
		{"mongo_find", "POST", "/api/v1/sources/mongo/find", `{"collection":"users","filter":"{}"}`, 200},
		{
			"mongo_aggregate",
			"POST",
			"/api/v1/sources/mongo/aggregate",
			`{"collection":"users","pipeline":"[{\"$match\":{}}]"}`,
			200,
		},
		{"mongo_error_collection", "GET", "/api/v1/sources/mongo/collections/missing/documents", "", 404},
		{"mongo_error_filter", "GET", "/api/v1/sources/mongo/collections/users/documents?filter=name", "", 422},
		{
			"mongo_error_operator",
			"GET",
			"/api/v1/sources/mongo/collections/users/documents?filter=name:nope:x",
			"",
			422,
		},
		{
			"mongo_error_pipeline",
			"POST",
			"/api/v1/sources/mongo/aggregate",
			`{"collection":"users","pipeline":"[{\"$out\":\"x\"}]"}`,
			422,
		},
		{"mongo_error_wrong_kind", "GET", "/api/v1/sources/redis/collections", "", 404},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			golden(t, tc.name, apiDo(h.s, h.login, tc.method, tc.target, tc.body), tc.status)
		})
	}

	for _, c := range h.fm.calls {
		if c != "find" && c != "findID" && c != "aggregate" {
			t.Errorf("a read route called %q on MongoDB", c)
		}
	}
}

func TestAPIRedisRead(t *testing.T) {
	h := newHarness(t, false)

	tests := []struct {
		name, method, target, body string
		status                     int
	}{
		{"redis_keys", "GET", "/api/v1/sources/redis/keys?match=session:*", "", 200},
		{"redis_key", "GET", "/api/v1/sources/redis/keys/session:abc", "", 200},
		{"redis_command", "POST", "/api/v1/sources/redis/command", `{"line":"GET session:abc"}`, 200},
		{"redis_error_command_not_allowed", "POST", "/api/v1/sources/redis/command", `{"line":"FLUSHALL"}`, 422},
		{"redis_error_write_needs_flag", "POST", "/api/v1/sources/redis/command", `{"line":"SET a b"}`, 422},
		{"redis_error_wrong_kind", "GET", "/api/v1/sources/mongo/keys", "", 404},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			golden(t, tc.name, apiDo(h.s, h.login, tc.method, tc.target, tc.body), tc.status)
		})
	}

	for _, c := range h.rec.names() {
		if c == "SET" || c == "DEL" || c == "KEYS" || c == "FLUSHALL" {
			t.Errorf("a read route sent %s to Redis: %v", c, h.rec.names())
		}
	}
}

func TestAPIRedisMissingKeyIs404(t *testing.T) {
	t.Parallel()

	rdb, _ := fakeRedis(t, "none")

	s, err := New(Config{Sources: []Source{rdb}, Version: "t"})
	if err != nil {
		t.Fatal(err)
	}

	rec := apiGet(s, session(t, s), "/api/v1/sources/redis/keys/gone")
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET a missing key = %d, want 404 (%s)", rec.Code, rec.Body)
	}
}

// writeRoutes are every write route of the API with a valid body.
func writeRoutes() []struct{ method, target, body string } {
	return []struct{ method, target, body string }{
		{"POST", "/api/v1/sources/db/tables/users/rows", `{"fields":{"id":{"text":"70"},"name":{"text":"x"}}}`},
		{"PATCH", "/api/v1/sources/db/tables/users/rows/1", `{"fields":{"name":{"text":"x"}}}`},
		{"DELETE", "/api/v1/sources/db/tables/users/rows/1", `{"confirm":"1"}`},
		{"POST", "/api/v1/sources/mongo/collections/users/documents", `{"doc":"{}"}`},
		{"PUT", "/api/v1/sources/mongo/collections/users/documents/1", `{"doc":"{}"}`},
		{"DELETE", "/api/v1/sources/mongo/collections/users/documents/1", `{"confirm":"1"}`},
		{"PATCH", "/api/v1/sources/redis/keys/session:abc", `{"action":"set","value":"x"}`},
		{"DELETE", "/api/v1/sources/redis/keys/session:abc", `{"confirm":"session:abc"}`},
	}
}

// mixedStudio is a Studio over the SQL harness plus the fake MongoDB and Redis.
func mixedStudio(t *testing.T, write bool) (*sqlHarness, *harness) {
	t.Helper()

	sh := newSQLHarness(t)
	m, fm := newMongo(t, write, rawDoc(t, int32(1), "ada"))
	rdb, rec := fakeRedis(t, "string")
	logs := &bytes.Buffer{}

	s, err := New(Config{
		Sources: []Source{sh.src, m, rdb}, Version: "t", Write: write, Log: slog.New(slog.NewTextHandler(logs, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}

	return sh, &harness{s: s, mongo: m, fm: fm, redis: rdb, rec: rec, logs: logs, login: session(t, s)}
}

func TestAPIWriteRoutesAbsentWithoutFlag(t *testing.T) {
	t.Parallel()

	sh, h := mixedStudio(t, false)

	for _, rt := range writeRoutes() {
		rec := apiDo(h.s, h.login, rt.method, rt.target, rt.body)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s without --write = %d, want 404", rt.method, rt.target, rec.Code)
		}
	}

	if n := sh.users(t); n != 5 {
		t.Errorf("users has %d rows, want 5", n)
	}

	for _, c := range h.fm.calls {
		if c != "find" && c != "findID" {
			t.Errorf("a write reached MongoDB: %v", h.fm.calls)
		}
	}

	for _, c := range h.rec.names() {
		if c == "SET" || c == "DEL" {
			t.Errorf("a write reached Redis: %v", h.rec.names())
		}
	}
}

func TestAPIWriteNeedsConfirm(t *testing.T) {
	t.Parallel()

	sh, h := mixedStudio(t, true)

	for _, rt := range []struct{ target string }{
		{"/api/v1/sources/db/tables/users/rows/1"},
		{"/api/v1/sources/mongo/collections/users/documents/" + url.PathEscape(`{"$numberInt":"1"}`)},
		{"/api/v1/sources/redis/keys/session:abc"},
	} {
		for name, body := range map[string]string{
			"missing": `{}`, "empty": `{"confirm":""}`, "wrong": `{"confirm":"nope"}`,
		} {
			rec := apiDo(h.s, h.login, "DELETE", rt.target, body)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Errorf("DELETE %s with %s confirm = %d, want 422 (%s)", rt.target, name, rec.Code, rec.Body)
			}
		}
	}

	if n := sh.users(t); n != 5 {
		t.Fatalf("a row was deleted without the confirmation: users = %d", n)
	}

	if slices.Contains(h.fm.calls, "remove") || slices.Contains(h.rec.names(), "DEL") {
		t.Errorf("a delete reached MongoDB or Redis: %v %v", h.fm.calls, h.rec.names())
	}

	for _, rt := range []struct{ target, body string }{
		{"/api/v1/sources/db/tables/users/rows/1", `{"confirm":"1"}`},
		{"/api/v1/sources/mongo/collections/users/documents/" + url.PathEscape(`{"$numberInt":"1"}`), `{"confirm":"1"}`},
		{"/api/v1/sources/redis/keys/session:abc", `{"confirm":"session:abc"}`},
	} {
		if rec := apiDo(h.s, h.login, "DELETE", rt.target, rt.body); rec.Code != http.StatusOK {
			t.Errorf("confirmed DELETE %s = %d, want 200 (%s)", rt.target, rec.Code, rec.Body)
		}
	}

	if n := sh.users(t); n != 4 {
		t.Errorf("users = %d after the confirmed delete, want 4", n)
	}
}

func TestAPIWriteAudited(t *testing.T) {
	t.Parallel()

	_, h := mixedStudio(t, true)

	for _, rt := range []struct{ method, target, body string }{
		{"POST", "/api/v1/sources/db/tables/users/rows", `{"fields":{"id":{"text":"70"},"name":{"text":"S3cretValue"}}}`},
		{"PATCH", "/api/v1/sources/db/tables/users/rows/70", `{"fields":{"name":{"text":"Another-S3cret"}}}`},
		{"DELETE", "/api/v1/sources/db/tables/users/rows/70", `{"confirm":"70"}`},
		{"PATCH", "/api/v1/sources/redis/keys/session:abc", `{"action":"set","value":"S3cret-redis"}`},
	} {
		if rec := apiDo(h.s, h.login, rt.method, rt.target, rt.body); rec.Code >= 300 {
			t.Fatalf("%s %s = %d (%s)", rt.method, rt.target, rec.Code, rec.Body)
		}
	}

	out := h.logs.String()
	for _, want := range []string{"action=insert", "action=update", "action=delete", "columns=id,name", "key=70", "ok=true"} {
		if !strings.Contains(out, want) {
			t.Errorf("the audit lacks %q: %s", want, out)
		}
	}

	if strings.Contains(out, "S3cret") {
		t.Errorf("a written value reached the log: %s", out)
	}

	rec := apiDo(h.s, h.login, "POST", "/api/v1/sources/db/tables/users/rows", `{"fields":{"nope":{"text":"x"}}}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("insert of an unknown column = %d, want 422", rec.Code)
	}
}
