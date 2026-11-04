package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallAdminUsage(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/app\n")
	writeTestFile(t, dir, "routes/web.go", "package routes\n")

	if code, _, errOut := vukem(t, dir, "install:admin"); code != 2 || !strings.Contains(errOut, "install:auth") {
		t.Errorf("install:admin without auth = %d %q", code, errOut)
	}

	if code, _, errOut := vukem(t, dir, "make:admin-resource", "Post"); code != 2 ||
		!strings.Contains(errOut, "install:admin") {
		t.Errorf("make:admin-resource without the panel = %d %q", code, errOut)
	}

	writeTestFile(t, dir, "app/admin/registry.go", "package admin\n")

	if code, _, errOut := vukem(t, dir, "make:admin-resource", "Ghost"); code != 2 ||
		!strings.Contains(errOut, "no models.Ghost") {
		t.Errorf("make:admin-resource for a missing model = %d %q", code, errOut)
	}
}

func TestInstallFlagsUsage(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/app\n")

	if code, _, errOut := vukem(t, dir, "install:flags"); code != 2 || !strings.Contains(errOut, "bootstrap/app.go") {
		t.Errorf("install:flags outside a project = %d %q", code, errOut)
	}

	writeTestFile(t, dir, "bootstrap/app.go", "package bootstrap\n")

	if code, _, errOut := vukem(t, dir, "install:flags"); code != 2 || !strings.Contains(errOut, "database") {
		t.Errorf("install:flags without migrations = %d %q", code, errOut)
	}
}

