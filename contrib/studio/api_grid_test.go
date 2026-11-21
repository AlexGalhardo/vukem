package studio

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func decodeMap(t *testing.T, body string) map[string]any {
	t.Helper()

	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("body is not a JSON object: %v\n%s", err, body)
	}

	return m
}

func TestAPICount(t *testing.T) {
	t.Parallel()

	_, h := mixedStudio(t, false)

	for target, want := range map[string]float64{
		"/api/v1/sources/db/tables/users/count":                               5,
		"/api/v1/sources/db/tables/users/count?filter=name:starts:a":          2,
		"/api/v1/sources/db/tables/users/count?filter=bio:null:":              1,
		"/api/v1/sources/db/tables/users/count?filter=id:gt:1&filter=id:lt:4": 2,
	} {
		rec := apiGet(h.s, h.login, target)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d (%s)", target, rec.Code, rec.Body)
		}

		if got := decodeMap(t, rec.Body.String())["count"]; got != want {
			t.Errorf("GET %s count = %v, want %v", target, got, want)
		}
	}

	for _, target := range []string{
		"/api/v1/sources/db/tables/users/count?filter=nope:eq:1",
		"/api/v1/sources/db/tables/users/count?filter=name:regex:1",
		"/api/v1/sources/db/tables/users/count?filter=broken",
	} {
		if rec := apiGet(h.s, h.login, target); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("GET %s = %d, want 422", target, rec.Code)
		}
	}

	if rec := apiGet(h.s, h.login, "/api/v1/sources/db/tables/ghost/count"); rec.Code != http.StatusNotFound {
		t.Errorf("count of an unknown table = %d, want 404", rec.Code)
	}
}

func TestAPICountTimeout(t *testing.T) {
	t.Parallel()

	sh, h := mixedStudio(t, false)
	sh.src.countLimit = time.Nanosecond

	rec := apiGet(h.s, h.login, "/api/v1/sources/db/tables/users/count")
	if rec.Code != http.StatusOK {
		t.Fatalf("a timed out count = %d (%s), want 200", rec.Code, rec.Body)
	}

	m := decodeMap(t, rec.Body.String())
	if m["timedOut"] != true {
		t.Errorf("body = %s, want timedOut", rec.Body)
	}

	if _, ok := m["count"]; ok {
		t.Errorf("a timed out count carries a number: %s", rec.Body)
	}
}

func userName(t *testing.T, sh *sqlHarness, id int) string {
	t.Helper()

	var name string
	if err := sh.db.QueryRowContext(t.Context(), `SELECT name FROM users WHERE id = ?`, id).Scan(&name); err != nil {
		t.Fatal(err)
	}

	return name
}

func TestUpdateExpectGuard(t *testing.T) {
	t.Parallel()

	sh, h := mixedStudio(t, true)
	patch := func(body string) int {
		return apiDo(h.s, h.login, "PATCH", "/api/v1/sources/db/tables/users/rows/1", body).Code
	}

	// A stale expected value is a 409 with the current value, and nothing changes.
	rec := apiDo(h.s, h.login, "PATCH", "/api/v1/sources/db/tables/users/rows/1",
		`{"fields":{"name":{"text":"S3cret-new"}},"expect":{"name":{"text":"stale-S3cret"}}}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale expect = %d (%s), want 409", rec.Code, rec.Body)
	}

	body := decodeMap(t, rec.Body.String())
	errBody, _ := body["error"].(map[string]any)

	cur, _ := errBody["current"].(map[string]any)["name"].(map[string]any)
	if errBody["code"] != "conflict" || cur["t"] != "ada" {
		t.Errorf("conflict body = %s, want code conflict and current name ada", rec.Body)
	}

	if got := userName(t, sh, 1); got != "ada" {
		t.Errorf("name = %q after a refused update, want ada", got)
	}

	// The current value is not secret to the client, but no value reaches the log.
	if out := h.logs.String(); strings.Contains(out, "S3cret") || strings.Contains(out, "ada") {
		t.Errorf("a value reached the log: %s", out)
	}

	if !strings.Contains(h.logs.String(), "error=conflict") {
		t.Errorf("the audit lacks the conflict class: %s", h.logs)
	}

	// The right expected value commits.
	if res := patch(`{"fields":{"name":{"text":"ada2"}},"expect":{"name":{"text":"ada"}}}`); res != http.StatusOK {
		t.Fatalf("matching expect = %d, want 200", res)
	}

	if got := userName(t, sh, 1); got != "ada2" {
		t.Errorf("name = %q, want ada2", got)
	}

	// A column outside the edited ones can guard the row too.
	if res := patch(`{"fields":{"name":{"text":"x"}},"expect":{"bio":{"text":"nope"}}}`); res != http.StatusConflict {
		t.Errorf("stale guard on another column = %d, want 409", res)
	}

	if res := patch(
		`{"fields":{"name":{"text":"x"}},"expect":{"ghost":{"text":"1"}}}`,
	); res != http.StatusUnprocessableEntity {
		t.Errorf("expect on an unknown column = %d, want 422", res)
	}

	if got := userName(t, sh, 1); got != "ada2" {
		t.Errorf("name = %q after refused updates, want ada2", got)
	}

	// Without expect the update is unguarded, as before.
	if res := patch(`{"fields":{"name":{"text":"ada3"}}}`); res != http.StatusOK {
		t.Errorf("unguarded update = %d, want 200", res)
	}

	// The guard is for updates: an insert refuses the field.
	if rec := apiDo(
		h.s,
		h.login,
		"POST",
		"/api/v1/sources/db/tables/users/rows",
		`{"fields":{"id":{"text":"80"},"name":{"text":"z"}},"expect":{"name":{"text":"z"}}}`,
	); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("insert with expect = %d, want 422", rec.Code)
	}
}

func TestUpdateExpectNull(t *testing.T) {
	t.Parallel()

	sh, h := mixedStudio(t, true)
	target := "/api/v1/sources/db/tables/users/rows/1" // bio is NULL

	if rec := apiDo(
		h.s,
		h.login,
		"PATCH",
		target,
		`{"fields":{"bio":{"text":"first"}},"expect":{"bio":{"text":"","null":false}}}`,
	); rec.Code != http.StatusConflict {
		t.Fatalf("an empty text is not NULL: %d (%s), want 409", rec.Code, rec.Body)
	}

	if rec := apiDo(h.s, h.login, "PATCH", target,
		`{"fields":{"bio":{"text":"first"}},"expect":{"bio":{"null":true}}}`); rec.Code != http.StatusOK {
		t.Fatalf("NULL expected and found: %d (%s), want 200", rec.Code, rec.Body)
	}

	// The value is no longer NULL: expecting NULL again conflicts and reports the text.
	rec := apiDo(h.s, h.login, "PATCH", target, `{"fields":{"bio":{"text":"second"}},"expect":{"bio":{"null":true}}}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"t":"first"`) {
		t.Fatalf("NULL expected, text found: %d (%s), want 409 with first", rec.Code, rec.Body)
	}

	// Setting NULL back, guarded by the text.
	if rec := apiDo(h.s, h.login, "PATCH", target,
		`{"fields":{"bio":{"null":true}},"expect":{"bio":{"text":"first"}}}`); rec.Code != http.StatusOK {
		t.Fatalf("set NULL guarded by text: %d (%s)", rec.Code, rec.Body)
	}

	var isNull bool
	if err := sh.db.QueryRowContext(t.Context(), `SELECT bio IS NULL FROM users WHERE id = 1`).
		Scan(&isNull); err != nil ||
		!isNull {
		t.Errorf("bio is NULL = %v, %v; want true", isNull, err)
	}
}
