package web

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/errs"
)

var update = flag.Bool("update", false, "rewrite golden files")

// fixedStackError is an error with a deterministic stack for golden output.
type fixedStackError struct{ err error }

func (f fixedStackError) Error() string { return f.err.Error() }
func (f fixedStackError) Unwrap() error { return f.err }
func (fixedStackError) StackFrames() []errs.Frame {
	return []errs.Frame{
		{Function: "example.com/blog/app/controllers.Show", File: "testdata/debug_source.go.txt", Line: 6},
		{Function: "github.com/AlexGalhardo/vukem/router.(*Router).serve", File: "/vukem/router/router.go", Line: 210},
		{Function: "net/http.HandlerFunc.ServeHTTP", File: "/go/src/net/http/server.go", Line: 2322},
	}
}

func debugRequest(t *testing.T) *http.Request {
	t.Helper()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/posts/7?draft=1", http.NoBody)
	r.Header.Set("Accept", "text/html")
	r.Header.Set("Authorization", "Bearer secret-token")
	r.Header.Set("Cookie", "vukem_session=secret-session")
	r.Header.Set("User-Agent", "test")

	return r.WithContext(WithCSPNonce(WithRequestID(r.Context(), "req-1"), "n0nce"))
}

func debugErr() error {
	cause := fixedStackError{errors.New("database is locked <b>")}
	return errs.Wrap(http.StatusInternalServerError, fmt.Errorf("load post 7: %w", cause))
}

func golden(t *testing.T, name, got string) {
	t.Helper()

	p := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(p, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(strings.ReplaceAll(string(want), "\r\n", "\n"), got); diff != "" {
		t.Errorf("%s mismatch (-want +got):\n%s", name, diff)
	}
}

func TestDebugPageGolden(t *testing.T) {
	t.Parallel()

	c, rec := newCtx(t, debugRequest(t), &Services{Log: slog.New(slog.DiscardHandler), Debug: true})
	c.SetRouteName("posts.update")
	c.HandleError(debugErr())

	if rec.Code != http.StatusInternalServerError || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("debug page = %d %v", rec.Code, rec.Header())
	}

	golden(t, "debug.golden.html", strings.ReplaceAll(rec.Body.String(), runtime.Version(), "GOVERSION"))
}

func TestDebugReportANSI(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	c, _ := newCtx(t, debugRequest(t), &Services{Log: slog.New(slog.DiscardHandler), Debug: true, DebugOut: &out})
	c.HandleError(debugErr())

	golden(t, "debug.golden.ansi", out.String())

	out.Reset()

	c, _ = newCtx(t, debugRequest(t), &Services{Log: slog.New(slog.DiscardHandler), Debug: true, DebugOut: &out})
	c.HandleError(errs.ErrNotFound)

	if out.Len() != 0 {
		t.Errorf("4xx wrote a debug report: %q", out.String())
	}
}

func TestDebugPageOnlyForBrowsers(t *testing.T) {
	t.Parallel()

	r := debugRequest(t)
	r.Header.Set("Accept", "application/json")

	_, rec := newCtx(t, r, &Services{Log: slog.New(slog.DiscardHandler), Debug: true})
	NewCtx(rec, r, &Services{Log: slog.New(slog.DiscardHandler), Debug: true}).HandleError(debugErr())

	if strings.Contains(rec.Body.String(), "<html") || !strings.Contains(rec.Body.String(), "database is locked") {
		t.Errorf("JSON debug response = %s", rec.Body.String())
	}
}

func TestSecurity_A02_DebugPageOffInProduction(t *testing.T) {
	t.Parallel()

	c, rec := newCtx(t, debugRequest(t), &Services{Log: slog.New(slog.DiscardHandler)})
	c.HandleError(debugErr())

	body := rec.Body.String()
	for _, leak := range []string{"database is locked", "controllers.Show", "debug_source", "return err", "secret"} {
		if strings.Contains(body, leak) {
			t.Errorf("production error page leaks %q:\n%s", leak, body)
		}
	}
}

func TestSecurity_A09_DebugPageRedactsSecrets(t *testing.T) {
	t.Parallel()

	c, rec := newCtx(t, debugRequest(t), &Services{Log: slog.New(slog.DiscardHandler), Debug: true})
	c.HandleError(debugErr())

	body := rec.Body.String()
	if strings.Contains(body, "secret-token") || strings.Contains(body, "secret-session") {
		t.Errorf("debug page shows credentials:\n%s", body)
	}

	if strings.Contains(body, "<b>") || strings.Contains(body, "<script>alert") {
		t.Errorf("debug page does not escape error text or source")
	}
}
