package testutil

import (
	"os"
	"testing"
)

// Symlink creates a symlink at newname pointing to oldname. When the
// platform refuses (Windows without developer mode), the test is skipped
// locally but fails on GitHub Actions: a security test that cannot create
// its link there would silently stop protecting anything.
func Symlink(tb testing.TB, oldname, newname string) {
	tb.Helper()

	err := os.Symlink(oldname, newname)
	if err == nil {
		return
	}

	if os.Getenv("GITHUB_ACTIONS") == "true" {
		tb.Fatalf("symlinks unavailable in CI: %v", err)
	}

	tb.Skipf("symlinks unavailable: %v", err)
}
