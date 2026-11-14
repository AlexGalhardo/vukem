package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// VF-029: the generators write imports from the module path plus the app's
// folder, whether go.mod sits in the app folder or above it.
func TestMakeGeneratorsFindTheModuleRoot(t *testing.T) {
	t.Parallel()

	t.Run("go.mod in the app folder", func(t *testing.T) {
		t.Parallel()

		dir := project(t)

		if code, out, errOut := vukem(t, dir, "make:policy", "Post"); code != 0 {
			t.Fatalf("make:policy = %d\n%s%s", code, out, errOut)
		}

		if got := read(t, dir, "app/policies/post_policy.go"); !strings.Contains(got, `"example.com/blog/app/models"`) {
			t.Errorf("policy imports = \n%s\nwant example.com/blog/app/models", got)
		}
	})

	t.Run("go.mod above the app folder", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		app := filepath.Join(root, "web")

		// The fixture project, moved to web/, with its go.mod moved to the root
		// under another module path, as in a repository with the app in a folder.
		src := project(t)

		mod, err := os.ReadFile(filepath.Join(src, "go.mod"))
		if err != nil {
			t.Fatal(err)
		}

		mono := strings.Replace(string(mod), "module example.com/blog", "module example.com/mono", 1)
		if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(mono), 0o600); err != nil {
			t.Fatal(err)
		}

		if err := os.Remove(filepath.Join(src, "go.mod")); err != nil {
			t.Fatal(err)
		}

		if err := os.Rename(src, app); err != nil {
			t.Fatal(err)
		}

		if code, out, errOut := vukem(t, app, "make:policy", "Post"); code != 0 {
			t.Fatalf("make:policy = %d\n%s%s", code, out, errOut)
		}

		got := read(t, app, "app/policies/post_policy.go")
		if !strings.Contains(got, `"example.com/mono/web/app/models"`) {
			t.Errorf("policy imports = \n%s\nwant example.com/mono/web/app/models", got)
		}

		if fileExists(t, root, "app/policies/post_policy.go") {
			t.Error("the file was written at the module root, not in the app folder")
		}
	})

	t.Run("no go.mod anywhere", func(t *testing.T) {
		t.Parallel()

		code, _, errOut := vukem(t, t.TempDir(), "make:policy", "Post")
		if code == 0 || !strings.Contains(errOut, "no go.mod here") {
			t.Errorf("make:policy = %d %q, want the no go.mod error", code, errOut)
		}
	})
}
