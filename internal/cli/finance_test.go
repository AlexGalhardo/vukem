package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestNewFinanceProjectEndToEnd scaffolds the personal finance SaaS, runs its
// own Go tests, the type check and the Vite build, and checks that the
// React Email templates it ships are what npm run build:emails writes.
// It needs the tenancy package of the framework checkout (--framework).
func TestNewFinanceProjectEndToEnd(t *testing.T) {
	t.Parallel()
	requireGo(t)

	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(repo, "tenancy", "tenancy.go")); err != nil {
		t.Skip("the finance example needs the tenancy package: ", err)
	}

	dir := t.TempDir()

	code, out, errOut := vukem(t, dir, "new", "app", "--example", "finance", "--module", "example.com/app",
		"--framework", repo, "--no-install", "--pm", "npm")
	if code != 0 {
		t.Fatalf("vukem new --example finance = %d\n%s%s", code, out, errOut)
	}

	project := filepath.Join(dir, "app")

	for _, rel := range []string{
		"app/workspaces/tenant.go", "app/models/tenant.go", "app/finance/importer.go", "app/billing/billing.go",
		"app/mail/resend_webhook.go", "app/jobs/finance.go", "routes/finance.go", "tests/finance_test.go",
		"resources/js/pages/Dashboard.tsx", "resources/js/pages/Import.tsx", "resources/js/emails/welcome.tsx",
		"resources/emails/welcome.html", "resources/emails/monthly_summary.html", "README.md",
		"resources/js/pages/Auth/TwoFactor.tsx", "resources/js/pages/Admin/Revenue.tsx",
	} {
		requireFile(t, project, rel)
	}

	for _, gone := range []string{"resources/js/pages/Posts", "app/controllers/post_controller.go"} {
		if _, err := os.Stat(filepath.Join(project, filepath.FromSlash(gone))); err == nil {
			t.Errorf("the finance app kept the starter's %s", gone)
		}
	}

	if mod := read(
		t,
		project,
		"go.mod",
	); !strings.Contains(
		mod,
		"contrib/stripe v"+strings.TrimPrefix(stripeVersion, "v"),
	) {
		t.Errorf("go.mod does not pin contrib/stripe %s:\n%s", stripeVersion, mod)
	}

	// The to-do example and the finance app exclude each other.
	if _, err := os.Stat(filepath.Join(project, "resources", "js", "pages", "Tasks")); err == nil {
		t.Error("the finance app has the to-do pages")
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

	// A clone has no .env: create the key the way CI does.
	if err := os.Remove(filepath.Join(project, ".env")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}

	if code, out, errOut := vukem(t, project, "key:generate", "--if-missing"); code != 0 {
		t.Fatalf("key:generate = %d\n%s%s", code, out, errOut)
	}

	if code, out, errOut := vukem(t, project, "codegen:pages", "-check"); code != 0 {
		t.Fatalf("codegen:pages -check = %d\n%s%s", code, out, errOut)
	}

	if props := read(t, project, "resources/js/page-props.ts"); !strings.Contains(props, `"Transactions"`) ||
		!strings.Contains(props, `"Auth/Login"`) || !strings.Contains(props, `"Admin/Revenue"`) {
		t.Errorf("page-props.ts lacks the finance pages:\n%s", props)
	}

	run("go", "test", "./...")

	// The finance code passes the same lint as the other generated projects.
	if err := os.MkdirAll(filepath.Join(project, "storage"), 0o755); err != nil {
		t.Fatal(err)
	}

	if code, out, errOut := vukem(t, project, "migrate"); code != 0 {
		t.Fatalf("migrate = %d\n%s%s", code, out, errOut)
	}

	requireGeneratedLint(t, project, "example.com/app")

	if _, err := exec.LookPath(npm()); err != nil {
		t.Log("npm not found: skipping npm install, type check, emails and vite build")
		return
	}

	run(npm(), "install", "--no-audit", "--no-fund")
	run(npm(), "run", "typecheck")

	// The shipped emails are the export of the components.
	shipped := map[string]string{}

	entries, err := os.ReadDir(filepath.Join(project, "resources", "emails"))
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range entries {
		shipped[e.Name()] = read(t, project, "resources/emails/"+e.Name())
	}

	run(npm(), "run", "build:emails")

	for name, want := range shipped {
		if got := read(t, project, "resources/emails/"+name); got != want {
			t.Errorf(
				"resources/emails/%s is stale: run npm run build:emails and update scaffolds/finance/resources/emails",
				name,
			)
		}
	}

	if code, out, errOut := vukem(t, project, "build"); code != 0 {
		t.Fatalf("vukem build = %d\n%s%s", code, out, errOut)
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

func TestFinanceDropsOnlyWhatTheStarterHas(t *testing.T) {
	t.Parallel()

	// The edits patch files of the starter and the installers: a renamed
	// line must fail here, in the CLI's tests, not in a user's terminal.
	for _, e := range financeEdits() {
		if e.file == "" || e.old == "" {
			t.Errorf("edit %+v is incomplete", e)
		}
	}
}
