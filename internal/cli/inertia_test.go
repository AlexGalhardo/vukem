package cli

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

			ssrEntry := strings.Replace(st.entry, "app.", "ssr.", 1)
			if ssr := read(t, project, ssrEntry); strings.Contains(ssr, "export ") ||
				!strings.Contains(ssr, "\ncreateInertiaApp({") {
				t.Errorf("%s must call createInertiaApp at the top level:\n%s", ssrEntry, ssr)
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

			if code, out, errOut := vukem(t, project, "install:auth", "--passkeys"); code != 0 ||
				!strings.Contains(out, "created routes/passkeys.go") {
				t.Fatalf("install:auth --passkeys = %d\n%s%s", code, out, errOut)
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

			if code, out, errOut := vukem(t, project, "build", "-ssr"); code != 0 {
				t.Fatalf("vukem build -ssr = %d\n%s%s", code, out, errOut)
			}

			// The SSR build lands in bootstrap/ssr and leaves the client build alone.
			if _, err := os.Stat(filepath.Join(project, "bootstrap", "ssr", "ssr.js")); err != nil {
				t.Errorf("SSR bundle: %v", err)
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

// install:inertia replaces package.json with the scaffold's, so it has to
// carry the project's package manager across: otherwise the next command
// goes back to guessing from a lockfile the project may not have, and
// advises an install with a manager the project does not use.
func TestInstallInertiaKeepsThePackageManager(t *testing.T) {
	t.Parallel()
	requireGo(t)

	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()

	code, out, errOut := vukem(t, dir, "new", "app", "--module", "example.com/app",
		"--framework", repo, "--no-install", "--pm", "npm")
	if code != 0 {
		t.Fatalf("vukem new = %d\n%s%s", code, out, errOut)
	}

	project := filepath.Join(dir, "app")
	if pkg := read(t, project, "package.json"); !strings.Contains(pkg, `"packageManager": "npm@`) {
		t.Fatalf("package.json does not record the manager:\n%s", pkg)
	}

	if code, out, errOut := vukem(t, project, "install:inertia", "--react", "--force", "--no-install"); code != 0 {
		t.Fatalf("install:inertia = %d\n%s%s", code, out, errOut)
	}

	if pkg := read(t, project, "package.json"); !strings.Contains(pkg, `"packageManager": "npm@`) {
		t.Fatalf("install:inertia dropped the manager:\n%s", pkg)
	}

	// The project has no lockfile, so this is the advice the reported bug
	// got wrong: it named whichever manager the machine happens to have.
	if code, _, errOut := vukem(t, project, "build"); code != 1 ||
		!strings.Contains(errOut, "run npm install first") {
		t.Fatalf("vukem build = %d %q, want the refusal to name npm", code, errOut)
	}
}

// A80: the demo resource's links live inside the demo. A project that
// removes it — the first thing a real project does — must not be left with
// navigation pointing at a route that is gone, and go build, go vet and tsc
// all stay green when it is, so only this check catches it.
//
// demo.ts is the single exception: it owns the link and gates it on an
// import.meta.glob over the demo's own pages, so the link disappears with
// them. Everything else may reach the demo route only through demo.ts.
func TestInertiaDemoLinksStayInsideTheDemo(t *testing.T) {
	t.Parallel()

	// Anything naming the demo route or its label.
	demoRef := regexp.MustCompile(`/posts|Posts`)

	for _, st := range inertiaStarters {
		t.Run(st.name, func(t *testing.T) {
			t.Parallel()

			root := "scaffolds/inertia-" + st.name + "/resources/js"

			owners, offenders := 0, []string(nil)

			err := fs.WalkDir(scaffoldFS, root, func(p string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return err
				}

				b, err := scaffoldFS.ReadFile(p)
				if err != nil || !demoRef.Match(b) {
					return err
				}

				rel := strings.TrimPrefix(p, root+"/")

				// The demo owns its own pages, its form, and demo.ts.
				if rel == "demo.ts" || strings.HasPrefix(rel, "pages/Posts/") ||
					strings.HasPrefix(rel, "components/PostForm.") {
					owners++

					return nil
				}

				offenders = append(offenders, rel)

				return nil
			})
			if err != nil {
				t.Fatal(err)
			}

			if len(offenders) > 0 {
				t.Errorf("these files reach the demo resource outside it: %v", offenders)
			}

			// Guards the guard: if the demo's own files stopped naming the
			// route, the check above would pass on an empty set.
			if owners == 0 {
				t.Error("no file names the demo route; this check would pass on anything")
			}

			// demo.ts must gate on the demo's pages, not on a wider glob:
			// one that still matches after the demo is gone leaves
			// demoInstalled true and the link rendered.
			demo, err := scaffoldFS.ReadFile(root + "/demo.ts")
			if err != nil {
				t.Fatal(err)
			}

			if !strings.Contains(string(demo), `import.meta.glob("./pages/Posts/`) {
				t.Errorf("demo.ts does not gate on the demo's own pages:\n%s", demo)
			}

			// ... and on the glob being empty. ">= 0" is always true, so
			// the link would survive the demo it belongs to.
			if !strings.Contains(string(demo), "Object.keys(demoPages).length > 0") {
				t.Errorf("demoInstalled is not the glob being non-empty:\n%s", demo)
			}

			// A file cannot render the link without the gate: demoLink and
			// demoInstalled are imported together or not at all.
			requireGateTravelsWithTheLink(t, root)
		})
	}
}

// requireGateTravelsWithTheLink fails if any file under root imports the
// demo link without the flag that hides it. That pairing is what keeps the
// link renderable only while the demo is there, and it is cheap to break
// by accident while editing a layout.
func requireGateTravelsWithTheLink(t *testing.T, root string) {
	t.Helper()

	users := 0

	err := fs.WalkDir(scaffoldFS, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasSuffix(p, "/demo.ts") {
			return err
		}

		b, err := scaffoldFS.ReadFile(p)
		if err != nil || !strings.Contains(string(b), "demoLink") {
			return err
		}

		users++

		if !strings.Contains(string(b), "demoInstalled") {
			t.Errorf("%s uses demoLink without demoInstalled", strings.TrimPrefix(p, root+"/"))
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if users == 0 {
		t.Error("nothing imports demoLink; this check would pass on anything")
	}
}
