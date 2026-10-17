package middleware

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// unwrapOnly is a middleware writer that supports http.ResponseController
// through Unwrap but does not implement Hijack itself.
type unwrapOnly struct{ http.ResponseWriter }

func (u unwrapOnly) Unwrap() http.ResponseWriter { return u.ResponseWriter }

func TestGzipHijackThroughWrappers(t *testing.T) {
	t.Parallel()

	var serverLog bytes.Buffer

	h := Gzip()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// A streaming handler that already started (headers buffered in
		// the gzip writer) and then drops the connection.
		w.WriteHeader(http.StatusOK)

		conn, buf, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Errorf("Hijack() = %v, want nil", err)

			return
		}
		defer conn.Close()

		_, _ = buf.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 6\r\nConnection: close\r\n\r\nraw ok")
		_ = buf.Flush()
	}))

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(unwrapOnly{w}, r)
	}))
	srv.Config.ErrorLog = log.New(&serverLog, "", 0)
	srv.Start()
	t.Cleanup(srv.Close)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("Accept-Encoding", "gzip")

	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	body, _ := io.ReadAll(res.Body)
	if string(body) != "raw ok" {
		t.Errorf("body = %q, want %q", body, "raw ok")
	}

	srv.Close()

	if strings.Contains(serverLog.String(), "hijacked") {
		t.Errorf("server log = %q, want no write on the hijacked connection", serverLog.String())
	}
}
