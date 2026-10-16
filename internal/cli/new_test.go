package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestVendoredHTMX(t *testing.T) {
	t.Parallel()

	b, err := starterFS.ReadFile("starter/" + htmxPath)
	if err != nil {
		t.Fatal(err)
	}

	sum := sha256.Sum256(b)
	if got := hex.EncodeToString(sum[:]); got != htmxSHA256 {
		t.Fatalf("htmx.min.js sha256 = %s, want %s (update htmxVersion and htmxSHA256 together)", got, htmxSHA256)
	}

	if !strings.HasPrefix(string(b), "var htmx=") {
		t.Fatal("htmx.min.js does not look like htmx")
	}
}

// Under the default CSP (script-src 'self' 'nonce-…', no inline styles),
// starter views must not use inline scripts, event handler attributes or
// style attributes.
//
// shortcut: static check of the templates, not a headless browser; add a
// browser check when the e2e suite gets one.
func TestSecurity_A02_StarterViewsRespectCSP(t *testing.T) {
	t.Parallel()

	forbidden := regexp.MustCompile(`(?i)<script|\son[a-z]+\s*=|\sstyle\s*=|hx-on|javascript:`)

	check := func(fsys fs.FS) fs.WalkDirFunc {
		return func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || strings.Contains(p, "/views/mail/") { // emails are not served pages
				return err
			}

			b, err := fs.ReadFile(fsys, p)
			if err != nil {
				return err
			}

			if m := forbidden.FindString(string(b)); m != "" {
				t.Errorf("%s contains %q", p, m)
			}

			return nil
		}
	}

	if err := fs.WalkDir(scaffoldFS, "scaffolds", check(scaffoldFS)); err != nil {
		t.Fatal(err)
	}

	err := fs.WalkDir(starterFS, "starter/resources/views", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		b, err := starterFS.ReadFile(p)
		if err != nil {
			return err
		}

		if m := forbidden.FindString(string(b)); m != "" {
			t.Errorf("%s contains %q", p, m)
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWriteStarter(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "blog")
	if err := writeStarter(dir, starterData{Name: "blog", Module: "example.com/blog"}); err != nil {
		t.Fatal(err)
	}

	for _, rel := range []string{
		"go.mod", "cmd/app/main.go", ".gitignore", ".env.example", "storage/.gitkeep",
		"resources/views/layouts/app.html", htmxPath, "tests/posts_test.go",
	} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			t.Errorf("missing %s", rel)
		}
	}

	_ = filepath.WalkDir(dir, func(p string, _ fs.DirEntry, _ error) error {
		if strings.HasSuffix(p, ".tmpl") {
			t.Errorf("unrendered stub %s", p)
		}

		return nil
	})

	if got := read(t, dir, "go.mod"); got != "module example.com/blog\n\ngo 1.27.0\n" {
		t.Errorf("go.mod = %q", got)
	}

	if !strings.Contains(read(t, dir, "cmd/app/main.go"), `"example.com/blog/bootstrap"`) {
		t.Error("module path not rendered")
	}

	// Views keep their html/template actions.
	if !strings.Contains(read(t, dir, "resources/views/home.html"), `{{route "posts.index"}}`) {
		t.Error("views were rendered as stubs")
	}
}

func TestNewUsage(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "taken"), 0o750); err != nil {
		t.Fatal(err)
	}

	if code, _, errOut := vukem(
		t,
		dir,
		"new",
		"../escape",
	); code != 2 ||
		!strings.Contains(errOut, "invalid project name") {
		t.Errorf("bad name = %d %q", code, errOut)
	}

	if code, _, errOut := vukem(t, dir, "new", "taken"); code != 1 || !strings.Contains(errOut, "already exists") {
		t.Errorf("existing dir = %d %q", code, errOut)
	}
}

// TestNewProjectEndToEnd creates a project, runs its own tests and builds
// it, as a user would.
func TestNewProjectEndToEnd(t *testing.T) {
	t.Parallel()
	requireGo(t)

	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()

	code, out, errOut := vukem(t, dir, "new", "blog", "--module", "example.com/blog", "--framework", repo)
	if code != 0 {
		t.Fatalf("vukem new = %d\n%s%s", code, out, errOut)
	}

	project := filepath.Join(dir, "blog")
	if env := read(t, project, ".env"); !strings.Contains(env, "APP_KEY=base64:") {
		t.Errorf(".env = %q", env)
	}

	cmd := exec.CommandContext(t.Context(), "go", "test", "./...")
	cmd.Dir = project
	cmd.Env = append(os.Environ(), "GOWORK=off")

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("starter tests: %v\n%s", err, out)
	}

	if code, out, errOut := vukem(
		t,
		project,
		"install:auth",
	); code != 0 ||
		!strings.Contains(out, "updated routes/registry.go") {
		t.Fatalf("vukem install:auth = %d\n%s%s", code, out, errOut)
	}

	if code, _, errOut := vukem(t, project, "install:auth"); code != 1 || !strings.Contains(errOut, "already exist") {
		t.Fatalf("second install:auth = %d %s", code, errOut)
	}

	cmd = exec.CommandContext(t.Context(), "go", "test", "./...")
	cmd.Dir = project
	cmd.Env = append(os.Environ(), "GOWORK=off")

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tests after install:auth: %v\n%s", err, out)
	}

	if code, out, errOut := vukem(t, project, "build"); code != 0 {
		t.Fatalf("vukem build = %d\n%s%s", code, out, errOut)
	}

	code, out, errOut = vukem(t, project, "route:list")
	if code != 0 || !strings.Contains(out, "posts.index") {
		t.Fatalf("vukem route:list = %d\n%s%s", code, out, errOut)
	}
}

func TestReleaseVersion(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{
		"v0.1.0": "v0.1.0", "v1.0.0-beta.2": "v1.0.0-beta.2", "devel": "latest",
		"v0.0.12+dirty": "latest", "v0.1.1-0.20261016120000-abcdef123456": "latest",
	} {
		if got := releaseVersion(in); got != want {
			t.Errorf("releaseVersion(%q) = %q, want %q", in, got, want)
		}
	}
}
