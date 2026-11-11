package cli

import (
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func benchServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()

	if h == nil {
		h = func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }
	}

	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	return srv
}

func TestRepeatFlag(t *testing.T) {
	t.Parallel()

	var r repeatFlag

	for _, v := range []string{"A: 1", "B: 2"} {
		if err := r.Set(v); err != nil {
			t.Fatal(err)
		}
	}

	if diff := cmp.Diff(repeatFlag{"A: 1", "B: 2"}, r); diff != "" {
		t.Fatalf("(-want +got):\n%s", diff)
	}

	if r.String() != "A: 1, B: 2" {
		t.Fatalf("String = %q", r.String())
	}
}

func TestBenchPrintsReport(t *testing.T) {
	t.Parallel()

	srv := benchServer(t, nil)

	code, out, errOut := vukem(t, t.TempDir(), "bench", "-n", "50", "-c", "2", srv.URL+"/")
	if code != 0 || !strings.Contains(out, "http_reqs") || !strings.Contains(out, " 50 ") ||
		!strings.Contains(out, "closed, 2 workers") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
	}
}

func TestBenchJSON(t *testing.T) {
	t.Parallel()

	srv := benchServer(t, nil)

	code, out, errOut := vukem(t, t.TempDir(), "bench", "-json", "-n", "20", srv.URL+"/")
	if code != 0 {
		t.Fatalf("code=%d err=%q", code, errOut)
	}

	var got struct {
		SchemaVersion int    `json:"schema_version"`
		HTTPReqs      uint64 `json:"http_reqs"`
	}

	if err := json.Unmarshal([]byte(out), &got); err != nil || got.SchemaVersion != 1 || got.HTTPReqs != 20 {
		t.Fatalf("out=%q err=%v got=%+v", out, err, got)
	}
}

func TestBenchWritesOutputFile(t *testing.T) {
	t.Parallel()

	srv := benchServer(t, nil)
	dir := t.TempDir()

	if code, _, errOut := vukem(t, dir, "bench", "-n", "10", "-o", "out.json", srv.URL+"/"); code != 0 {
		t.Fatalf("code=%d err=%q", code, errOut)
	}

	b, err := os.ReadFile(filepath.Join(dir, "out.json"))
	if err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil || got["http_reqs"] != float64(10) {
		t.Fatalf("out.json = %q (%v)", b, err)
	}
}

func TestBenchOpenModel(t *testing.T) {
	t.Parallel()

	srv := benchServer(t, nil)

	code, out, errOut := vukem(t, t.TempDir(), "bench", "-rate", "200/s", "-n", "10", srv.URL+"/")
	if code != 0 || !strings.Contains(out, "open, 200/s") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
	}
}

func TestBenchUsageErrors(t *testing.T) {
	t.Parallel()

	tests := [][]string{
		{"bench"},
		{"bench", "http://127.0.0.1:1/", "http://127.0.0.1:2/"},
		{"bench", "-c", "5", "-rate", "10/s", "http://127.0.0.1:1/"},
		{"bench", "-body", "x", "-body-file", "y", "http://127.0.0.1:1/"},
		{"bench", "-d", "2h", "http://127.0.0.1:1/"},
		{"bench", "-H", "no colon here", "http://127.0.0.1:1/"},
		{"bench", "-rate", "nope", "http://127.0.0.1:1/"},
		{"bench", "-body-file", "missing.txt", "http://127.0.0.1:1/"},
		{"bench", "checkout", "-n", "1"},
	}

	for _, args := range tests {
		if code, _, errOut := vukem(t, t.TempDir(), args...); code != 2 {
			t.Errorf("vukem %v: code=%d err=%q, want 2", args, code, errOut)
		}
	}
}

func TestSecurity_A10_BenchCLIRefusesRemoteTarget(t *testing.T) {
	t.Parallel()

	code, out, errOut := vukem(t, t.TempDir(), "bench", "-n", "1", "http://example.com/")
	if code != 2 || !strings.Contains(errOut, "only loopback hosts are allowed") || strings.Contains(out, "http_reqs") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
	}
}

func TestBenchAllowRemoteIsRepeatable(t *testing.T) {
	t.Parallel()

	srv := benchServer(t, nil)

	// A loopback target needs no allowance; the flags must still parse, twice.
	code, _, errOut := vukem(
		t,
		t.TempDir(),
		"bench",
		"-n",
		"1",
		"-allow-remote",
		"a.example",
		"-allow-remote",
		"b.example",
		srv.URL+"/",
	)
	if code != 0 {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
}

func TestBenchHeaderAndBody(t *testing.T) {
	t.Parallel()

	var (
		mu   sync.Mutex
		seen []string
	)

	srv := benchServer(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)

		mu.Lock()
		seen = append(
			seen,
			strings.Join([]string{r.Method, r.Header.Get("X-Test"), string(b), r.Header.Get("User-Agent")}, "|"),
		)
		mu.Unlock()

		_, _ = w.Write([]byte("ok"))
	})

	code, _, errOut := vukem(
		t,
		t.TempDir(),
		"bench",
		"-n",
		"1",
		"-m",
		"POST",
		"-H",
		"X-Test: 1",
		"-body",
		"hi",
		srv.URL+"/",
	)
	if code != 0 {
		t.Fatalf("code=%d err=%q", code, errOut)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(seen) != 1 || seen[0] != "POST|1|hi|vukem-bench/v0.0.0-test" {
		t.Fatalf("server saw %q", seen)
	}
}

