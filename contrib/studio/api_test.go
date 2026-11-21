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
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
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
