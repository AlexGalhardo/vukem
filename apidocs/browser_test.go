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

var (
	labelEl = regexp.MustCompile(`(?s)<label([^>]*)>(.*?)</label>`)
	// The elements a <label> can label.
	labelableEl = regexp.MustCompile(`(?i)<(?:button|input|meter|output|progress|select|textarea)[\s/>]`)
	// Scalar's server card captions its value with a <label>; page.js
	// retags it, so the <span> is proof the card rendered and was fixed.
	serverCaption = regexp.MustCompile(`(?s)<span[^>]*>\s*Server\s*</span>`)
)

// TestBrowser_APIDocsNoCSPViolations loads the page in headless Chromium
// under the app's default secure headers and fails on any CSP violation
// the console reports (A13), and on any <label> left labelling nothing
// (A95). It is skipped without a Chromium-based browser (set VUKEM_CHROME
// to point at one) and with -short.
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
	// Chromium helper processes can keep stderr open after the browser
	// exits; do not let them hang the test.
	cmd.WaitDelay = 5 * time.Second

	dom, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s: %v\n%s", chrome, err, stderr.String())
	}

	if !strings.Contains(string(dom), "scalar-app") {
		t.Fatalf("Scalar did not render:\n%.2000s\n%s", dom, stderr.String())
	}

	// page.js re-applied the line-number variables of the code samples.
	// Scalar renders the samples late: on slower hosted runners (Windows,
	// macOS) the virtual time budget can end before they exist, so the check
	// is mandatory only on Linux, the reference runner. The CSP check below
	// runs everywhere.
	hasVars := regexp.MustCompile(`<code[^>]+style="--line-count: \d+;`).Match(dom)
	// page.js tags every rewritten sample with data-vukem-line-vars, so its
	// presence means the samples were rendered.
	rendered := strings.Contains(string(dom), "data-vukem-line-vars")

	switch {
	case hasVars:
	case runtime.GOOS == "linux" || rendered:
		t.Errorf("code samples lack their line-number variables:\n%.4000s", bodyOf(dom))
	default:
		t.Logf("code samples not rendered within the time budget on %s; line-number check skipped", runtime.GOOS)
	}

	// page.js retagged Scalar's caption labels, so no <label> is left that
	// neither names a form field nor wraps one — the markup DevTools reports
	// as "No label associated with a form field".
	for _, m := range labelEl.FindAllSubmatch(dom, -1) {
		if bytes.Contains(m[1], []byte(" for=")) || labelableEl.Match(m[2]) {
			continue
		}

		t.Errorf("<label> that labels no form field: %s", m[0])
	}

	if !serverCaption.Match(dom) {
		t.Errorf("the server card's caption is not a retagged <span>:\n%.4000s", bodyOf(dom))
	}

	if v := consoleCSP.FindAllString(stderr.String(), -1); len(v) > 0 {
		t.Errorf("CSP violations on the docs page:\n%s", strings.Join(v, "\n"))
	}
}

// bodyOf trims the dumped DOM to its body so a failure shows the rendered
// page, not the inline styles of the head.
func bodyOf(dom []byte) []byte {
	if i := bytes.Index(dom, []byte("<body")); i >= 0 {
		return dom[i:]
	}

	return dom
}
