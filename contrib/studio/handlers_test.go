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
)

// harness is a Studio over a fake MongoDB and a recorded Redis.
type harness struct {
	s     *Studio
	mongo *MongoDB
	fm    *fakeMongo
	redis *RedisDB
	rec   *recorder
	logs  *bytes.Buffer
	login *http.Cookie
}

func newHarness(t *testing.T, write bool) *harness {
	t.Helper()

	m, fm := newMongo(t, write, rawDoc(t, int32(1), "ada"), rawDoc(t, int32(2), "linus"))
	rdb, rec := fakeRedis(t, "string")

	h := &harness{mongo: m, fm: fm, redis: rdb, rec: rec, logs: &bytes.Buffer{}}

	s, err := New(Config{
		Sources: []Source{m, rdb}, Version: "v0.0.0-test", Write: write,
		Log: slog.New(slog.NewTextHandler(h.logs, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}

	h.s, h.login = s, session(t, s)

	return h
}

func (h *harness) do(method, target string, form url.Values) *httptest.ResponseRecorder {
	var r *http.Request

	if form != nil {
		r = request(method, target)
		r.Body = http.NoBody
		r = httptest.NewRequest(method, "http://127.0.0.1:4984"+target, strings.NewReader(form.Encode()))
		r.RemoteAddr = "127.0.0.1:50000"
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		r = request(method, target)
	}

	r.AddCookie(h.login)

	return serve(h.s, r)
}

func TestSourcePagesDispatchByKind(t *testing.T) {
	t.Parallel()

	h := newHarness(t, false)

	if rec := h.do("GET", "/s/mongo", nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), "users") {
		t.Errorf("mongo source page = %d %q", rec.Code, rec.Body.String())
	}

	if rec := h.do(
		"GET",
		"/s/redis",
		nil,
	); rec.Code != 200 ||
		!strings.Contains(rec.Body.String(), "session") && !strings.Contains(rec.Body.String(), "Match") {
		t.Errorf("redis source page = %d %q", rec.Code, rec.Body.String())
	}

	if rec := h.do("GET", "/s/nope", nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown source = %d, want 404", rec.Code)
	}

	if rec := h.do("GET", "/s/mongo/c/users", nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), "ada") {
		t.Errorf("documents = %d", rec.Code)
	}

	if rec := h.do("GET", "/s/mongo/c/missing", nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown collection = %d, want 404", rec.Code)
	}
}

func TestSecurity_A03_StudioEscapesDocumentHTML(t *testing.T) {
	t.Parallel()

	m, _ := newMongo(t, false, rawDoc(t, int32(1), "<script>alert(1)</script>"))
	s, err := New(Config{Sources: []Source{m}, Version: "t"})
	if err != nil {
		t.Fatal(err)
	}

	r := request("GET", "/s/mongo/c/users")
	r.AddCookie(session(t, s))

	body := serve(s, r).Body.String()
	if strings.Contains(body, "<script>alert") || !strings.Contains(body, "&lt;script&gt;") {
		t.Errorf("document HTML is not escaped: %q", body)
	}
}

func TestSecurity_A01_StudioWriteRoutesAbsentByDefault(t *testing.T) {
	t.Parallel()

	h := newHarness(t, false)

	for _, target := range []string{
		"/s/mongo/c/users", "/s/mongo/c/users/d/%7B%22$numberInt%22:%221%22%7D", "/s/redis/k/session:abc",
	} {
		rec := h.do("POST", target, url.Values{"action": {"delete"}, "confirm": {"1"}, "doc": {"{}"}})
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s without --write = %d, want 404 or 405", target, rec.Code)
		}
	}

	if len(h.fm.calls) != 0 && h.fm.calls[0] != "find" {
		t.Errorf("a write reached MongoDB: %v", h.fm.calls)
	}

	for _, c := range h.rec.names() {
		if c == "DEL" || c == "SET" {
			t.Errorf("a write reached Redis: %v", h.rec.names())
		}
	}

	// The pages carry no forms that change data.
	for _, target := range []string{"/s/mongo/c/users", "/s/redis/k/session:abc"} {
		if body := h.do("GET", target, nil).Body.String(); strings.Contains(body, `method="post"`) {
			t.Errorf("%s has a write form without --write", target)
		}
	}
}

func TestSecurity_A01_StudioDeleteNeedsTypedConfirmation(t *testing.T) {
	t.Parallel()

	h := newHarness(t, true)
	docTarget := "/s/mongo/c/users/d/" + url.PathEscape(`{"$numberInt":"1"}`)

	for name, form := range map[string]url.Values{
		"no confirm":    {"action": {"delete"}},
		"wrong confirm": {"action": {"delete"}, "confirm": {"2"}},
		"empty confirm": {"action": {"delete"}, "confirm": {""}},
	} {
		if rec := h.do("POST", docTarget, form); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("document delete, %s: %d, want 422", name, rec.Code)
		}

		if rec := h.do("POST", "/s/redis/k/session:abc", form); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("key delete, %s: %d, want 422", name, rec.Code)
		}
	}

	for _, c := range h.fm.calls {
		if c == "remove" {
			t.Error("a document was deleted without confirmation")
		}
	}

	for _, c := range h.rec.names() {
		if c == "DEL" {
			t.Error("a key was deleted without confirmation")
		}
	}

	// The right confirmation deletes.
	if rec := h.do(
		"POST",
		docTarget,
		url.Values{"action": {"delete"}, "confirm": {"1"}},
	); rec.Code != http.StatusSeeOther {
		t.Errorf("confirmed document delete = %d", rec.Code)
	}

	if rec := h.do(
		"POST",
		"/s/redis/k/session:abc",
		url.Values{"action": {"delete"}, "confirm": {"session:abc"}},
	); rec.Code != http.StatusSeeOther {
		t.Errorf("confirmed key delete = %d", rec.Code)
	}
}

