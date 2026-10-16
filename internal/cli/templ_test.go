package cli

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestSnapshotWatchesTemplFiles(t *testing.T) {
	t.Parallel()

	dir := project(t)
	env := &Env{Dir: dir}

	a, err := takeSnapshot(dir)
	if err != nil {
		t.Fatal(err)
	}

	write := func(rel string) {
		t.Helper()

		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(full, []byte("package views\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// Generated files are rebuilt from the .templ sources, so they do not
	// trigger a rebuild of their own.
	write("resources/views/home_templ.go")

	b, _ := takeSnapshot(dir)
	if a.changed(b) || hasTempl(env) {
		t.Fatalf("generated file: changed = %v, hasTempl = %v, want false, false", a.changed(b), hasTempl(env))
	}

	write("resources/views/home.templ")

	c, _ := takeSnapshot(dir)
	if !b.changed(c) || !hasTempl(env) {
		t.Fatalf(".templ file: changed = %v, hasTempl = %v, want true, true", b.changed(c), hasTempl(env))
	}
}

// templFiles maps every generated *_templ.go file under dir to its content.
func templFiles(t *testing.T, dir string) map[string]string {
	t.Helper()

	files := map[string]string{}

	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, "_templ.go") {
			return err
		}

		b, err := os.ReadFile(p)
		files[p] = string(b)

		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	return files
}

// TestNewTemplProjectEndToEnd creates a templ project, checks its generated
// components are up to date, runs its tests and, when npm is available,
// builds it.
func TestNewTemplProjectEndToEnd(t *testing.T) {
	t.Parallel()
	requireGo(t)

	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()

	code, out, errOut := vukem(t, dir, "new", "app", "--templ", "--module", "example.com/app", "--framework", repo,
		"--no-install")
	if code != 0 {
		t.Fatalf("vukem new --templ = %d\n%s%s", code, out, errOut)
	}

	project := filepath.Join(dir, "app")

	for _, gone := range []string{"resources/views/layouts", "resources/views/posts", "resources/views/home.html"} {
		if _, err := os.Stat(filepath.Join(project, filepath.FromSlash(gone))); err == nil {
			t.Errorf("templ starter kept the html/template file %s", gone)
		}
	}

	if app := read(t, project, "bootstrap/app.go"); strings.Contains(app, "WithViews") {
		t.Errorf("bootstrap/app.go still enables html/template views:\n%s", app)
	}

	if mod := read(t, project, "go.mod"); !strings.Contains(mod, "tool github.com/a-h/templ/cmd/templ") {
		t.Errorf("go.mod has no templ tool directive:\n%s", mod)
	}

	generated := templFiles(t, project)
	for _, name := range []string{"layout", "home", "posts"} {
		if _, ok := generated[filepath.Join(project, "resources", "views", name+"_templ.go")]; !ok {
			t.Errorf("vukem new did not generate resources/views/%s_templ.go", name)
		}
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

	// The committed *_templ.go files must match the .templ sources.
	run("go", "tool", "templ", "generate")

	if diff := cmp.Diff(generated, templFiles(t, project)); diff != "" {
		t.Errorf("templ generate changed the generated files (-before +after):\n%s", diff)
	}

	run("go", "vet", "./...")
	run("go", "test", "./...")

	if code, _, errOut := vukem(t, project, "install:auth"); code != 2 || !strings.Contains(errOut, "templ") {
		t.Errorf("install:auth on a templ project = %d %s", code, errOut)
	}

	if code, _, errOut := vukem(t, project, "build"); code != 1 || !strings.Contains(errOut, "npm install") {
		t.Fatalf("vukem build without node_modules = %d %s", code, errOut)
	}

	if _, err := exec.LookPath(npm()); err != nil {
		t.Log("npm not found: skipping npm install and vite build")
		return
	}

	run(npm(), "install", "--no-audit", "--no-fund")

	if code, out, errOut := vukem(t, project, "build"); code != 0 {
		t.Fatalf("vukem build = %d\n%s%s", code, out, errOut)
	}

	if m := read(
		t,
		project,
		"resources/public/build/.vite/manifest.json",
	); !strings.Contains(
		m,
		"resources/js/app.ts",
	) {
		t.Errorf("manifest lacks resources/js/app.ts:\n%s", m)
	}
}
