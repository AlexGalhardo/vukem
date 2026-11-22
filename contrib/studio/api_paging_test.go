package studio

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"go.mongodb.org/mongo-driver/v2/bson"
)

const (
	usersRows = "/api/v1/sources/db/tables/users/rows"
	mongoDocs = "/api/v1/sources/mongo/collections/users/documents"
)

// pageIDs reads the first cell of every row of a rows response, and hasMore.
func pageIDs(t *testing.T, body string) ([]string, bool) {
	t.Helper()

	var res struct {
		Rows [][]struct {
			T string `json:"t"`
		} `json:"rows"`
		HasMore bool `json:"hasMore"`
	}
	if err := json.Unmarshal([]byte(body), &res); err != nil {
		t.Fatalf("rows response is not JSON: %v\n%s", err, body)
	}

	ids := make([]string, 0, len(res.Rows))
	for _, row := range res.Rows {
		ids = append(ids, row[0].T)
	}

	return ids, res.HasMore
}

func TestAPIPageSizeIsBounded(t *testing.T) {
	t.Parallel()

	_, h := mixedStudio(t, false)

	for _, base := range []string{usersRows, mongoDocs} {
		for _, n := range []string{"1", "25", "50", "75", "100"} {
			if rec := apiGet(h.s, h.login, base+"?perPage="+n); rec.Code != http.StatusOK {
				t.Errorf("GET %s?perPage=%s = %d (%s), want 200", base, n, rec.Code, rec.Body)
			}
		}

		for _, n := range []string{"101", "200", "1000", "9999999999999999999", "0", "-1", "abc", "2.5", " 25"} {
			target := base + "?perPage=" + url.QueryEscape(n)
			if rec := apiGet(h.s, h.login, target); rec.Code != http.StatusUnprocessableEntity {
				t.Errorf("GET %s = %d, want 422", target, rec.Code)
			}
		}

		// Absent means the default, not an error.
		if rec := apiGet(h.s, h.login, base); rec.Code != http.StatusOK {
			t.Errorf("GET %s with no perPage = %d, want 200", base, rec.Code)
		}
	}
}

func TestAPIRowsAreReadByPageNumber(t *testing.T) {
	t.Parallel()

	_, h := mixedStudio(t, false)

	tests := []struct {
		name    string
		query   string
		want    []string
		hasMore bool
	}{
		{"page 1", "perPage=2&page=1", []string{"1", "2"}, true},
		{"page 2 of the primary key order", "perPage=2&page=2", []string{"3", "4"}, true},
		{"the last page is partial", "perPage=2&page=3", []string{"5"}, false},
		{"a page past the end is empty", "perPage=2&page=4", []string{}, false},
		{"descending", "perPage=2&page=2&desc=1", []string{"3", "2"}, true},
		{"a sorted view", "perPage=2&page=2&sort=name", []string{"5", "3"}, true},
		{"page 0 is the first page", "perPage=2&page=0", []string{"1", "2"}, true},
		{"a page that is not a number is the first", "perPage=2&page=x", []string{"1", "2"}, true},
		{"the filter applies before the page", "perPage=2&page=2&filter=id:gt:1", []string{"4", "5"}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := apiGet(h.s, h.login, usersRows+"?"+tc.query)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s = %d (%s)", tc.query, rec.Code, rec.Body)
			}

			got, hasMore := pageIDs(t, rec.Body.String())
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("ids of %s (-want +got):\n%s", tc.query, diff)
			}

			if hasMore != tc.hasMore {
				t.Errorf("hasMore of %s = %v, want %v", tc.query, hasMore, tc.hasMore)
			}
		})
	}
}