func TestSecurity_A09_StudioAuditsEveryWrite(t *testing.T) {
	t.Parallel()

	h := newHarness(t, true)

	secret := "hunter2-document-value"
	docTarget := "/s/mongo/c/users/d/" + url.PathEscape(`{"$numberInt":"1"}`)

	h.do("POST", docTarget, url.Values{"action": {"replace"}, "doc": {`{"name":"` + secret + `"}`}})
	h.do("POST", docTarget, url.Values{"action": {"delete"}, "confirm": {"nope"}}) // refused
	h.do("POST", "/s/redis/k/session:abc", url.Values{"action": {"set"}, "value": {secret}})
	h.do("POST", "/s/mongo/c/users", url.Values{"doc": {`{"v":"` + secret + `"}`}})

	lines := strings.Split(strings.TrimSpace(h.logs.String()), "\n")

	n := 0

	for _, l := range lines {
		if strings.Contains(l, `msg="studio write"`) {
			n++
		}
	}

	if n != 4 {
		t.Errorf("%d audit lines for 4 writes (one refused):\n%s", n, h.logs.String())
	}

	if strings.Contains(h.logs.String(), secret) {
		t.Errorf("an audit line carries a written value:\n%s", h.logs.String())
	}

	if !strings.Contains(h.logs.String(), "ok=false") || !strings.Contains(h.logs.String(), "ok=true") {
		t.Errorf("audit lines must record both outcomes:\n%s", h.logs.String())
	}
}

func TestWriteRedisSetKeepsTTLAndTTLAction(t *testing.T) {
	t.Parallel()

	h := newHarness(t, true)

	if rec := h.do(
		"POST",
		"/s/redis/k/k1",
		url.Values{"action": {"set"}, "value": {"v"}},
	); rec.Code != http.StatusSeeOther {
		t.Errorf("set = %d", rec.Code)
	}

	if rec := h.do(
		"POST",
		"/s/redis/k/k1",
		url.Values{"action": {"ttl"}, "seconds": {"abc"}},
	); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("ttl with text = %d, want 422", rec.Code)
	}

	if rec := h.do(
		"POST",
		"/s/redis/k/k1",
		url.Values{"action": {"ttl"}, "seconds": {"0"}},
	); rec.Code != http.StatusSeeOther {
		t.Errorf("ttl 0 = %d", rec.Code)
	}

	got := strings.Join(h.rec.names(), " ")
	if !strings.Contains(got, "SET") || !strings.Contains(got, "PERSIST") {
		t.Errorf("commands = %s, want SET and PERSIST", got)
	}
}

func TestExportFilenameAndCSVFormulas(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{
		"users": "users.jsonl", "../etc/passwd": "_etc_passwd.jsonl", `a"b;c`: "a_b_c.jsonl", "": "export.jsonl", "..": "export.jsonl",
		"résumé": "r_sum_.jsonl",
	} {
		if got := exportFilename(in, "jsonl"); got != want {
			t.Errorf("exportFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSecurity_A03_StudioExportNeutralizesFormulas(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	rows := [][]string{{"=1+1", "+x", "-x", "@x", "plain", ""}}
	if err := writeCSV(csv.NewWriter(&buf), []string{"a", "b", "c", "d", "e", "f"}, rows); err != nil {
		t.Fatal(err)
	}

	got := buf.String()
	if !strings.Contains(got, "'=1+1,'+x,'-x,'@x,plain,") {
		t.Errorf("csv = %q, want formula cells prefixed with a quote", got)
	}
}

func TestExportDocumentsStreamsAndCaps(t *testing.T) {
	t.Parallel()

	h := newHarness(t, false)

	rec := h.do("GET", "/s/mongo/c/users/export?format=json", nil)
	if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Disposition"), `filename="users.jsonl"`) {
		t.Fatalf("export = %d %v", rec.Code, rec.Header())
	}

	lines := strings.Split(strings.TrimSpace(rec.Body.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"name":"ada"`) {
		t.Errorf("lines = %q, want 2 JSON lines", lines)
	}

	h.s.exportMax = 1

	rec = h.do("GET", "/s/mongo/c/users/export?format=json", nil)
	if n := len(strings.Split(strings.TrimSpace(rec.Body.String()), "\n")); n != 1 {
		t.Errorf("capped export has %d rows, want 1", n)
	}

	if got := rec.Result().Trailer.Get("X-Studio-Truncated"); got != "true" {
		t.Errorf("trailer X-Studio-Truncated = %q, want true", got)
	}

	if rec := h.do("GET", "/s/mongo/c/users/export?format=xml", nil); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("format xml = %d, want 422", rec.Code)
	}
}
