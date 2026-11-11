package studio

import (
	"bytes"
	"encoding/csv"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

var writeTables = append(
	append(fakeCatalog{}, sqlTables...),
	Table{
		Name: "items",
		Columns: []Column{
			{Name: "id", Type: "integer"},
			{Name: "qty", Type: "integer"},
			{Name: "price", Type: "numeric(10,2)", Nullable: true},
			{Name: "active", Type: "boolean", Nullable: true},
			{Name: "born", Type: "date", Nullable: true},
			{Name: "meta", Type: "json", Nullable: true},
			{Name: "note", Type: "text", Nullable: true},
			{Name: "raw", Type: "blob", Nullable: true},
		},
		PrimaryKey: []string{"id"},
	},
	Table{
		Name:       "owners",
		Columns:    []Column{{Name: "id", Type: "integer"}, {Name: "name", Type: "text"}},
		PrimaryKey: []string{"id"},
	},
	Table{
		Name:       "pets",
		Columns:    []Column{{Name: "id", Type: "integer"}, {Name: "owner_id", Type: "integer", Nullable: true}},
		PrimaryKey: []string{"id"},
		ForeignKeys: []ForeignKey{
			{Columns: []string{"owner_id"}, RefTable: "owners", RefColumns: []string{"id"}},
		},
	},
)

// writeStudio is a Studio over the SQL harness plus the tables of writeTables.
func writeStudio(t *testing.T, write bool) (*sqlHarness, *Studio, *bytes.Buffer, *http.Cookie) {
	t.Helper()

	h := newSQLHarness(t)

	for _, q := range []string{
		`CREATE TABLE items (id INTEGER PRIMARY KEY, qty INTEGER NOT NULL, price NUMERIC, active BOOLEAN, born DATE,
			meta JSON, note TEXT, raw BLOB)`,
		`INSERT INTO items (id, qty, note, raw) VALUES (1, 5, 'first', x'ff00')`,
		`CREATE TABLE owners (id INTEGER PRIMARY KEY, name TEXT)`,
		`INSERT INTO owners VALUES (1, 'ada')`,
		`CREATE TABLE pets (id INTEGER PRIMARY KEY, owner_id INTEGER REFERENCES owners (id))`,
		`INSERT INTO pets VALUES (1, 1)`,
	} {
		if _, err := h.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}

	h.src = NewSQL("db", "sqlite file", h.db, writeTables)
	h.count.n.Store(0)

	logs := &bytes.Buffer{}

	s, err := New(Config{
		Sources: []Source{h.src}, Version: "t", Write: write, Log: slog.New(slog.NewTextHandler(logs, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}

	return h, s, logs, session(t, s)
}

func postForm(s *Studio, c *http.Cookie, target string, form url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:4984"+target, strings.NewReader(form.Encode()))
	r.RemoteAddr = "127.0.0.1:50000"
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	if c != nil {
		r.AddCookie(c)
	}

	return serve(s, r)
}

func (h *sqlHarness) count1(t *testing.T, query string) int {
	t.Helper()

	var n int
	if err := h.db.QueryRowContext(t.Context(), query).Scan(&n); err != nil {
		t.Fatal(err)
	}

	return n
}

func TestCoerce(t *testing.T) {
	t.Parallel()

	tests := []struct {
		typ  string
		f    Field
		want any
		bad  bool
	}{
		{"integer", Field{Text: " 42 "}, int64(42), false},
		{"bigint", Field{Text: "4x"}, nil, true},
		{"int unsigned", Field{Text: "7"}, int64(7), false},
		{"boolean", Field{Text: "TRUE"}, true, false},
		{"boolean", Field{Text: "0"}, false, false},
		{"boolean", Field{Text: "yes"}, nil, true},
		{"real", Field{Text: "1.5"}, 1.5, false},
		{"numeric(10,2)", Field{Text: "-12.30"}, "-12.30", false},
		{"decimal", Field{Text: "1e5"}, nil, true},
		{"date", Field{Text: "2026-11-11"}, time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC), false},
		{
			"timestamp with time zone",
			Field{Text: "2026-11-11T10:00:00-03:00"},
			time.Date(2026, 11, 11, 13, 0, 0, 0, time.UTC),
			false,
		},
		{"datetime", Field{Text: "tomorrow"}, nil, true},
		{"jsonb", Field{Text: `{"a":1}`}, `{"a":1}`, false},
		{"json", Field{Text: `{a`}, nil, true},
		{"blob", Field{Text: "x"}, nil, true},
		{"varchar(20)", Field{Text: "  keep spaces "}, "  keep spaces ", false},
		{"integer", Field{Null: true}, nil, true},
	}

	for _, tt := range tests {
		got, err := coerce(Column{Name: "c", Type: tt.typ}, tt.f)
		if (err != nil) != tt.bad {
			t.Errorf("coerce(%s, %+v) error = %v, want error %v", tt.typ, tt.f, err, tt.bad)
			continue
		}

		if err != nil && !strings.HasPrefix(err.Error(), "invalid input") {
			t.Errorf("coerce(%s) error %v is not an input error", tt.typ, err)
		}

		if !tt.bad && !cmp.Equal(got, tt.want) {
			t.Errorf("coerce(%s, %+v) = %#v, want %#v", tt.typ, tt.f, got, tt.want)
		}
	}

	if v, err := coerce(Column{Name: "c", Type: "integer", Nullable: true}, Field{Null: true}); err != nil || v != nil {
		t.Errorf("NULL on a nullable column = %v, %v", v, err)
	}
}

func TestSQLWriteFlows(t *testing.T) {
	t.Parallel()

	h, s, _, c := writeStudio(t, true)

	// Insert: an empty column gets the default, NULL and typed values are coerced.
	rec := postForm(s, c, "/s/db/t/items", url.Values{
		"col.id": {"2"}, "col.qty": {"3"}, "col.price": {"9.50"}, "col.active": {"true"}, "col.born": {"2026-01-02"},
		"col.meta": {`{"k":1}`}, "col.note": {""}, "null.note": {"1"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("insert = %d %s", rec.Code, rec.Body.String())
	}

	rs, err := h.src.Query(
		t.Context(),
		`SELECT qty, price, active, born, meta, note IS NULL FROM items WHERE id = 2`,
		false,
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
	rec = postForm(s, c, "/s/db/t/items/r?k=2", url.Values{
		"action": {"update"}, "col.qty": {"8"}, "col.note": {"edited"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("update = %d %s", rec.Code, rec.Body.String())
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
	rec = postForm(s, c, "/s/db/t/items/r?k=999", url.Values{"action": {"update"}, "col.qty": {"1"}})
	if rec.Code == http.StatusSeeOther {
		t.Error("an update of a missing row succeeded")
	}

	// Delete with the typed key.
	rec = postForm(s, c, "/s/db/t/items/r?k=2", url.Values{"action": {"delete"}, "confirm": {"2"}})
	if rec.Code != http.StatusSeeOther || h.count1(t, `SELECT COUNT(*) FROM items`) != 1 {
		t.Errorf("delete = %d, items = %d", rec.Code, h.count1(t, `SELECT COUNT(*) FROM items`))
	}

	// A composite key is typed back joined by commas.
	rec = postForm(s, c, "/s/db/t/pairs/r?k=1&k=2", url.Values{"action": {"delete"}, "confirm": {"1,2"}})
	if rec.Code != http.StatusSeeOther || h.count1(t, `SELECT COUNT(*) FROM pairs`) != 2 {
		t.Errorf("composite delete = %d", rec.Code)
	}
}

func TestSecurity_A01_StudioSQLWriteRoutesAbsentByDefault(t *testing.T) {
	t.Parallel()

	h, s, _, c := writeStudio(t, false)

	for _, req := range []struct{ target, action string }{
		{"/s/db/t/users", ""},
		{"/s/db/t/users/r?k=1", "delete"},
		{"/s/db/t/users/r?k=1", "update"},
	} {
		rec := postForm(s, c, req.target, url.Values{
			"action": {req.action}, "confirm": {"1"}, "col.name": {"x"}, "col.id": {"90"},
		})
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s without --write = %d, want 404 or 405", req.target, rec.Code)
		}
	}

	if code, _ := get(s, c, "/s/db/t/users/new"); code != http.StatusNotFound {
		t.Errorf("new-row form without --write = %d, want 404", code)
	}

	if h.users(t) != 5 {
		t.Errorf("a write reached the table: users = %d", h.users(t))
	}

	for _, target := range []string{"/s/db/t/users", "/s/db/t/users/r?k=1"} {
		_, body := get(s, c, target)
		if strings.Contains(body, `method="post"`) || strings.Contains(body, "/new") {
			t.Errorf("%s offers a write without --write", target)
		}
	}
}

func TestSecurity_A01_StudioSQLWriteNeedsPrimaryKey(t *testing.T) {
	t.Parallel()

	h, s, logs, c := writeStudio(t, true)

	if code, _ := get(s, c, "/s/db/t/log/new"); code != http.StatusNotFound {
		t.Errorf("new-row form of a table without a primary key = %d, want 404", code)
	}

	for _, form := range []url.Values{{"col.line": {"two"}}, {"action": {"delete"}, "confirm": {""}}} {
		for _, target := range []string{"/s/db/t/log", "/s/db/t/log/r?k=one"} {
			if rec := postForm(s, c, target, form); rec.Code != http.StatusNotFound {
				t.Errorf("POST %s on a table without a primary key = %d, want 404", target, rec.Code)
			}
		}
	}

	if h.count1(t, `SELECT COUNT(*) FROM log`) != 1 {
		t.Error("a table without a primary key was written")
	}

	if _, body := get(s, c, "/s/db/t/log"); strings.Contains(body, "New row") {
		t.Error("the grid offers a new row for a table without a primary key")
	}

	if strings.Contains(logs.String(), "ok=true") {
		t.Errorf("a refused write was audited as ok: %s", logs.String())
	}
}

func TestSecurity_A01_StudioSQLDeleteNeedsTypedConfirmation(t *testing.T) {
	t.Parallel()

	h, s, logs, c := writeStudio(t, true)

	for name, form := range map[string]url.Values{
		"missing": {"action": {"delete"}},
		"wrong":   {"action": {"delete"}, "confirm": {"2"}},
		"empty":   {"action": {"delete"}, "confirm": {""}},
	} {
		if rec := postForm(s, c, "/s/db/t/users/r?k=1", form); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s confirmation = %d, want 422", name, rec.Code)
		}
	}

	if h.users(t) != 5 {
		t.Fatalf("a row was deleted without confirmation: users = %d", h.users(t))
	}

	if !strings.Contains(logs.String(), "ok=false") || strings.Contains(logs.String(), "ok=true") {
		t.Errorf("refused deletes must be audited as failures: %s", logs.String())
	}

	if rec := postForm(
		s,
		c,
		"/s/db/t/users/r?k=1",
		url.Values{"action": {"delete"}, "confirm": {"1"}},
	); rec.Code != http.StatusSeeOther ||
		h.users(t) != 4 {
		t.Errorf("confirmed delete = %d, users = %d", rec.Code, h.users(t))
	}
}

func TestSecurity_A03_StudioSQLWriteRejectsUnknownColumns(t *testing.T) {
	t.Parallel()

	h, s, _, c := writeStudio(t, true)

	for name, req := range map[string]struct {
		target string
		form   url.Values
	}{
		"insert":         {"/s/db/t/users", url.Values{"col.id": {"60"}, "col.name": {"x"}, "col.nope": {"1"}}},
		"insert null":    {"/s/db/t/users", url.Values{"col.id": {"61"}, "col.name": {"x"}, "null.nope": {"1"}}},
		"injected name":  {"/s/db/t/users", url.Values{"col.name) VALUES ('x'); --": {"1"}}},
		"update unknown": {"/s/db/t/users/r?k=1", url.Values{"action": {"update"}, "col.nope": {"1"}}},
		"update the key": {"/s/db/t/users/r?k=1", url.Values{"action": {"update"}, "col.id": {"77"}}},
		"update no cols": {"/s/db/t/users/r?k=1", url.Values{"action": {"update"}}},
	} {
		if rec := postForm(s, c, req.target, req.form); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d, want 422", name, rec.Code)
		}
	}

	if n := h.count.n.Load(); n != 0 {
		t.Errorf("%d statements were sent for refused writes, want none", n)
	}

	if h.users(t) != 5 {
		t.Error("the users table changed")
	}
}

func TestSecurity_A01_StudioSQLWriteRejectsBadValues(t *testing.T) {
	t.Parallel()

	h, s, _, c := writeStudio(t, true)

	for name, form := range map[string]url.Values{
		"integer": {"col.id": {"5"}, "col.qty": {"many"}},
		"null":    {"col.id": {"5"}, "col.qty": {""}, "null.qty": {"1"}},
		"date":    {"col.id": {"5"}, "col.qty": {"1"}, "col.born": {"soon"}},
		"json":    {"col.id": {"5"}, "col.qty": {"1"}, "col.meta": {"{"}},
		"blob":    {"col.id": {"5"}, "col.qty": {"1"}, "col.raw": {"AAEC"}},
	} {
		if rec := postForm(s, c, "/s/db/t/items", form); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d, want 422", name, rec.Code)
		}
	}

	if sent := h.count.n.Load(); sent != 0 || h.count1(t, `SELECT COUNT(*) FROM items`) != 1 {
		t.Errorf("a bad value reached the database (statements: %d)", sent)
	}
}

func TestSecurity_A09_StudioSQLWritesAreAuditedWithoutValues(t *testing.T) {
	t.Parallel()

	_, s, logs, c := writeStudio(t, true)

	postForm(s, c, "/s/db/t/users", url.Values{"col.id": {"70"}, "col.name": {"S3cretValue"}})
	postForm(s, c, "/s/db/t/users/r?k=70", url.Values{"action": {"update"}, "col.name": {"Another-S3cret"}})
	postForm(s, c, "/s/db/t/users/r?k=70", url.Values{"action": {"delete"}, "confirm": {"70"}})

	out := logs.String()
	for _, want := range []string{"action=insert", "action=update", "action=delete", "target=users", "columns=id,name", "key=70", "ok=true"} {
		if !strings.Contains(out, want) {
			t.Errorf("the audit lacks %q: %s", want, out)
		}
	}

	if strings.Contains(out, "S3cret") {
		t.Errorf("a written value reached the log: %s", out)
	}
}

func TestSecurity_A01_StudioSQLWriteNeedsSessionAndSameOrigin(t *testing.T) {
	t.Parallel()

	h, s, _, c := writeStudio(t, true)

	form := url.Values{"action": {"delete"}, "confirm": {"1"}}

	if rec := postForm(s, nil, "/s/db/t/users/r?k=1", form); rec.Code != http.StatusUnauthorized {
		t.Errorf("POST without the session = %d, want 401", rec.Code)
	}

	r := httptest.NewRequest(
		http.MethodPost,
		"http://127.0.0.1:4984/s/db/t/users/r?k=1",
		strings.NewReader(form.Encode()),
	)
	r.RemoteAddr = "127.0.0.1:50000"
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "https://evil.example")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	r.AddCookie(c)

	if rec := serve(s, r); rec.Code != http.StatusForbidden {
		t.Errorf("cross-site POST = %d, want 403", rec.Code)
	}

	if h.users(t) != 5 {
		t.Error("a row was deleted by a request without a valid session or origin")
	}
}

func TestSQLRowPageEditsOnlyWhatItShowsInFull(t *testing.T) {
	t.Parallel()

	h, s, _, c := writeStudio(t, true)

	if _, err := h.db.ExecContext(
		t.Context(),
		`INSERT INTO items (id, qty, note) VALUES (3, 1, ?)`,
		strings.Repeat("x", detailCellMax+10),
	); err != nil {
		t.Fatal(err)
	}

	_, body := get(s, c, "/s/db/t/items/r?k=3")
	if strings.Contains(body, `name="col.note"`) {
		t.Error("a cut value is offered for editing: saving it would write the cut text back")
	}

	if !strings.Contains(body, `name="col.qty"`) || strings.Contains(body, `name="col.id"`) {
		t.Error("the form must offer the editable columns and not the primary key")
	}

	_, body = get(s, c, "/s/db/t/items/r?k=1")
	if strings.Contains(body, `name="col.raw"`) {
		t.Error("a binary value is offered for editing")
	}
}

func TestSQLRowPageShowsIncomingReferences(t *testing.T) {
	t.Parallel()

	_, s, _, c := writeStudio(t, false)

	code, body := get(s, c, "/s/db/t/owners/r?k=1")
	if code != http.StatusOK || !strings.Contains(body, "Referenced by") ||
		!strings.Contains(body, "/s/db/t/pets?f=owner_id%3Aeq%3A1") {
		t.Errorf("incoming references = %d %s", code, body)
	}

	if code, body := get(s, c, "/s/db/t/pets?f=owner_id:eq:1"); code != 200 || !strings.Contains(body, "owner_id") {
		t.Errorf("the link target = %d", code)
	}
}

func TestSQLTablesShowRowEstimates(t *testing.T) {
	t.Parallel()

	_, s, _, c := writeStudio(t, false)

	if _, body := get(s, c, "/s/db"); !strings.Contains(body, "5 rows") || !strings.Contains(body, "1 rows") {
		t.Errorf("tables page lacks row counts: %s", body)
	}
}

func TestSQLUnknownTableIsNotFound(t *testing.T) {
	t.Parallel()

	_, s, _, c := writeStudio(t, true)

	for _, target := range []string{
		"/s/db/t/users%3BDROP%20TABLE%20users", "/s/db/t/%22users%22", "/s/db/t/nope/r?k=1", "/s/db/t/nope/export",
		"/s/db/t/nope/new",
	} {
		if code, _ := get(s, c, target); code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", target, code)
		}
	}
}

func TestSQLExport(t *testing.T) {
	t.Parallel()

	h, s, _, c := writeStudio(t, false)

	long := strings.Repeat("é", 400)
	if _, err := h.db.ExecContext(t.Context(), `UPDATE users SET name = ? WHERE id = 5`, "=HYPERLINK(1)"); err != nil {
		t.Fatal(err)
	}

	if _, err := h.db.ExecContext(t.Context(), `UPDATE users SET bio = ? WHERE id = 1`, long); err != nil {
		t.Fatal(err)
	}

	r := request(http.MethodGet, "/s/db/t/users/export")
	r.AddCookie(c)
	rec := serve(s, r)

	lines := strings.Split(strings.TrimSpace(rec.Body.String()), "\n")
	if rec.Code != 200 || len(lines) != 5 || !strings.HasPrefix(lines[0], `{"id":"1","name":"ada","bio":"`+long) {
		t.Fatalf("jsonl = %d, %d lines, first %.80q", rec.Code, len(lines), lines[0])
	}

	if !strings.Contains(lines[1], `"bio":"50%_off"`) ||
		!strings.Contains(rec.Header().Get("Content-Disposition"), "users.jsonl") {
		t.Errorf("jsonl line 2 / disposition = %q / %q", lines[1], rec.Header().Get("Content-Disposition"))
	}

	// CSV: formulas are neutralized, NULL is empty, the filter applies.
	r = request(http.MethodGet, "/s/db/t/users/export?format=csv&f=id:gte:4&sort=name")
	r.AddCookie(c)

	rows, err := csv.NewReader(strings.NewReader(serve(s, r).Body.String())).ReadAll()
	if err != nil || len(rows) != 3 || rows[0][1] != "name" ||
		rows[1][1] != "'=HYPERLINK(1)" && rows[2][1] != "'=HYPERLINK(1)" {
		t.Errorf("csv = %v, %v", rows, err)
	}

	// The cap and the trailer.
	s.exportMax = 2

	r = request(http.MethodGet, "/s/db/t/users/export")
	r.AddCookie(c)
	rec = serve(s, r)

	if n := strings.Count(rec.Body.String(), "\n"); n != 2 || rec.Result().Trailer.Get("X-Studio-Truncated") != "true" {
		t.Errorf("capped export: %d lines, trailer %q", n, rec.Result().Trailer.Get("X-Studio-Truncated"))
	}

	// Refusals are normal error pages, before anything is streamed.
	for target, want := range map[string]int{
		"/s/db/t/users/export?format=xml":    http.StatusUnprocessableEntity,
		"/s/db/t/users/export?f=nope:eq:1":   http.StatusUnprocessableEntity,
		"/s/db/t/users/export?sort=%22id%22": http.StatusUnprocessableEntity,
	} {
		if code, _ := get(s, c, target); code != want {
			t.Errorf("GET %s = %d, want %d", target, code, want)
		}
	}
}
