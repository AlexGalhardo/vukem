package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// financeFrameworkRepo returns the checkout the generated projects use as the
// framework (--framework), or skips when it lacks the packages the app needs.
func financeFrameworkRepo(t *testing.T) string {
	t.Helper()
	requireGo(t)

	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(repo, "tenancy", "tenancy.go")); err != nil {
		t.Skip("the finance example needs the tenancy package: ", err)
	}

	return repo
}

// generateFinance runs vukem new --example finance in a temp directory and
// returns the project directory and the command output.
func generateFinance(t *testing.T, extra ...string) (project, out string) {
	t.Helper()

	dir := t.TempDir()
	args := append([]string{
		"new", "money", "--example", "finance", "--module", "example.test/money",
		"--framework", financeFrameworkRepo(t), "--no-install",
	}, extra...)

	code, stdout, stderr := vukem(t, dir, args...)
	if code != 0 {
		t.Fatalf("vukem %v = %d\n%s%s", args, code, stdout, stderr)
	}

	return filepath.Join(dir, "money"), stdout
}

// TestNewFinanceProjectEndToEnd generates the project from the exported app
// and runs what the app's own CI runs: tidy, build, vet and its whole test
// suite (isolation, billing, sync and the rest), under another module path.
func TestNewFinanceProjectEndToEnd(t *testing.T) {
	t.Parallel()

	project, out := generateFinance(t)

	for _, rel := range []string{
		"go.mod", "README.md", "NOTICE", "Dockerfile", "compose.yaml", ".env", ".env.example", "package.json",
		"bun.lock", "bootstrap/app.go", "routes/finance.go", "resources/js/pages/Dashboard.tsx", "setup-local.md",
	} {
		requireFile(t, project, rel)
	}

	for _, gone := range []string{
		"mobile", "desktop", ".github", ".agents", ".claude", "development", "VERSION", "MANIFEST.sha256",
		"LICENSE.vukem-finances", "resources/js/pages/Posts", "resources/js/pages/Tasks",
	} {
		requireNoFile(t, project, gone)
	}

	if !strings.Contains(out, "vukem-finances "+financeAppVersion()) {
		t.Errorf("output does not name the app release:\n%s", out)
	}

	mod := read(t, project, "go.mod")
	for _, want := range []string{"module example.test/money\n", "contrib/stripe " + financeStripeVersion} {
		if !strings.Contains(mod, want) {
			t.Errorf("go.mod lacks %q:\n%s", want, mod)
		}
	}

	env := read(t, project, ".env")
	if !strings.Contains(env, "APP_KEY=base64:") || !strings.Contains(env, "APP_EDITION=personal") {
		t.Errorf(".env lacks APP_KEY or APP_EDITION=personal:\n%s", env)
	}

	// Every .tmpl was rendered: no suffix and no placeholder survives.
	err := filepath.WalkDir(project, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		if strings.HasSuffix(p, ".tmpl") {
			t.Errorf("%s kept its .tmpl suffix", p)
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(read(t, project, "bootstrap/app.go"), "vukem-finances") ||
		strings.Contains(read(t, project, "compose.yaml"), "vukem-finances") {
		t.Error("the app's own name or module path survived in bootstrap/app.go or compose.yaml")
	}

	run := func(name string, args ...string) {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), name, args...)
		cmd.Dir = project
		cmd.Env = append(os.Environ(), "GOWORK=off")

		if out, err := combinedOutput(cmd); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}

	run("go", "mod", "tidy")
	run("go", "build", "./...")
	run("go", "vet", "./...")
	run("go", "test", "-p", "4", "./...")
}

func TestFinanceEdition(t *testing.T) {
	t.Parallel()

	project, _ := generateFinance(t, "--edition", "saas")

	env := read(t, project, ".env")
	if strings.Count(env, "APP_EDITION=") != 1 || !strings.Contains(env, "APP_EDITION=saas") {
		t.Errorf(".env = %s, want one APP_EDITION=saas", env)
	}

	if !strings.Contains(read(t, project, ".env.example"), "APP_EDITION=personal") {
		t.Error(".env.example must keep the personal default")
	}

	for name, args := range map[string][]string{
		"unknown":       {"new", "app", "--example", "finance", "--edition", "team"},
		"other example": {"new", "app", "--example", "todo", "--edition", "saas"},
		"no example":    {"new", "app", "--no-example", "--edition", "saas"},
	} {
		code, _, errOut := vukem(t, t.TempDir(), args...)
		if code != 2 || !strings.Contains(errOut, "--edition") {
			t.Errorf("%s: vukem %v = %d %q, want a usage error about --edition", name, args, code, errOut)
		}
	}
}

