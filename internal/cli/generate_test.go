package cli

import (
	"errors"
	"io/fs"
	"os"
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
