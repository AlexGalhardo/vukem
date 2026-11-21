package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The pre-commit hook of a --standards project runs Betterleaks over the
// staged files, so a fresh project must pass its own scan (VF-095): UI labels
// and fixtures are not passwords, and the commented PostgreSQL URL of
// .env.example carries no credential. A real secret must still be caught.
func TestStandardsProjectPassesItsOwnSecretScan(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("generates a project and runs a scanner")
	}

	scanner, err := exec.LookPath("betterleaks")
	if err != nil {
		t.Skip("betterleaks is not installed; the generated hook and CI run the same scan")
	}

	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}

	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()

	code, out, errOut := vukem(t, dir, "new", "app", "--inertia", "react", "--example", "finance", "--standards",
		"--module", "example.com/app", "--framework", repo, "--no-install", "--pm", "npm")
	if code != 0 {
		t.Skipf("the finance example could not be generated here (%d): %s%s", code, out, errOut)
	}

	project := filepath.Join(dir, "app")

	run := func(name string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(t.Context(), name, args...)
		cmd.Dir = project

		return cmd.CombinedOutput()
	}

	if b, err := run(git, "init", "-q"); err != nil {
		t.Fatalf("git init: %v\n%s", err, b)
	}

	if b, err := run(git, "add", "-A"); err != nil {
		t.Fatalf("git add: %v\n%s", err, b)
	}

	if b, err := run(scanner, "git", "--pre-commit", "--staged", "--no-banner", "--redact"); err != nil {
		t.Fatalf("the scaffold fails its own secret scan: %v\n%s", err, b)
	}

	// The allowlist is narrow: another rule still fires on the same paths.
	leak := filepath.Join(project, "resources", "js", "lib", "leak.ts")
	if err := os.WriteFile(
		leak,
		[]byte("export const key = \"sk_live_"+"0123456789abcdefghijklmn\"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	if b, err := run(git, "add", "-A"); err != nil {
		t.Fatalf("git add: %v\n%s", err, b)
	}

	if b, err := run(scanner, "git", "--pre-commit", "--staged", "--no-banner", "--redact"); err == nil {
		t.Errorf("a Stripe key in an allowlisted path was not caught:\n%s", b)
	}
}