// --docker, --standards and --license do not overwrite what the app ships,
// say so once, and still write what the app lacks.
func TestFinanceFlagsDoNotOverwrite(t *testing.T) {
	t.Parallel()

	plain, _ := generateFinance(t)
	project, out := generateFinance(t, "--docker", "--standards", "--license", "mit", "--ci", "--deploy", "railway")

	for _, rel := range []string{"compose.yaml", "SECURITY.md", ".editorconfig", "package.json", "README.md"} {
		if read(t, project, rel) != read(t, plain, rel) {
			t.Errorf("%s was changed by the flags", rel)
		}
	}

	line := ""

	for l := range strings.SplitSeq(out, "\n") {
		if strings.Contains(l, "kept the app's") {
			line = l
		}
	}

	for _, want := range []string{"compose.yaml", "SECURITY.md"} {
		if !strings.Contains(line, want) {
			t.Errorf("no single line says the app's %s was kept:\n%s", want, out)
		}
	}

	// What the app does not ship is written.
	for _, rel := range []string{
		"LICENSE", "CONTRIBUTING.md", "CODE_OF_CONDUCT.md", "CHANGELOG.md", ".githooks/commit-msg", ".config/cliff.toml",
		".github/workflows/ci.yml", ".railway/railway.ts",
	} {
		requireFile(t, project, rel)
	}

	// The app's MIT text stays when the project has a license of its own.
	requireFile(t, project, "LICENSE.vukem-finances")
	requireNoFile(t, project, "NOTICE")

	notice := read(t, plain, "NOTICE")
	if !strings.Contains(notice, "MIT License") {
		t.Errorf("NOTICE is not the app's MIT text:\n%s", notice)
	}

	requireNoFile(t, plain, "LICENSE")
	requireNoFile(t, plain, "LICENSE.vukem-finances")
}

func TestFinanceRefusesConflictingFlags(t *testing.T) {
	t.Parallel()

	for _, extra := range [][]string{{"--pm", "npm"}, {"--pm", "pnpm"}, {"--assets-image", "oven/bun:1.4.4"}} {
		args := append([]string{"new", "app", "--example", "finance"}, extra...)

		code, _, errOut := vukem(t, t.TempDir(), args...)
		if code != 2 || !strings.Contains(errOut, extra[0]) {
			t.Errorf("vukem %v = %d %q, want a usage error naming %s", args, code, errOut, extra[0])
		}
	}
}

func TestFinanceExampleNeedsTheReactStack(t *testing.T) {
	t.Parallel()

	for _, flag := range [][]string{{"--api"}, {"--templ"}, {"--minimal"}, {"--inertia", "vue"}, {"--inertia", "svelte"}} {
		dir := t.TempDir()
		args := append([]string{"new", "app", "--example", "finance"}, flag...)

		code, _, errOut := vukem(t, dir, args...)
		if code != 2 || !strings.Contains(errOut, "--example") && !strings.Contains(errOut, "finance") {
			t.Errorf("vukem %v = %d %q, want a usage error about --example finance", args, code, errOut)
		}
	}
}

func TestSeedRequiresFinanceExample(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	code, _, errOut := vukem(t, dir, "new", "app", "--example", "todo", "--seed", "--no-install", "--pm", "npm")
	if code != 2 || !strings.Contains(errOut, "--seed") {
		t.Fatalf("vukem new --example todo --seed = %d %q", code, errOut)
	}
}

func TestFinanceSeedFlag(t *testing.T) {
	t.Parallel()

	repo := financeFrameworkRepo(t)

	plain := filepath.Join(t.TempDir(), "plain")
	if err := os.MkdirAll(plain, 0o750); err != nil {
		t.Fatal(err)
	}

	code, out, errOut := vukem(t, plain, "new", "app", "--example", "finance", "--module", "example.com/plain",
		"--framework", repo, "--no-install")
	if code != 0 {
		t.Fatalf("vukem new without --seed = %d\n%s%s", code, out, errOut)
	}

	project := filepath.Join(plain, "app")
	if code, out, errOut := vukem(t, project, "migrate"); code != 0 {
		t.Fatalf("migrate = %d\n%s%s", code, out, errOut)
	}

	if got := sqliteCount(t, project, "select count(*) from users"); got != "0" {
		t.Fatalf("no-seed users = %s, want 0", got)
	}

	seeded := filepath.Join(t.TempDir(), "seeded")
	if err := os.MkdirAll(seeded, 0o750); err != nil {
		t.Fatal(err)
	}

	code, out, errOut = vukem(t, seeded, "new", "money", "--example", "finance", "--seed",
		"--module", "example.com/money", "--framework", repo, "--no-install")
	if code != 0 {
		t.Fatalf("vukem new --seed = %d\n%s%s", code, out, errOut)
	}

	money := filepath.Join(seeded, "money")
	if got := sqliteCount(t, money, "select count(*) from users"); got != "1" {
		t.Fatalf("seeded users = %s, want 1", got)
	}

	if got := sqliteCount(t, money, "select count(*) from transactions"); got != "32" {
		t.Fatalf("seeded transactions = %s, want 32", got)
	}

	if code, out, errOut = vukem(t, money, "db:seed"); code != 0 {
		t.Fatalf("repeated db:seed = %d\n%s%s", code, out, errOut)
	}

	if got := sqliteCount(t, money, "select count(*) from users"); got != "1" {
		t.Fatalf("repeated seed users = %s, want 1", got)
	}

	if got := sqliteCount(t, money, "select count(*) from transactions"); got != "32" {
		t.Fatalf("repeated seed transactions = %s, want 32", got)
	}
}

func sqliteCount(t *testing.T, project, query string) string {
	t.Helper()

	cmd := exec.Command("python3", "-c", `
import sqlite3, sys
con = sqlite3.connect(sys.argv[1])
print(con.execute(sys.argv[2]).fetchone()[0])
`, filepath.Join(project, "storage", "database.sqlite"), query)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sqlite %s: %v\n%s", query, err, out)
	}

	return strings.TrimSpace(string(out))
}
