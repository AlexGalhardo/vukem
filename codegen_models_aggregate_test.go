package vukem

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runAggregateWithModels(t *testing.T, withModels bool, args ...string) error {
	t.Helper()

	var out bytes.Buffer

	build := func(opts ...Option) (*App, error) {
		opts = append(opts, WithLogger(discardLogger()), WithStdout(&out))
		if withModels {
			opts = append(opts, WithModels(registeredModels()...))
		}

		return New(opts...)
	}

	return RunMain(t.Context(), append([]string{"app", "codegen"}, args...), testSource(nil).Lookup, build)
}

// The aggregate keeps the typed column file current, with the package name
// the file already has, and honours -check. An app without WithModels does
// not use the file, so it is left alone.
func TestCodegenCommand_Models(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	cols := filepath.Join(dir, "app", "models", "cols", "cols_gen.go")

	if _, err := runCodegenModels(t, registeredModels(), "-o", cols, "-pkg", "mycols"); err != nil {
		t.Fatal(err)
	}

	fresh, err := os.ReadFile(cols)
	if err != nil {
		t.Fatal(err)
	}

	if err := runAggregateWithModels(t, true, "-check"); err != nil {
		t.Fatalf("codegen -check on a fresh file = %v", err)
	}

	stale := strings.Replace(string(fresh), "package mycols\n", "package mycols\n\n// stale\n", 1)
	if err := os.WriteFile(cols, []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := runAggregateWithModels(t, true, "-check"); err == nil || !strings.Contains(err.Error(), "out of date") {
		t.Fatalf("codegen -check on a stale file = %v", err)
	}

	if got, _ := os.ReadFile(cols); string(got) != stale {
		t.Error("codegen -check wrote the file")
	}

	if err := runAggregateWithModels(t, true); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(cols)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(got, fresh) {
		t.Errorf("codegen did not restore the file (package kept as mycols):\n%s", got)
	}

	// Without WithModels the file is not the app's: no error, no change.
	if err := os.WriteFile(cols, []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := runAggregateWithModels(t, false); err != nil {
		t.Errorf("codegen without WithModels = %v, want the target skipped", err)
	}

	if err := runAggregateWithModels(t, false, "-check"); err != nil {
		t.Errorf("codegen -check without WithModels = %v, want the target skipped", err)
	}

	if got, _ := os.ReadFile(cols); string(got) != stale {
		t.Error("codegen without WithModels touched the file")
	}
}

func TestPackageOf(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "x.go")
	if err := os.WriteFile(path, []byte("// Code generated.\n\npackage thing\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := packageOf(path); got != "thing" {
		t.Errorf("packageOf = %q", got)
	}

	if got := packageOf(filepath.Join(t.TempDir(), "missing.go")); got != "" {
		t.Errorf("packageOf(missing) = %q, want empty", got)
	}
}
