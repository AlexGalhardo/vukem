package vukem

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMain lets the test binary act as a fake "node" for ssr:start: with
// VUKEM_SSR_HELPER set it appends a line to that file and exits with
// VUKEM_SSR_EXIT.
func TestMain(m *testing.M) {
	if log := os.Getenv("VUKEM_SSR_HELPER"); log != "" {
		f, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = f.WriteString(strings.Join(os.Args[1:], " ") + "\n")
			_ = f.Close()
		}

		if os.Getenv("VUKEM_SSR_EXIT") == "0" {
			os.Exit(0)
		}

		os.Exit(3)
	}

	os.Exit(m.Run())
}

func TestSSRStartRestartsCrashedServer(t *testing.T) { //nolint:paralleltest // sets process environment
	dir := t.TempDir()
	bundle := filepath.Join(dir, "ssr.js")
	runs := filepath.Join(dir, "runs.log")

	if err := os.WriteFile(bundle, []byte("//"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("VUKEM_SSR_HELPER", runs)
	t.Setenv("VUKEM_SSR_EXIT", "3")

	app := newTestApp(t)
	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan error, 1)
	go func() {
		done <- runCommand(ctx, app, "ssr:start",
			[]string{"-bundle", bundle, "-runtime", os.Args[0], "-restart-delay", "10ms"})
	}()

	deadline := time.Now().Add(10 * time.Second)
	for {
		b, _ := os.ReadFile(runs)
		if strings.Count(string(b), bundle) >= 3 {
			break
		}

		if time.Now().After(deadline) {
			t.Fatalf("server not restarted, runs:\n%s", b)
		}

		time.Sleep(10 * time.Millisecond) //nolint:forbidigo // waits on real child processes
	}

	cancel()

	if err := <-done; err != nil {
		t.Errorf("ssr:start after cancel = %v", err)
	}

	t.Setenv("VUKEM_SSR_EXIT", "0")

	if err := runCommand(
		t.Context(),
		app,
		"ssr:start",
		[]string{"-bundle", bundle, "-runtime", os.Args[0]},
	); err != nil {
		t.Errorf("clean exit = %v", err)
	}

	err := runCommand(t.Context(), app, "ssr:start", []string{"-bundle", filepath.Join(dir, "missing.js")})
	if err == nil || !strings.Contains(err.Error(), "npm run build") {
		t.Errorf("missing bundle = %v", err)
	}
}

func TestSSRCheck(t *testing.T) {
	t.Parallel()

	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}

		_, _ = io.WriteString(w, `{"status":"OK"}`)
	}))
	t.Cleanup(healthy.Close)

	var out bytes.Buffer

	app := newTestApp(t, WithStdout(&out))

	if err := runCommand(t.Context(), app, "ssr:check", []string{"-url", healthy.URL}); err != nil ||
		!strings.Contains(out.String(), "healthy") {
		t.Errorf("ssr:check healthy = %v %q", err, out.String())
	}

	if err := runCommand(t.Context(), app, "ssr:check", []string{"-url", "http://127.0.0.1:1"}); err == nil {
		t.Error("ssr:check on a closed port = nil")
	}
}
