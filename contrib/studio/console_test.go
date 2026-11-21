package studio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const endlessQuery = "WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM c) SELECT count(*) FROM c"

func TestConsoleKind(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct{ text, family, want string }{
		{"select 1", "sqlite", "SELECT"},
		{"  -- note\n UPDATE t SET a = 1", "sqlite", "UPDATE"},
		{"/* c */ delete from t", "postgres", "DELETE"},
		{"WITH x AS (SELECT 1) SELECT * FROM x", "postgres", "WITH"},
		{"WITH x AS (DELETE FROM t RETURNING *) SELECT * FROM x", "postgres", "DELETE"},
		{"EXPLAIN QUERY PLAN SELECT 1", "sqlite", "EXPLAIN"},
		{"PRAGMA table_info(t)", "sqlite", "PRAGMA"},
		{"drop table t", "mysql", "DROP"},
		{"", "mysql", ""},
	} {
		if got := statementKind(tt.text, tt.family); got != tt.want {
			t.Errorf("statementKind(%q, %s) = %q, want %q", tt.text, tt.family, got, tt.want)
		}
	}

	h := newSQLHarness(t)

	rs, err := h.src.Query(t.Context(), "SELECT 1", false)
	if err != nil || rs.Kind != "SELECT" {
		t.Errorf("Query kind = %q, %v, want SELECT", rs.Kind, err)
	}
}

func TestConsoleKindIsInTheAPIResponse(t *testing.T) {
	t.Parallel()

	_, s, _, c := writeStudio(t, false)

	rec := apiDo(s, c, http.MethodPost, "/api/v1/sources/db/query", queryBodyJSON(t, "SELECT 1", false))

	var got struct {
		Kind string `json:"kind"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Kind != "SELECT" {
		t.Errorf("kind = %q, %v, want SELECT (%s)", got.Kind, err, rec.Body)
	}
}

func TestConsoleCancel(t *testing.T) {
	t.Parallel()

	h := newSQLHarness(t)
	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan error, 1)

	go func() {
		_, err := h.src.Query(ctx, endlessQuery, false)
		done <- err
	}()

	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Error("the endless query returned no error")
		}

		if got := errorClass(err); got != "canceled" {
			t.Errorf("error class = %q, want canceled", got)
		}
	case <-time.After(time.Second):
		t.Fatal("the query was still running one second after the cancel")
	}
}

func TestConsoleCancelThroughTheRequest(t *testing.T) {
	t.Parallel()

	_, s, _, c := writeStudio(t, false)
	ctx, cancel := context.WithCancel(t.Context())

	r := httptest.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1:4984/api/v1/sources/db/query",
		strings.NewReader(queryBodyJSON(t, endlessQuery, false)))
	r.RemoteAddr = "127.0.0.1:50000"
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(c)

	done := make(chan *httptest.ResponseRecorder, 1)

	go func() { done <- serve(s, r) }()

	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case rec := <-done:
		if rec.Code != statusClientClosed || !strings.Contains(rec.Body.String(), `"canceled"`) {
			t.Errorf("aborted request = %d %s, want 499 canceled", rec.Code, rec.Body)
		}
	case <-time.After(time.Second):
		t.Fatal("the handler was still running one second after the request was aborted")
	}
}

func TestConsoleResultSizeLimit(t *testing.T) {
	t.Parallel()

	h := newSQLHarness(t)
	cols := strings.TrimSuffix(strings.Repeat("hex(zeroblob(100)),", 60), ",")

	rs, err := h.src.Query(t.Context(),
		"WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM c WHERE x < 1000) SELECT "+cols+" FROM c", false)
	if err != nil {
		t.Fatal(err)
	}

	if !rs.Truncate || len(rs.Rows) >= consoleMaxRows {
		t.Errorf("rows = %d, truncate = %v, want the size limit to cut the result", len(rs.Rows), rs.Truncate)
	}
}