func TestBenchBodyFileIsLimitedTo10MiB(t *testing.T) {
	t.Parallel()

	srv := benchServer(t, nil)
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "big.bin"), make([]byte, 10<<20+1), 0o600); err != nil {
		t.Fatal(err)
	}

	code, _, errOut := vukem(t, dir, "bench", "-n", "1", "-m", "POST", "-body-file", "big.bin", srv.URL+"/")
	if code != 2 || !strings.Contains(errOut, "larger than 10 MiB") {
		t.Fatalf("code=%d err=%q, want a usage error", code, errOut)
	}

	if err := os.WriteFile(filepath.Join(dir, "ok.bin"), make([]byte, 10<<20), 0o600); err != nil {
		t.Fatal(err)
	}

	if code, _, errOut := vukem(
		t,
		dir,
		"bench",
		"-n",
		"1",
		"-m",
		"POST",
		"-body-file",
		"ok.bin",
		srv.URL+"/",
	); code != 0 {
		t.Fatalf("a body of exactly 10 MiB was refused: code=%d err=%q", code, errOut)
	}
}

func TestBenchBodyFile(t *testing.T) {
	t.Parallel()

	var (
		mu   sync.Mutex
		body string
	)

	srv := benchServer(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)

		mu.Lock()
		body = string(b)
		mu.Unlock()

		_, _ = w.Write([]byte("ok"))
	})

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "payload.json"), []byte(`{"a":1}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if code, _, errOut := vukem(
		t,
		dir,
		"bench",
		"-n",
		"1",
		"-m",
		"POST",
		"-body-file",
		"payload.json",
		srv.URL+"/",
	); code != 0 {
		t.Fatalf("code=%d err=%q", code, errOut)
	}

	mu.Lock()
	defer mu.Unlock()

	if body != `{"a":1}` {
		t.Fatalf("server body = %q", body)
	}
}

func TestBenchURLThresholdExit3(t *testing.T) {
	t.Parallel()

	srv := benchServer(t, nil)

	code, out, errOut := vukem(
		t,
		t.TempDir(),
		"bench",
		"-n",
		"20",
		"-threshold",
		"p95<1ns",
		"-threshold",
		"error_rate<1%",
		srv.URL+"/",
	)
	if code != 3 || !strings.Contains(out, "http_reqs") || !strings.Contains(errOut, "FAIL  p95<1ns") ||
		!strings.Contains(errOut, "ok") {
		t.Fatalf("code=%d out=%q err=%q, want exit 3 with the report and the threshold table", code, out, errOut)
	}

	if code, _, errOut := vukem(t, t.TempDir(), "bench", "-n", "20", "-threshold", "p95<1h", srv.URL+"/"); code != 0 {
		t.Fatalf("thresholds that hold: code=%d err=%q", code, errOut)
	}

	if code, _, _ := vukem(t, t.TempDir(), "bench", "-n", "1", "-threshold", "nonsense", srv.URL+"/"); code != 2 {
		t.Fatalf("a bad threshold = %d, want a usage error", code)
	}
}

func TestBenchURLSaveAndCompare(t *testing.T) {
	t.Parallel()

	srv := benchServer(t, nil)
	dir := t.TempDir()

	if code, _, errOut := vukem(
		t,
		dir,
		"bench",
		"-n",
		"30",
		"-save",
		filepath.Join(".vukem", "bench", "baseline.json"),
		srv.URL+"/",
	); code != 0 {
		t.Fatalf("save: code=%d err=%q", code, errOut)
	}

	saved := filepath.Join(dir, ".vukem", "bench", "baseline.json")
	if _, err := os.Stat(saved); err != nil {
		t.Fatal(err)
	}

	var base map[string]any

	b, err := os.ReadFile(saved)
	if err != nil {
		t.Fatal(err)
	}

	if err := json.Unmarshal(b, &base); err != nil {
		t.Fatal(err)
	}

	// A baseline no real run can fall behind keeps the check stable on a busy machine.
	easy, err := json.Marshal(map[string]any{
		"schema_version": 1,
		"http_reqs":      1,
		"rps":            0.001,
		"http_req_duration": map[string]any{
			"p95_ns": 3.6e12,
			"p99_ns": 3.6e12,
		},
		"target":      base["target"],
		"model":       base["model"],
		"concurrency": base["concurrency"],
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "easy.json"), easy, 0o600); err != nil {
		t.Fatal(err)
	}

	if code, _, errOut := vukem(t, dir, "bench", "-n", "30", "-compare", "easy.json", srv.URL+"/"); code != 0 {
		t.Fatalf("compare with an easy baseline: code=%d err=%q", code, errOut)
	}

	// The tolerance has a ceiling of 100%.
	if code, _, _ := vukem(t, dir, "bench", "-n", "1", "-tolerance", "900%", srv.URL+"/"); code != 2 {
		t.Fatalf("a tolerance of 900%% = %d, want a usage error", code)
	}

	// A fabricated baseline that no real run can match.
	base["rps"] = 1e12
	base["http_req_duration"] = map[string]any{"p95_ns": 1, "p99_ns": 1}

	fake, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "fake.json"), fake, 0o600); err != nil {
		t.Fatal(err)
	}

	code, _, errOut := vukem(t, dir, "bench", "-n", "30", "-compare", "fake.json", srv.URL+"/")
	if code != 4 || !strings.Contains(errOut, "REGRESSED") {
		t.Fatalf("compare with a better baseline: code=%d err=%q, want 4", code, errOut)
	}
}

func TestBenchWithoutURLOutsideProjectIsUsageError(t *testing.T) {
	t.Parallel()

	if code, _, errOut := vukem(
		t,
		t.TempDir(),
		"bench",
		"-d",
		"1s",
	); code != 2 ||
		!strings.Contains(errOut, "bench:run") {
		t.Fatalf("code=%d err=%q, want a usage error that names bench:run", code, errOut)
	}
}

func TestBenchURLRejectsProjectOnlyFlags(t *testing.T) {
	t.Parallel()

	srv := benchServer(t, nil)

	for _, flagArgs := range [][]string{{"-scenario", "x"}, {"-stages", "1s:1"}, {"-url", "http://127.0.0.1:1"}, {"-seed"}, {"-list"}} {
		args := append([]string{"bench", "-n", "1"}, flagArgs...)
		args = append(args, srv.URL+"/")

		if code, _, _ := vukem(t, t.TempDir(), args...); code != 2 {
			t.Errorf("vukem %v: code=%d, want 2", args, code)
		}
	}
}

func TestForwardedFlags(t *testing.T) {
	t.Parallel()

	set := flag.NewFlagSet("bench", flag.ContinueOnError)
	run := benchCmd(set)

	_ = run

	if err := set.Parse(
		[]string{"-scenario", "a", "-scenario", "b", "-d", "5s", "-threshold", "p95<1s", "-seed", "-c", "3"},
	); err != nil {
		t.Fatal(err)
	}

	want := []string{"-c=3", "-d=5s", "-scenario=a", "-scenario=b", "-seed=true", "-threshold=p95<1s"}
	if diff := cmp.Diff(want, forwardedFlags(set)); diff != "" {
		t.Fatalf("(-want +got):\n%s", diff)
	}
}

func TestBenchDelegatesToApp(t *testing.T) {
	t.Parallel()
	requireGo(t)

	dir := stdlibApp(t, echoApp)

	code, out, errOut := vukem(
		t,
		dir,
		"bench",
		"-d",
		"1s",
		"-c",
		"1",
		"-threshold",
		"p95<1s",
		"-scenario",
		"a",
		"-scenario",
		"b",
		"-seed",
	)
	if code != 0 || out != "app: bench:run -c=1 -d=1s -scenario=a -scenario=b -seed=true -threshold=p95<1s\n" {
		t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
	}
}

func TestBenchDelegationRejectsURLOnlyFlags(t *testing.T) {
	t.Parallel()

	// bench:run in the app does not know these: refuse before building and starting the app.
	for _, args := range [][]string{
		{"bench", "-n", "5"},
		{"bench", "-m", "POST"},
		{"bench", "-H", "A: 1"},
		{"bench", "-body", "x"},
		{"bench", "-body-file", "f"},
		{"bench", "-connections", "5"},
		{"bench", "-no-keepalive"},
		{"bench", "-http2"},
		{"bench", "-o", "out.json"},
		{"bench", "-quiet"},
	} {
		code, _, errOut := vukem(t, t.TempDir(), args...)
		if code != 2 || !strings.Contains(errOut, "URL") {
			t.Errorf("vukem %v: code=%d err=%q, want a usage error about the URL", args, code, errOut)
		}
	}
}

func TestBenchForwardsYes(t *testing.T) {
	t.Parallel()
	requireGo(t)

	dir := stdlibApp(t, echoApp)

	code, out, errOut := vukem(t, dir, "bench", "-database-url", "postgres://x/y", "-yes")
	if code != 0 || out != "app: bench:run -database-url=postgres://x/y -yes=true\n" {
		t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
	}
}
