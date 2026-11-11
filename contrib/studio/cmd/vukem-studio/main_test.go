package main

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite" // creates the fixture database
)

// project makes a directory with a .env and a SQLite file.
func project(t *testing.T, appEnv string) string {
	t.Helper()

	dir := t.TempDir()

	if err := os.MkdirAll(filepath.Join(dir, "storage"), 0o750); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(dir, "storage", "database.sqlite")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := db.Exec("CREATE TABLE t (id INTEGER)"); err != nil {
		t.Fatal(err)
	}

	_ = db.Close()

	env := "APP_ENV=" + appEnv + "\nDATABASE_URL=file:storage/database.sqlite\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}

	return dir
}

func noEnv(string) (string, bool) { return "", false }

func TestSecurity_A05_StudioRefusesProduction(t *testing.T) {
	t.Parallel()

	dir := project(t, "production")

	var stdout, stderr bytes.Buffer

	code := run(t.Context(), []string{"--dir", dir, "--no-open"}, &stdout, &stderr, noEnv)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}

	want := "vukem-studio: refusing to start with APP_ENV=production (pass --allow-production to override)"
	if !strings.Contains(stderr.String(), want) || stdout.Len() != 0 {
		t.Errorf("stderr = %q, stdout = %q", stderr.String(), stdout.String())
	}

	// An unset APP_ENV is production too.
	empty := t.TempDir()

	if code := run(t.Context(), []string{"--dir", empty, "--no-open"}, io.Discard, io.Discard, noEnv); code != 2 {
		t.Errorf("exit code with no APP_ENV = %d, want 2", code)
	}
}

func TestSecurity_A09_StudioNeverPrintsCredentials(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer

	// A literal test value, not a secret. The host does not resolve, so the
	// run ends in an error: nothing it prints may carry the password.
	args := []string{
		"--dir", t.TempDir(), "--no-open", "--allow-production",
		"--database-url", "postgres://app:s3cret@db.invalid:5432/app",
	}

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	code := run(ctx, args, &stdout, &stderr, noEnv)
	if code == 0 {
		t.Fatal("exit code 0 against a host that does not exist")
	}

	for name, out := range map[string]string{"stdout": stdout.String(), "stderr": stderr.String()} {
		if strings.Contains(out, "s3cret") {
			t.Errorf("%s leaks the password: %q", name, out)
		}
	}
}

func TestRunRefusesBadUsage(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{"--nope"}, {"extra"}, {"--port", "70000"}} {
		if code := run(t.Context(), args, io.Discard, io.Discard, noEnv); code != 2 {
			t.Errorf("run(%v) = %d, want 2", args, code)
		}
	}
}

func TestRunVersion(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	if code := run(
		t.Context(),
		[]string{"--version"},
		&out,
		io.Discard,
		noEnv,
	); code != 0 ||
		!strings.HasPrefix(out.String(), "vukem-studio v") {
		t.Errorf("--version = %d %q", code, out.String())
	}
}

func TestRunMissingSQLiteFileIsAnError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("APP_ENV=development\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer

	if code := run(t.Context(), []string{"--dir", dir, "--no-open"}, io.Discard, &stderr, noEnv); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}

	if _, err := os.Stat(filepath.Join(dir, "storage", "database.sqlite")); err == nil {
		t.Error("Studio created a database file")
	}
}

// syncBuffer is a bytes.Buffer safe for the server goroutine and the test.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.b.String()
}

func TestRunServesAndStops(t *testing.T) {
	t.Parallel()

	dir := project(t, "development")

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var stdout syncBuffer

	done := make(chan int, 1)

	go func() {
		done <- run(ctx, []string{"--dir", dir, "--port", "0", "--no-open"}, &stdout, io.Discard, noEnv)
	}()

	urlRe := regexp.MustCompile(`open {2}(http://127\.0\.0\.1:\d+/\?token=\S+)`)

	var startURL string

	for range 100 {
		if m := urlRe.FindStringSubmatch(stdout.String()); m != nil {
			startURL = m[1]
			break
		}

		time.Sleep(50 * time.Millisecond) //nolint:forbidigo // waits on the server goroutine
	}

	if startURL == "" {
		t.Fatalf("no start URL in the output: %q", stdout.String())
	}

	if !strings.Contains(stdout.String(), "(read-only)") ||
		!strings.Contains(stdout.String(), "sqlite  storage/database.sqlite") {
		t.Errorf("header = %q", stdout.String())
	}

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, startURL, http.NoBody)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("start URL status = %d, want 303", resp.StatusCode)
	}

	cancel()

	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit code = %d, want 0", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not stop after the context was cancelled")
	}
}
