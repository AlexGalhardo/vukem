package studio

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

const itemsBase = "/api/v1/sources/db/tables/items/rows"

func TestAPISQLWriteFlows(t *testing.T) {
	t.Parallel()

	h, s, _, c := writeStudio(t, true)

	// Insert: an omitted column gets the default, NULL and typed values are coerced.
	rec := apiDo(s, c, "POST", itemsBase, `{"fields":{
		"id":{"text":"2"},"qty":{"text":"3"},"price":{"text":"9.50"},"active":{"text":"true"},"born":{"text":"2026-01-02"},
		"meta":{"text":"{\"k\":1}"},"note":{"text":"","null":true}}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("insert = %d %s", rec.Code, rec.Body)
	}

	rs, err := h.src.Query(
		t.Context(), `SELECT qty, price, active, born, meta, note IS NULL FROM items WHERE id = 2`, false,
	)
	if err != nil || len(rs.Rows) != 1 {
		t.Fatalf("inserted row = %+v, %v", rs, err)
	}

	got := []string{}
	for _, cell := range rs.Rows[0] {
		got = append(got, cell.Text)
	}

	if diff := cmp.Diff([]string{"3", "9.5", "1", "2026-01-02T00:00:00Z", `{"k":1}`, "1"}, got); diff != "" {
		t.Errorf("inserted values (-want +got):\n%s", diff)
	}

	// Update: only the submitted columns change.
	rec = apiDo(s, c, "PATCH", itemsBase+"/2", `{"fields":{"qty":{"text":"8"},"note":{"text":"edited"}}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("update = %d %s", rec.Code, rec.Body)
	}

	rs, _ = h.src.Query(t.Context(), `SELECT qty, price, note FROM items WHERE id = 2`, false)
	if got := []string{
		rs.Rows[0][0].Text,
		rs.Rows[0][1].Text,
		rs.Rows[0][2].Text,
	}; !cmp.Equal(
		got,
		[]string{"8", "9.5", "edited"},
	) {
		t.Errorf("updated row = %v", got)
	}

	// A key that matches no row changes nothing and is an error.
	if rec := apiDo(s, c, "PATCH", itemsBase+"/999", `{"fields":{"qty":{"text":"1"}}}`); rec.Code == http.StatusOK {
		t.Error("an update of a missing row succeeded")
	}

	// Delete with the typed key.
	rec = apiDo(s, c, "DELETE", itemsBase+"/2", `{"confirm":"2"}`)
	if rec.Code != http.StatusOK || h.count1(t, `SELECT COUNT(*) FROM items`) != 1 {
		t.Errorf("delete = %d, items = %d", rec.Code, h.count1(t, `SELECT COUNT(*) FROM items`))
	}

	// A composite key is typed back joined by commas.
	pairs := "/api/v1/sources/db/tables/pairs/rows/" + url.PathEscape("1,2")

	if rec := apiDo(s, c, "DELETE", pairs, `{"confirm":"1"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("composite delete typed with one part = %d, want 422", rec.Code)
	}

	rec = apiDo(s, c, "DELETE", pairs, `{"confirm":"1,2"}`)
	if rec.Code != http.StatusOK || h.count1(t, `SELECT COUNT(*) FROM pairs`) != 2 {
		t.Errorf("composite delete = %d", rec.Code)
	}
}

func TestSecurityAPISQLDeleteNeedsTheTypedKeyAndIsAudited(t *testing.T) {
	t.Parallel()

	h, s, logs, c := writeStudio(t, true)
	target := "/api/v1/sources/db/tables/users/rows/1"

	for name, body := range map[string]string{"missing": `{}`, "wrong": `{"confirm":"2"}`, "empty": `{"confirm":""}`} {
		if rec := apiDo(s, c, "DELETE", target, body); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s confirmation = %d, want 422", name, rec.Code)
		}
	}

	if h.users(t) != 5 {
		t.Fatalf("a row was deleted without confirmation: users = %d", h.users(t))
	}

	if !strings.Contains(logs.String(), "ok=false") || strings.Contains(logs.String(), "ok=true") {
		t.Errorf("refused deletes must be audited as failures: %s", logs.String())
	}

	if rec := apiDo(s, c, "DELETE", target, `{"confirm":"1"}`); rec.Code != http.StatusOK || h.users(t) != 4 {
		t.Errorf("confirmed delete = %d, users = %d", rec.Code, h.users(t))
	}
}

func TestSecurityAPISQLWriteRejectsUnknownColumnsAndBadValues(t *testing.T) {
	t.Parallel()

	h, s, _, c := writeStudio(t, true)

	users := "/api/v1/sources/db/tables/users/rows"

	for name, rt := range map[string]struct{ method, target, body string }{
		"insert":         {"POST", users, `{"fields":{"id":{"text":"60"},"name":{"text":"x"},"nope":{"text":"1"}}}`},
		"insert null":    {"POST", users, `{"fields":{"id":{"text":"61"},"name":{"text":"x"},"nope":{"null":true}}}`},
		"injected name":  {"POST", users, `{"fields":{"name) VALUES ('x'); --":{"text":"1"}}}`},
		"update unknown": {"PATCH", users + "/1", `{"fields":{"nope":{"text":"1"}}}`},
		"update the key": {"PATCH", users + "/1", `{"fields":{"id":{"text":"77"}}}`},
		"update no cols": {"PATCH", users + "/1", `{"fields":{}}`},
		"integer":        {"POST", itemsBase, `{"fields":{"id":{"text":"5"},"qty":{"text":"many"}}}`},
		"null":           {"POST", itemsBase, `{"fields":{"id":{"text":"5"},"qty":{"text":"","null":true}}}`},
		"date":           {"POST", itemsBase, `{"fields":{"id":{"text":"5"},"qty":{"text":"1"},"born":{"text":"soon"}}}`},
		"json":           {"POST", itemsBase, `{"fields":{"id":{"text":"5"},"qty":{"text":"1"},"meta":{"text":"{"}}}`},
		"blob":           {"POST", itemsBase, `{"fields":{"id":{"text":"5"},"qty":{"text":"1"},"raw":{"text":"AAEC"}}}`},
	} {
		if rec := apiDo(s, c, rt.method, rt.target, rt.body); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d, want 422 (%s)", name, rec.Code, rec.Body)
		}
	}

	if h.users(t) != 5 || h.count1(t, `SELECT COUNT(*) FROM items`) != 1 {
		t.Error("a refused write changed a table")
	}

	if rec := apiDo(
		s,
		c,
		"POST",
		users,
		`{"fields":{"id":{"text":"1"},"name":{"text":"x"}}}`,
	); rec.Code == http.StatusCreated {
		t.Error("an insert with a duplicate key succeeded")
	}
}

func TestSecurityAPISQLWriteNeedsAPrimaryKey(t *testing.T) {
	t.Parallel()

	h, s, logs, c := writeStudio(t, true)
	logRows := "/api/v1/sources/db/tables/log/rows"

	for _, rt := range []struct{ method, target, body string }{
		{"POST", logRows, `{"fields":{"line":{"text":"two"}}}`},
		{"PATCH", logRows + "/one", `{"fields":{"line":{"text":"two"}}}`},
		{"DELETE", logRows + "/one", `{"confirm":"one"}`},
	} {
		if rec := apiDo(s, c, rt.method, rt.target, rt.body); rec.Code != http.StatusNotFound {
			t.Errorf("%s %s on a table without a primary key = %d, want 404", rt.method, rt.target, rec.Code)
		}
	}

	if h.count1(t, `SELECT COUNT(*) FROM log`) != 1 {
		t.Error("a table without a primary key was written")
	}

	if strings.Contains(logs.String(), "ok=true") {
		t.Errorf("a refused write was audited as ok: %s", logs.String())
	}
}
