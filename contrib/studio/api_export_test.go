package studio

import (
	"encoding/csv"
	"net/http"
	"strings"
	"testing"
)

func TestAPIExportRows(t *testing.T) {
	t.Parallel()

	h, s, _, c := writeStudio(t, false)

	long := strings.Repeat("é", 400)
	if _, err := h.db.ExecContext(t.Context(), `UPDATE users SET name = ? WHERE id = 5`, "=HYPERLINK(1)"); err != nil {
		t.Fatal(err)
	}

	if _, err := h.db.ExecContext(t.Context(), `UPDATE users SET bio = ? WHERE id = 1`, long); err != nil {
		t.Fatal(err)
	}

	base := "/api/v1/sources/db/tables/"
	rec := apiGet(s, c, base+"users/export")

	lines := strings.Split(strings.TrimSpace(rec.Body.String()), "\n")
	if rec.Code != 200 || len(lines) != 5 || !strings.HasPrefix(lines[0], `{"id":1,"name":"ada","bio":"`+long) {
		t.Fatalf("jsonl = %d, %d lines, first %.80q", rec.Code, len(lines), lines[0])
	}

	// Numbers stay numbers; text that looks like one stays text.
	if got := apiGet(s, c, base+"items/export").Body.String(); !strings.Contains(got, `"id":1,"qty":5,"price":null,`) {
		t.Errorf("items jsonl = %q, want the integers as JSON numbers and NULL as null", got)
	}

	if !strings.Contains(lines[1], `"bio":"50%_off"`) ||
		!strings.Contains(rec.Header().Get("Content-Disposition"), "users.jsonl") {
		t.Errorf("jsonl line 2 / disposition = %q / %q", lines[1], rec.Header().Get("Content-Disposition"))
	}

	// CSV: formulas are neutralized, NULL is empty, the filter applies.
	rows, err := csv.NewReader(strings.NewReader(
		apiGet(s, c, base+"users/export?format=csv&filter=id:gte:4&sort=name").Body.String(),
	)).ReadAll()
	if err != nil || len(rows) != 3 || rows[0][1] != "name" ||
		rows[1][1] != "'=HYPERLINK(1)" && rows[2][1] != "'=HYPERLINK(1)" {
		t.Errorf("csv = %v, %v", rows, err)
	}

	// The cap and the trailer.
	s.exportMax = 2
	rec = apiGet(s, c, base+"users/export")

	if n := strings.Count(rec.Body.String(), "\n"); n != 2 || rec.Result().Trailer.Get("X-Studio-Truncated") != "true" {
		t.Errorf("capped export: %d lines, trailer %q", n, rec.Result().Trailer.Get("X-Studio-Truncated"))
	}

	// Refusals are JSON errors, before anything is streamed.
	for target, want := range map[string]int{
		base + "users/export?format=xml":             http.StatusUnprocessableEntity,
		base + "users/export?filter=nope:eq:1":       http.StatusUnprocessableEntity,
		base + "users/export?sort=%22id%22":          http.StatusUnprocessableEntity,
		base + "nope/export":                         http.StatusNotFound,
		base + "users%3BDROP%20TABLE%20users/export": http.StatusNotFound,
		"/api/v1/sources/nope/tables/users/export":   http.StatusNotFound,
	} {
		rec := apiGet(s, c, target)
		if rec.Code != want || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
			t.Errorf("GET %s = %d %s, want a JSON %d", target, rec.Code, rec.Header().Get("Content-Type"), want)
		}
	}
}

func TestAPIExportDocuments(t *testing.T) {
	t.Parallel()

	h := newHarness(t, false)
	base := "/api/v1/sources/mongo/collections/users/export"

	rec := apiGet(h.s, h.login, base+"?format=json")
	if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Disposition"), `filename="users.jsonl"`) {
		t.Fatalf("export = %d %v", rec.Code, rec.Header())
	}

	lines := strings.Split(strings.TrimSpace(rec.Body.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"name":"ada"`) {
		t.Errorf("lines = %q, want 2 JSON lines", lines)
	}

	h.s.exportMax = 1

	rec = apiGet(h.s, h.login, base+"?format=json")
	if n := len(strings.Split(strings.TrimSpace(rec.Body.String()), "\n")); n != 1 {
		t.Errorf("capped export has %d rows, want 1", n)
	}

	if got := rec.Result().Trailer.Get("X-Studio-Truncated"); got != "true" {
		t.Errorf("trailer X-Studio-Truncated = %q, want true", got)
	}

	if rec := apiGet(h.s, h.login, base+"?format=xml"); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("format xml = %d, want 422", rec.Code)
	}

	if rec := apiGet(h.s, h.login, "/api/v1/sources/redis/collections/users/export"); rec.Code != http.StatusNotFound {
		t.Errorf("export of a Redis source = %d, want 404", rec.Code)
	}
}

func TestAPIRedisSetKeepsTTLAndTTLAction(t *testing.T) {
	t.Parallel()

	h := newHarness(t, true)
	target := "/api/v1/sources/redis/keys/k1"

	if rec := apiDo(h.s, h.login, "PATCH", target, `{"action":"set","value":"v"}`); rec.Code != http.StatusOK {
		t.Errorf("set = %d", rec.Code)
	}

	if rec := apiDo(h.s, h.login, "PATCH", target, `{"action":"ttl","seconds":"abc"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("ttl with text = %d, want 422", rec.Code)
	}

	if rec := apiDo(h.s, h.login, "PATCH", target, `{"action":"ttl"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("ttl without seconds = %d, want 422", rec.Code)
	}

	if rec := apiDo(h.s, h.login, "PATCH", target, `{"action":"ttl","seconds":0}`); rec.Code != http.StatusOK {
		t.Errorf("ttl 0 = %d", rec.Code)
	}

	got := strings.Join(h.rec.names(), " ")
	if !strings.Contains(got, "SET") || !strings.Contains(got, "PERSIST") {
		t.Errorf("commands = %s, want SET and PERSIST", got)
	}
}

func TestBootstrapListsSourcesWithoutCredentials(t *testing.T) {
	t.Parallel()

	s := newStudio(t)
	cookie := session(t, s)

	rec := apiGet(s, cookie, "/api/v1/bootstrap")
	if body := rec.Body.String(); rec.Code != http.StatusOK || !strings.Contains(body, "sqlite  storage/app.db") ||
		!strings.Contains(body, `"write":false`) {
		t.Errorf("bootstrap = %d %q", rec.Code, body)
	}

	if rec := apiGet(s, cookie, "/api/v1/nope"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown API path: status = %d, want 404", rec.Code)
	}
}
