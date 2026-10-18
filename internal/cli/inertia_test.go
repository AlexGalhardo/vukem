package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// inertiaFrameworks under test; each needs a scaffolds/inertia-<name>.
var inertiaStarters = []struct{ name, entry, page string }{
	{"react", "resources/js/app.tsx", "tsx"},
	{"vue", "resources/js/app.ts", "vue"},
	{"svelte", "resources/js/app.ts", "svelte"},
}

// TestNewInertiaProjectEndToEnd creates an Inertia project per framework,
// runs its Go tests and, when npm is available, type-checks and builds it.
func TestNewInertiaProjectEndToEnd(t *testing.T) {
	t.Parallel()
	requireGo(t)

	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	for _, st := range inertiaStarters {
		t.Run(st.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()

			// --pm npm: this test drives npm itself, so the project must be an npm one.
			code, out, errOut := vukem(t, dir, "new", "app", "--inertia", st.name, "--module", "example.com/app",
				"--framework", repo, "--no-install", "--pm", "npm")
			if code != 0 {
				t.Fatalf("vukem new --inertia %s = %d\n%s%s", st.name, code, out, errOut)
			}

			project := filepath.Join(dir, "app")

			for _, gone := range []string{"resources/views/posts", "resources/views/layouts", "resources/js/app.ts"} {
				if _, err := os.Stat(filepath.Join(project, filepath.FromSlash(gone))); err == nil && gone != st.entry {
					t.Errorf("%s kept the HTMX file %s", st.name, gone)
				}
			}

			if app := read(
				t,
				project,
				"bootstrap/app.go",
			); !strings.Contains(
				app,
				"vukem.WithInertia(inertia.Config{})",
			) {
				t.Errorf("bootstrap/app.go =\n%s", app)
			}

			if page := read(t, project, "resources/js/pages/Error."+st.page); !strings.Contains(page, "status") {
				t.Errorf("Error page = %q, want one showing status and message", page)
			}

			if !strings.Contains(read(t, project, "vite.config.ts"), st.entry) {
				t.Errorf("vite.config.ts does not build %s", st.entry)
			}

			run := func(name string, args ...string) {
				t.Helper()

				cmd := exec.CommandContext(t.Context(), name, args...)
				cmd.Dir = project
				cmd.Env = append(os.Environ(), "GOWORK=off")

				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%s %v: %v\n%s", name, args, err, out)
				}
			}

			run("go", "test", "./...")

			if code, out, errOut := vukem(t, project, "install:auth", "--2fa"); code != 0 ||
				!strings.Contains(out, "created resources/js/pages/Auth/TwoFactorChallenge.") ||
				!strings.Contains(out, "updated app/controllers/account/account.go") {
				t.Fatalf("install:auth --2fa = %d\n%s%s", code, out, errOut)
			}

			if _, err := os.Stat(filepath.Join(project, "resources", "views", "auth")); err == nil {
				t.Error("install:auth on an Inertia app wrote HTMX auth views")
			}

			if code, out, errOut := vukem(t, project, "install:billing"); code != 0 ||
				!strings.Contains(out, "created resources/js/pages/Billing.") ||
				strings.Contains(out, "resources/views/billing") {
				t.Fatalf("install:billing = %d\n%s%s", code, out, errOut)
			}

			run("go", "test", "./...")

			// The Go the three frontends generate is the same; linting one
			// of them covers the inertia and auth-inertia scaffolds.
			if st.name == "react" {
				requireGeneratedLint(t, project, "example.com/app")
			}

			if _, err := exec.LookPath(npm()); err != nil {
				t.Log("npm not found: skipping npm install, type check and vite build")
				return
			}

			run(npm(), "install", "--no-audit", "--no-fund")
			run(npm(), "run", "typecheck")

			if code, out, errOut := vukem(t, project, "build"); code != 0 {
				t.Fatalf("vukem build = %d\n%s%s", code, out, errOut)
			}

			if m := read(t, project, "resources/public/build/.vite/manifest.json"); !strings.Contains(m, st.entry) {
				t.Errorf("manifest lacks %s:\n%s", st.entry, m)
			}
		})
	}
}

func TestInstallInertiaUsage(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	if code, _, errOut := vukem(
		t,
		dir,
		"install:inertia",
		"--vue",
		"--svelte",
	); code != 2 ||
		!strings.Contains(errOut, "choose one") {
		t.Errorf("two frameworks = %d %q", code, errOut)
	}

	if code, _, errOut := vukem(t, dir, "install:inertia"); code != 2 || !strings.Contains(errOut, "bootstrap/app.go") {
		t.Errorf("outside a project = %d %q", code, errOut)
	}

	if err := os.MkdirAll(filepath.Join(dir, "bootstrap"), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(
		filepath.Join(dir, "bootstrap", "app.go"),
		[]byte("package bootstrap\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	if code, _, errOut := vukem(t, dir, "install:inertia"); code != 2 || !strings.Contains(errOut, "--force") {
		t.Errorf("without --force = %d %q", code, errOut)
	}
}

// A bootstrap/app.go that cannot be patched fails before any HTMX file is
// deleted.
func TestInstallInertiaKeepsFilesWhenAppCannotBePatched(t *testing.T) {
	t.Parallel()

	dir := project(t)
	page := filepath.Join(dir, "resources", "views", "home.html")

	if err := os.MkdirAll(filepath.Dir(page), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(page, []byte("<h1>home</h1>"), 0o600); err != nil {
		t.Fatal(err)
	}

	before := read(t, dir, "bootstrap/app.go")

	code, _, errOut := vukem(t, dir, "install:inertia", "--force", "--no-install")
	if code != 1 || !strings.Contains(errOut, "WithVite") {
		t.Fatalf("install:inertia without WithVite = %d %q, want the patch error", code, errOut)
	}

	if _, err := os.Stat(page); err != nil {
		t.Errorf("resources/views/home.html: %v, want it kept", err)
	}

	if read(t, dir, "bootstrap/app.go") != before {
		t.Error("bootstrap/app.go changed, want it untouched")
	}

	if _, err := os.Stat(filepath.Join(dir, "resources", "js", "pages")); err == nil {
		t.Error("Inertia pages written, want nothing written")
	}
}
