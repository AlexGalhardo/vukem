package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
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
// starter views (html/template and templ) must not use inline scripts,
// event handler attributes or style attributes.
//
// shortcut: static check of the templates, not a headless browser; add a
// browser check when the e2e suite gets one.
func TestSecurity_A02_StarterViewsRespectCSP(t *testing.T) {
	t.Parallel()

	forbidden := regexp.MustCompile(`(?i)<script|\son[a-z]+\s*=|\sstyle\s*=|hx-on|javascript:`)

	check := func(fsys fs.FS) fs.WalkDirFunc {
		return func(p string, d fs.DirEntry, err error) error {
			// Emails are not served pages; JSX/Vue/Svelte event props are not
			// inline HTML handlers.
			isView := strings.HasSuffix(p, ".html") || strings.HasSuffix(p, ".templ") ||
				strings.HasSuffix(p, ".templ.tmpl")
			if err != nil || d.IsDir() || strings.Contains(p, "/views/mail/") || !isView {
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

	code, out, errOut := vukem(
		t,
		dir,
		"new",
		"blog",
		"--module",
		"example.com/blog",
		"--framework",
		repo,
		"--no-install",
		// This test drives npm itself, so it pins the project to npm instead
		// of letting the scaffold pick what this machine has.
		"--pm",
		"npm",
	)
	if code != 0 {
		t.Fatalf("vukem new = %d\n%s%s", code, out, errOut)
	}

	project := filepath.Join(dir, "blog")
	if env := read(t, project, ".env"); !strings.Contains(env, "APP_KEY=base64:") {
		t.Errorf(".env = %q", env)
	}

	if df := read(t, project, "Dockerfile"); !strings.Contains(df, "AS assets") ||
		!strings.Contains(df, "./cmd/app") {
		t.Errorf("Dockerfile = %q", df)
	}

	if _, err := os.Stat(filepath.Join(project, ".dockerignore")); err != nil {
		t.Error(err)
	}

	requireGovulncheckTool(t, project)

	// APP_ENV=production in .env (not the process) must keep the
	// embedded views even with a resources/ folder on disk.
	envTest := filepath.Join(project, "resources", "env_test.go")
	if err := os.WriteFile(envTest, []byte(resourcesEnvTest), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.CommandContext(t.Context(), "go", "test", "./...")
	cmd.Dir = project
	cmd.Env = append(os.Environ(), "GOWORK=off")

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("starter tests: %v\n%s", err, out)
	}

	if err := os.Remove(envTest); err != nil {
		t.Fatal(err)
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

	// Without --locales the default locale is the only catalog written.
	requireFile(t, project, "app/accounts/lang/en/auth.json")
	requireNoFile(t, project, "app/accounts/lang/pt-BR")

	if code, _, errOut := vukem(t, project, "install:auth", "--locales", "fr"); code != 2 ||
		!strings.Contains(errOut, "no auth message catalog") {
		t.Fatalf("install:auth --locales fr = %d %s", code, errOut)
	}

	if code, out, errOut := vukem(
		t,
		project,
		"install:auth",
		"--social",
		"github,google,apple,linkedin",
	); code != 0 ||
		!strings.Contains(out, "created routes/social.go") ||
		strings.Contains(out, "created routes/auth.go") {
		t.Fatalf("install:auth --social = %d\n%s%s", code, out, errOut)
	}

	// A later generator writes catalogs only for the locales the project
	// has: it never re-adds one the developer removed (A79).
	requireFile(t, project, "app/accounts/lang/en/social.json")
	requireNoFile(t, project, "app/accounts/lang/pt-BR")

	// --2fa on a project with install:auth adds only two-factor
	// authentication, and makes social sign-in ask for the code too.
	if code, out, errOut := vukem(t, project, "install:auth", "--2fa"); code != 0 ||
		!strings.Contains(out, "created routes/two_factor.go") || strings.Contains(out, "created routes/auth.go") ||
		!strings.Contains(out, "updated app/controllers/account/account.go") ||
		!strings.Contains(out, "updated routes/social.go") {
		t.Fatalf("install:auth --2fa = %d\n%s%s", code, out, errOut)
	}

	if code, _, errOut := vukem(
		t,
		project,
		"install:auth",
		"--2fa",
	); code != 1 ||
		!strings.Contains(errOut, "already exist") {
		t.Fatalf("second install:auth --2fa = %d %s", code, errOut)
	}

	if env := read(t, project, ".env.example"); !strings.Contains(env, "SOCIAL_GOOGLE_CLIENT_SECRET=") {
		t.Fatalf(".env.example = %q", env)
	}

	if code, _, errOut := vukem(
		t,
		project,
		"install:auth",
		"--social",
		"myspace",
	); code != 2 ||
		!strings.Contains(errOut, "unknown social provider") {
		t.Fatalf("unknown provider = %d %s", code, errOut)
	}

	// install:api on a web project reuses the authentication core; with
	// two-factor authentication installed, token issuance asks for the code
	// too (TestAPITokenRequiresTwoFactor runs below).
	if code, out, errOut := vukem(
		t,
		project,
		"install:api",
	); code != 0 ||
		!strings.Contains(out, "created routes/api.go") ||
		strings.Contains(out, "created app/accounts/accounts.go") ||
		!strings.Contains(out, "updated app/controllers/apiv1/tokens.go") ||
		!strings.Contains(out, "created tests/api_two_factor_test.go") {
		t.Fatalf("install:api = %d\n%s%s", code, out, errOut)
	}

	// install:billing adds Stripe subscriptions against the local
	// contrib/stripe; its generated tests run against a fake Stripe API.
	if code, out, errOut := vukem(t, project, "install:billing"); code != 0 ||
		!strings.Contains(out, "created routes/billing.go") ||
		!strings.Contains(out, "created resources/views/billing/show.html") ||
		!strings.Contains(out, "updated routes/registry.go") {
		t.Fatalf("install:billing = %d\n%s%s", code, out, errOut)
	}

	if mod := read(t, project, "go.mod"); !strings.Contains(mod, stripeModule+" v0.0.0") {
		t.Errorf("go.mod does not require contrib/stripe:\n%s", mod)
	}

	if env := read(t, project, ".env.example"); !strings.Contains(env, "STRIPE_KEY=\n") ||
		!strings.Contains(env, "STRIPE_WEBHOOK_SECRET=\n") || !strings.Contains(env, "STRIPE_PRICE_PRO=\n") {
		t.Errorf(".env.example = %q", env)
	}

	cmd = exec.CommandContext(t.Context(), "go", "test", "./...")
	cmd.Dir = project
	cmd.Env = append(os.Environ(), "GOWORK=off")

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tests after install:auth: %v\n%s", err, out)
	}

	// The advice names whichever package manager this machine has, so the
	// assertion is on the refusal, not on the manager.
	if code, _, errOut := vukem(t, project, "build"); code != 1 ||
		!strings.Contains(errOut, "frontend dependencies missing") {
		t.Fatalf("vukem build without node_modules = %d %s", code, errOut)
	}

	if _, err := exec.LookPath(npm()); err != nil {
		t.Log("npm not found: skipping npm install and vite build")
	} else {
		install := exec.CommandContext(t.Context(), npm(), "install", "--no-audit", "--no-fund")
		install.Dir = project

		if out, err := install.CombinedOutput(); err != nil {
			t.Fatalf("npm install: %v\n%s", err, out)
		}

		if code, out, errOut := vukem(t, project, "build"); code != 0 {
			t.Fatalf("vukem build = %d\n%s%s", code, out, errOut)
		}

		manifest := read(t, project, filepath.Join("resources", "public", "build", ".vite", "manifest.json"))
		if !strings.Contains(manifest, `"resources/css/app.css"`) ||
			!strings.Contains(manifest, `"resources/js/app.ts"`) {
			t.Errorf("manifest = %s", manifest)
		}
	}

	code, out, errOut = vukem(t, project, "route:list")
	if code != 0 || !strings.Contains(out, "posts.index") {
		t.Fatalf("vukem route:list = %d\n%s%s", code, out, errOut)
	}

	requireGeneratedLint(t, project, "example.com/blog")
}

// lintMu keeps the generated-project lint runs off each other's back: they
// are the slowest step of the parallel end-to-end tests, and running them
// at once only thrashes the machine.
var lintMu sync.Mutex

// generatedLintConfig turns this repository's .golangci.yml into the
// config an app would use. Three things are repository-internal and
// nothing else is touched: gofumpt's module-path and gci's prefix name
// this module (an app names its own), and depguard holds the
// module-boundary and dependency-allowlist rules of this repository, which
// an app does not share (docs/architecture.md#module-boundaries).
func generatedLintConfig(cfg []byte, module string) []byte {
	cfg = bytes.ReplaceAll(cfg, []byte("module-path: "+frameworkModule), []byte("module-path: "+module))
	cfg = bytes.ReplaceAll(cfg, []byte("prefix("+frameworkModule+")"), []byte("prefix("+module+")"))

	return bytes.ReplaceAll(cfg, []byte("\n    - depguard\n"), []byte("\n"))
}

// requireGeneratedLint runs this repository's own golangci-lint over a
// generated project, so generated code cannot drift from the bar the
// framework holds itself to (the linters are the repository's, unchanged).
func requireGeneratedLint(t *testing.T, project, module string) {
	t.Helper()

	bin, err := exec.LookPath("golangci-lint")
	if err != nil {
		t.Log("golangci-lint not found: skipping the generated-code lint")
		return
	}

	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := os.ReadFile(filepath.Join(repo, ".golangci.yml"))
	if err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(project, ".golangci.yml")
	if err := os.WriteFile(dst, generatedLintConfig(cfg, module), 0o600); err != nil {
		t.Fatal(err)
	}

	lintMu.Lock()
	defer lintMu.Unlock()

	// --allow-parallel-runners: lintMu already serializes these runs, and
	// without it a golangci-lint running elsewhere (an editor, a hook)
	// fails the test with "parallel golangci-lint is running".
	cmd := exec.CommandContext(t.Context(), bin, "run", "--allow-parallel-runners", "./...")
	cmd.Dir = project
	cmd.Env = append(os.Environ(), "GOWORK=off")

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("golangci-lint on the generated project: %v\n%s", err, out)
	}

	if err := os.Remove(dst); err != nil {
		t.Fatal(err)
	}
}

// requireGovulncheckTool fails unless the generated go.mod pins govulncheck
// with a tool directive, so `go tool govulncheck ./...` works out of the box.
func requireGovulncheckTool(t *testing.T, project string) {
	t.Helper()

	mod := read(t, project, "go.mod")
	if !hasTool([]byte(mod), "golang.org/x/vuln/cmd/govulncheck") {
		t.Errorf("go.mod has no govulncheck tool directive:\n%s", mod)
	}

	if !strings.Contains(mod, "golang.org/x/vuln v1.9.0") {
		t.Errorf("go.mod does not pin golang.org/x/vuln v1.9.0:\n%s", mod)
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

// TestNewAPIProjectEndToEnd creates a JSON API project and runs its tests.
func TestNewAPIProjectEndToEnd(t *testing.T) {
	t.Parallel()
	requireGo(t)

	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()

	code, out, errOut := vukem(t, dir, "new", "shop", "--api", "--module", "example.com/shop", "--framework", repo)
	if code != 0 {
		t.Fatalf("vukem new --api = %d\n%s%s", code, out, errOut)
	}

	project := filepath.Join(dir, "shop")
	requireGovulncheckTool(t, project)

	for _, gone := range []string{"resources/public", "routes/web.go", "resources/views/home.html"} {
		if _, err := os.Stat(filepath.Join(project, filepath.FromSlash(gone))); err == nil {
			t.Errorf("%s still exists in the API starter", gone)
		}
	}

	if df := read(t, project, "Dockerfile"); strings.Contains(df, "AS assets") || !strings.Contains(df, "./cmd/app") {
		t.Errorf("API Dockerfile = %q", df)
	}

	if env := read(t, project, ".env.example"); !strings.Contains(env, "CORS_ALLOWED_ORIGINS=") {
		t.Errorf(".env.example = %q", env)
	}

	cmd := exec.CommandContext(t.Context(), "go", "test", "./...")
	cmd.Dir = project
	cmd.Env = append(os.Environ(), "GOWORK=off")

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("API starter tests: %v\n%s", err, out)
	}

	if code, out, errOut := vukem(
		t,
		project,
		"route:list",
	); code != 0 ||
		!strings.Contains(out, "api.v1.posts.index") ||
		!strings.Contains(out, "api.v1.tokens.store") {
		t.Fatalf("route:list = %d\n%s%s", code, out, errOut)
	}

	requireGeneratedLint(t, project, "example.com/shop")
}

// A generated `return u, orm.Create(ctx, tx, &u)` leaves unspecified whether
// u is read before or after Create sets its ID (gocritic evalOrder), so
// scaffolds must mutate first and return after.
func TestScaffoldsDoNotReturnValueMutatedInSameStatement(t *testing.T) {
	t.Parallel()

	ret := regexp.MustCompile(`^\s*return ([A-Za-z_]\w*), (.+)$`)

	check := func(fsys fs.FS) fs.WalkDirFunc {
		return func(p string, d fs.DirEntry, err error) error {
			isGo := strings.HasSuffix(p, ".go") || strings.HasSuffix(p, ".go.tmpl")
			if err != nil || d.IsDir() || !isGo {
				return err
			}

			b, err := fs.ReadFile(fsys, p)
			if err != nil {
				return err
			}

			for i, line := range strings.Split(string(b), "\n") {
				m := ret.FindStringSubmatch(line)
				if len(m) == 3 && regexp.MustCompile(`&`+m[1]+`\b`).MatchString(m[2]) {
					t.Errorf("%s:%d returns %s while mutating it: %s", p, i+1, m[1], strings.TrimSpace(line))
				}
			}

			return nil
		}
	}

	for name, fsys := range map[string]fs.FS{"scaffolds": scaffoldFS, "starter": starterFS} {
		if err := fs.WalkDir(fsys, name, check(fsys)); err != nil {
			t.Fatal(err)
		}
	}
}

const resourcesEnvTest = `package resources

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestDotEnvProductionUsesEmbeddedViews(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "resources", "views"), 0o750); err != nil {
		t.Fatal(err)
	}

	t.Chdir(dir)
	t.Setenv("APP_ENV", "")
	_ = os.Unsetenv("APP_ENV")

	if _, err := fs.Stat(Views(), "home.html"); err == nil {
		t.Fatal("development: Views() has home.html, want the empty resources/views on disk")
	}

	if err := os.WriteFile(".env", []byte("APP_ENV=production"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := fs.Stat(Views(), "home.html"); err != nil {
		t.Fatalf("APP_ENV=production in .env: %v, want the embedded views", err)
	}
}
`
