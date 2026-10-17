package apidocs_test

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// chromePath finds a Chromium-based browser: VUKEM_CHROME, then the PATH,
// then the default Windows and macOS install locations.
func chromePath() string {
	if p := os.Getenv("VUKEM_CHROME"); p != "" {
		return p
	}

	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome", "msedge"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}

	var candidates []string

	switch runtime.GOOS {
	case "windows":
		for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LocalAppData"} {
			if dir := os.Getenv(env); dir != "" {
				candidates = append(candidates,
					filepath.Join(dir, "Google", "Chrome", "Application", "chrome.exe"),
					filepath.Join(dir, "Microsoft", "Edge", "Application", "msedge.exe"))
			}
		}
	case "darwin":
		candidates = []string{"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"}
	}

	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	return ""
}

var consoleCSP = regexp.MustCompile(`(?i).*(content security policy|refused to|violates the following).*`)

// TestBrowser_APIDocsNoCSPViolations loads the page in headless Chromium
// under the app's default secure headers and fails on any CSP violation
// the console reports (A13). It is skipped without a Chromium-based
// browser (set VUKEM_CHROME to point at one) and with -short.
func TestBrowser_APIDocsNoCSPViolations(t *testing.T) {
	if testing.Short() {
		t.Skip("browser test")
	}

	chrome := chromePath()
	if chrome == "" {
		t.Skip("no Chromium-based browser found (set VUKEM_CHROME)")
	}

	srv := httptest.NewServer(handler(t, newApp(t, nil)))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()

	var stderr bytes.Buffer

	cmd := exec.CommandContext(ctx, chrome,
		"--headless=new", "--disable-gpu", "--no-first-run", "--no-default-browser-check",
		"--disable-extensions", "--disable-background-networking", "--disable-component-update",
		"--disable-sync", "--no-sandbox",
		"--user-data-dir="+t.TempDir(),
		"--enable-logging=stderr", "--v=0", "--log-level=0",
		"--virtual-time-budget=15000", "--dump-dom", srv.URL+"/docs")
	cmd.Stderr = &stderr

	dom, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s: %v\n%s", chrome, err, stderr.String())
	}

	if !strings.Contains(string(dom), "scalar-app") {
		t.Fatalf("Scalar did not render:\n%.2000s\n%s", dom, stderr.String())
	}

	// page.js re-applied the line-number variables of the code samples.
	if !regexp.MustCompile(`<code[^>]+style="--line-count: \d+;`).Match(dom) {
		t.Errorf("code samples lack their line-number variables:\n%.4000s", dom)
	}

	if v := consoleCSP.FindAllString(stderr.String(), -1); len(v) > 0 {
		t.Errorf("CSP violations on the docs page:\n%s", strings.Join(v, "\n"))
	}
}