func writeTestFile(t *testing.T, dir, rel, content string) {
	t.Helper()

	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestAdminEndToEnd adds the admin panel to a new project, generates
// resources with and without a request schema, and runs the project's
// tests.
func TestAdminEndToEnd(t *testing.T) {
	t.Parallel()
	requireGo(t)

	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()

	if code, out, errOut := vukem(
		t, dir, "new", "shop", "--module", "example.com/shop", "--framework", repo, "--no-install",
	); code != 0 {
		t.Fatalf("vukem new = %d\n%s%s", code, out, errOut)
	}

	project := filepath.Join(dir, "shop")

	steps := [][]string{
		{"install:auth"},
		{"install:admin"},
		{"install:flags"},
		{"make:admin-resource", "Post"},
		{"make:model", "Tag", "-m"},
		{"make:admin-resource", "Tag"},
	}
	for _, args := range steps {
		if code, out, errOut := vukem(t, project, args...); code != 0 {
			t.Fatalf("vukem %s = %d\n%s%s", strings.Join(args, " "), code, out, errOut)
		}
	}

	registry := read(t, project, "app/admin/registry.go")
	for _, want := range []string{"Post(),", "Tag(),", "authz.For[*models.Post](PostPolicy{Admins: admins})"} {
		if !strings.Contains(registry, want) {
			t.Errorf("app/admin/registry.go lacks %q:\n%s", want, registry)
		}
	}

	if !strings.Contains(read(t, project, "app/admin/post.go"), "requests.PostSchema") {
		t.Error("the Post resource does not use requests.PostSchema")
	}

	if !strings.Contains(read(t, project, "database/migrations/registry.go"), "CreateAdminAuditLogsTable()") {
		t.Error("the audit log migration is not registered")
	}

	if !strings.Contains(read(t, project, ".env.example"), "ADMIN_EMAILS=") {
		t.Error(".env.example lacks ADMIN_EMAILS")
	}

	if code, _, errOut := vukem(t, project, "install:admin"); code != 1 || !strings.Contains(errOut, "already exist") {
		t.Errorf("second install:admin = %d %s", code, errOut)
	}

	if app := read(t, project, "bootstrap/app.go"); !strings.Contains(app, "vukem.WithFlags(features.All()...)") ||
		!strings.Contains(app, "example.com/shop/app/features") {
		t.Errorf("bootstrap/app.go does not register the flags:\n%s", app)
	}

	if !strings.Contains(read(t, project, "database/migrations/registry.go"), "CreateFeatureFlagsTable()") {
		t.Error("the feature flags migration is not registered")
	}

	// A declared flag reaches the admin page through WithFlags, the
	// migration and the panel wiring.
	writeTestFile(t, project, "app/features/features.go", `package features

import "github.com/AlexGalhardo/vukem/flags"

var Beta = flags.Bool("beta", false).Describe("Beta features")

func All() []flags.AnyFlag { return []flags.AnyFlag{Beta} }
`)
	writeTestFile(t, project, "tests/flags_page_test.go", `package tests

import "testing"

func TestFlagsPageListsBeta(t *testing.T) {
	t.Parallel()

	adminClient(t, newAdminApp(t), adminEmail).Get("/admin/_flags").AssertOK().AssertSee("Beta features")
}
`)

	// Re-running install:flags, also with --force, keeps the app's flags
	// and adds no second migration.
	for _, args := range [][]string{{"install:flags"}, {"install:flags", "--force"}} {
		if code, out, errOut := vukem(t, project, args...); code != 0 {
			t.Fatalf("vukem %s again = %d\n%s%s", strings.Join(args, " "), code, out, errOut)
		}
	}

	if !strings.Contains(read(t, project, "app/features/features.go"), "Beta features") {
		t.Error("re-running install:flags overwrote app/features/features.go")
	}

	migs, err := filepath.Glob(filepath.Join(project, "database", "migrations", "*_create_feature_flags_table.go"))
	if err != nil || len(migs) != 1 {
		t.Errorf("feature flags migrations = %v, %v, want exactly one", migs, err)
	}

	if n := strings.Count(read(t, project, "database/migrations/registry.go"), "CreateFeatureFlagsTable()"); n != 1 {
		t.Errorf("the feature flags migration is registered %d times, want 1", n)
	}

	if n := strings.Count(read(t, project, "bootstrap/app.go"), "vukem.WithFlags("); n != 1 {
		t.Errorf("bootstrap/app.go registers the flags %d times, want 1", n)
	}

	cmd := exec.CommandContext(t.Context(), "go", "test", "./...")
	cmd.Dir = project
	cmd.Env = append(os.Environ(), "GOWORK=off")

	if out, err := combinedOutput(cmd); err != nil {
		t.Fatalf("tests after install:admin: %v\n%s", err, out)
	}

	if code, out, errOut := vukem(t, project, "route:list"); code != 0 || !strings.Contains(out, "admin.posts.index") {
		t.Fatalf("route:list = %d\n%s%s", code, out, errOut)
	}
}

func TestInstallAdminStackValidation(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/app\n")
	writeTestFile(t, dir, "routes/web.go", "package routes\n")
	writeTestFile(t, dir, "app/accounts/accounts.go", "package accounts\n")

	if code, _, errOut := vukem(t, dir, "install:admin", "-stack", "angular"); code != 2 ||
		!strings.Contains(errOut, "htmx or inertia") {
		t.Errorf("install:admin -stack angular = %d %q", code, errOut)
	}

	// The default stack does not ask for Inertia, the Inertia stack does.
	if code, _, errOut := vukem(t, dir, "install:admin", "-stack", "inertia"); code != 2 ||
		!strings.Contains(errOut, "Inertia + React") {
		t.Errorf("install:admin -stack inertia without Inertia = %d %q", code, errOut)
	}

	writeTestFile(t, dir, "package.json", `{"dependencies":{"@inertiajs/vue3":"1"}}`)

	if code, _, errOut := vukem(t, dir, "install:admin", "-stack", "inertia"); code != 2 ||
		!strings.Contains(errOut, "Vue and Svelte") {
		t.Errorf("install:admin -stack inertia on Vue = %d %q", code, errOut)
	}
}

// Both stacks render a routes/admin.go that is valid Go; the Inertia one moves the panel to /admin-api and serves
// the React pages at /admin behind the panel's guard.
func TestAdminRoutesScaffoldPerStack(t *testing.T) {
	t.Parallel()

	src, err := scaffoldFS.ReadFile("scaffolds/admin/routes/admin.go.tmpl")
	if err != nil {
		t.Fatal(err)
	}

	for stack, want := range map[string][]string{
		"htmx":    {"panel.Mount(r)"},
		"inertia": {"admin.DataPrefix", "pages.Dashboard", "pages.List", "pages.Show", "panel.Guard()"},
	} {
		out, err := renderScaffold(
			"routes/admin.go",
			src,
			map[string]string{"Module": "example.com/app", "AdminStack": stack},
		)
		if err != nil {
			t.Fatalf("%s: %v", stack, err)
		}

		for _, w := range want {
			if !strings.Contains(string(out), w) {
				t.Errorf("%s routes/admin.go misses %q:\n%s", stack, w, out)
			}
		}

		if stack == "htmx" && strings.Contains(string(out), "DataPrefix") {
			t.Errorf("htmx routes/admin.go mentions the Inertia prefix:\n%s", out)
		}
	}

	pages, err := scaffoldFS.ReadFile("scaffolds/admin-inertia-react/app/admin/pages.go.tmpl")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := renderScaffold(
		"app/admin/pages.go",
		pages,
		map[string]string{"Module": "example.com/app"},
	); err != nil {
		t.Fatalf("pages.go: %v", err)
	}
}

// The generated test of the scaffold names the panel's real URLs for each stack: the audit log and flags pages live
// at /admin-api in the Inertia stack, where /admin/{slug} is a React resource route.
func TestAdminTestScaffoldPerStack(t *testing.T) {
	t.Parallel()

	src, err := scaffoldFS.ReadFile("scaffolds/admin/tests/admin_test.go.tmpl")
	if err != nil {
		t.Fatal(err)
	}

	for stack, want := range map[string]string{"htmx": `"/admin/_audit"`, "inertia": `"/admin-api/_audit"`} {
		out, err := renderScaffold(
			"tests/admin_test.go",
			src,
			map[string]string{"Module": "example.com/app", "AdminStack": stack},
		)
		if err != nil {
			t.Fatalf("%s: %v", stack, err)
		}

		if !strings.Contains(string(out), want) {
			t.Errorf("%s admin_test.go misses %s:\n%s", stack, want, out)
		}
	}
}
