package cli

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func fileExists(t *testing.T, dir, rel string) bool {
	t.Helper()

	_, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}

	return err == nil
}

// A rejected flag combination must not leave a listener file behind.
func TestMakeListenerRejectsAsyncQueuedBeforeWriting(t *testing.T) {
	t.Parallel()

	dir := project(t)
	before := read(t, dir, "app/listeners/registry.go")

	code, _, errOut := vukem(t, dir, "make:listener", "Notify", "--event", "OrderShipped", "--async", "--queued")
	if code != 2 || !strings.Contains(errOut, "exclusive") {
		t.Fatalf("make:listener --async --queued = %d %q, want usage error", code, errOut)
	}

	if fileExists(t, dir, "app/listeners/notify.go") {
		t.Error("app/listeners/notify.go written, want nothing written")
	}

	if read(t, dir, "app/listeners/registry.go") != before {
		t.Error("registry changed, want it untouched")
	}
}

// Starters ship no app/policies/registry.go: make:policy creates it.
func TestMakePolicyCreatesRegistry(t *testing.T) {
	t.Parallel()

	dir := project(t)
	if err := os.Remove(filepath.Join(dir, "app", "policies", "registry.go")); err != nil {
		t.Fatal(err)
	}

	code, out, errOut := vukem(t, dir, "make:policy", "Post")
	if code != 0 {
		t.Fatalf("make:policy = %d\n%s%s", code, out, errOut)
	}

	reg := read(t, dir, "app/policies/registry.go")
	for _, want := range []string{
		"package policies",
		"func All() []authz.Policy",
		"authz.For[*models.Post](PostPolicy{})",
		`"example.com/blog/app/models"`,
	} {
		if !strings.Contains(reg, want) {
			t.Errorf("registry.go missing %q:\n%s", want, reg)
		}
	}

	if !strings.Contains(out, "created app/policies/registry.go") {
		t.Errorf("output = %q, want the registry reported as created", out)
	}
}

// make:model checks every target before writing: a clash on the
// controller leaves no model, migration or factory behind.
func TestMakeModelWritesNothingOnClash(t *testing.T) {
	t.Parallel()

	dir := project(t)

	if code, _, errOut := vukem(t, dir, "make:controller", "Post"); code != 0 {
		t.Fatalf("make:controller = %d %q", code, errOut)
	}

	before := read(t, dir, "database/migrations/registry.go")

	code, _, errOut := vukem(t, dir, "make:model", "Post", "-m", "-f", "-c")
	if code != 1 || !strings.Contains(errOut, "app/controllers/post_controller.go already exists") {
		t.Fatalf("make:model = %d %q, want the controller clash", code, errOut)
	}

	for _, rel := range []string{"app/models/post.go", "database/factories/post_factory.go"} {
		if fileExists(t, dir, rel) {
			t.Errorf("%s written, want nothing written", rel)
		}
	}

	if read(t, dir, "database/migrations/registry.go") != before {
		t.Error("migrations registry changed, want it untouched")
	}

	entries, err := os.ReadDir(filepath.Join(dir, "database", "migrations"))
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range entries {
		if strings.Contains(e.Name(), "create_posts_table") {
			t.Errorf("migration %s written, want nothing written", e.Name())
		}
	}
}

// Generated code is the first Go a developer reads in a new project, so the
// make:* stubs are held to the same .golangci.yml as the framework itself:
// a fresh app reporting findings nobody wrote teaches that the bar is
// optional. Mirrors the gate TestNewProjectEndToEnd puts on vukem new and
// the installers (A76, same class of defect in the make:* generators).
func TestGeneratedStubsPassRepositoryLint(t *testing.T) {
	t.Parallel()
	requireGo(t)

	dir := project(t)

	repoSum, err := os.ReadFile(filepath.Join("..", "..", "go.sum"))
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "go.sum"), repoSum, 0o600); err != nil {
		t.Fatal(err)
	}

	runGenerators(t, dir)

	// golangci-lint typechecks the project, so its dependencies must
	// resolve before it runs.
	tidy := exec.CommandContext(t.Context(), "go", "mod", "tidy")
	tidy.Dir = dir
	tidy.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")

	if out, err := tidy.CombinedOutput(); err != nil {
		t.Fatalf("go mod tidy: %v\n%s", err, out)
	}

	requireGeneratedLint(t, dir, "example.com/blog")
}

// A package comment the generators already wrote is enough: an installer
// shipping its own doc.go for the same package must neither refuse over it
// nor replace it.
func TestInstallKeepsAPackageCommentTheGeneratorsWrote(t *testing.T) {
	t.Parallel()

	dir := project(t)

	if code, out, errOut := vukem(t, dir, "make:mail", "WelcomeMail"); code != 0 {
		t.Fatalf("make:mail = %d\n%s%s", code, out, errOut)
	}

	before := read(t, dir, "app/mail/doc.go")

	if code, out, errOut := vukem(t, dir, "install:auth"); code != 0 {
		t.Fatalf("install:auth after make:mail = %d\n%s%s", code, out, errOut)
	}

	if got := read(t, dir, "app/mail/doc.go"); got != before {
		t.Errorf("app/mail/doc.go = %q, want it kept as %q", got, before)
	}
}