// The cursor of the earlier interface still works, and wins over a page number.
func TestAPIRowsCursorWinsOverPage(t *testing.T) {
	t.Parallel()

	_, h := mixedStudio(t, false)

	first := apiGet(h.s, h.login, usersRows+"?perPage=2")

	var p struct {
		Next string `json:"next"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &p); err != nil || p.Next == "" {
		t.Fatalf("first page = %s", first.Body)
	}

	rec := apiGet(h.s, h.login, usersRows+"?perPage=2&page=3&after="+url.QueryEscape(p.Next))
	if got, _ := pageIDs(t, rec.Body.String()); !cmp.Equal(got, []string{"3", "4"}) {
		t.Errorf("rows after the cursor with page=3 = %v, want [3 4]", got)
	}
}

func TestAPIDocumentsAreReadByPageNumber(t *testing.T) {
	// Not parallel: the fake MongoDB records the arguments of its last call.
	_, h := mixedStudio(t, false)

	tests := []struct {
		query string
		skip  int64
		limit int64
	}{
		{"perPage=25", 0, 26},
		{"perPage=25&page=3", 50, 26},
		{"perPage=100&page=2&sort=name", 100, 101},
		{"perPage=100&page=2&sort=_id&desc=1", 100, 101},
		// With a cursor the page number is not used.
		{"perPage=25&page=3&after=" + url.QueryEscape(`{"$numberInt":"1"}`), 0, 26},
	}

	for _, tc := range tests {
		if rec := apiGet(h.s, h.login, mongoDocs+"?"+tc.query); rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d (%s)", tc.query, rec.Code, rec.Body)
		}

		h.fm.mu.Lock()
		got := h.fm.findArgs
		h.fm.mu.Unlock()

		if got.skip != tc.skip || got.limit != tc.limit {
			t.Errorf("GET %s asked MongoDB for skip %d limit %d, want skip %d limit %d",
				tc.query, got.skip, got.limit, tc.skip, tc.limit)
		}
	}
}

func TestAPIDocumentCount(t *testing.T) {
	// Not parallel inside: the fake MongoDB sorts its collection list in place.
	_, h := mixedStudio(t, false)
	h.fm.counted = 27

	rec := apiGet(h.s, h.login, "/api/v1/sources/mongo/collections/users/count?filter="+url.QueryEscape("name:eq:ada"))
	if rec.Code != http.StatusOK {
		t.Fatalf("count = %d (%s)", rec.Code, rec.Body)
	}

	if got := decodeMap(t, rec.Body.String())["count"]; got != float64(27) {
		t.Errorf("count = %v, want 27", got)
	}

	if diff := cmp.Diff(
		bson.D{{Key: "name", Value: bson.D{{Key: "$eq", Value: "ada"}}}},
		h.fm.countFilter,
	); diff != "" {
		t.Errorf("the filter reached MongoDB (-want +got):\n%s", diff)
	}

	h.fm.countErr = errors.New("boom: a secret in the driver's text")
	if rec := apiGet(h.s, h.login, "/api/v1/sources/mongo/collections/users/count"); rec.Code == http.StatusOK ||
		strings.Contains(rec.Body.String(), "secret") {
		t.Errorf("a failed count = %d %s, want an error without the driver's text", rec.Code, rec.Body)
	}

	h.fm.countErr = context.DeadlineExceeded

	rec = apiGet(h.s, h.login, "/api/v1/sources/mongo/collections/users/count")
	if rec.Code != http.StatusOK || decodeMap(t, rec.Body.String())["timedOut"] != true {
		t.Errorf("a count that ran out of time = %d %s, want timedOut", rec.Code, rec.Body)
	}

	for target, want := range map[string]int{
		"/api/v1/sources/mongo/collections/ghost/count":             http.StatusNotFound,
		"/api/v1/sources/mongo/collections/users/count?filter=name": http.StatusUnprocessableEntity,
		"/api/v1/sources/db/collections/users/count":                http.StatusNotFound,
	} {
		if rec := apiGet(h.s, h.login, target); rec.Code != want {
			t.Errorf("GET %s = %d, want %d", target, rec.Code, want)
		}
	}
}

// An export is the whole filtered result, whatever page or size the browser was on.
func TestAPIExportIgnoresThePage(t *testing.T) {
	t.Parallel()

	_, h := mixedStudio(t, false)

	for _, query := range []string{"", "?page=2&perPage=1", "?page=3&perPage=2&filter=id:gt:0", "?perPage=1000"} {
		rec := apiGet(h.s, h.login, "/api/v1/sources/db/tables/users/export"+query)
		if lines := strings.Split(
			strings.TrimSpace(rec.Body.String()),
			"\n",
		); rec.Code != http.StatusOK ||
			len(lines) != 5 {
			t.Errorf("export%s = %d with %d lines, want all 5 rows", query, rec.Code, len(lines))
		}
	}
}

func TestPerPageParam(t *testing.T) {
	t.Parallel()

	for raw, want := range map[string]int{"": 0, "1": 1, "100": 100} {
		got, err := perPageParam(url.Values{"perPage": {raw}}, "perPage")
		if err != nil || got != want {
			t.Errorf("perPageParam(%q) = %d, %v, want %d", raw, got, err, want)
		}
	}

	for _, raw := range []string{"0", "101", "-5", "x", strconv.Itoa(maxAPIPerPage + 1)} {
		if _, err := perPageParam(url.Values{"perPage": {raw}}, "perPage"); !errors.Is(err, errInput) {
			t.Errorf("perPageParam(%q) error = %v, want an input error", raw, err)
		}
	}
}
