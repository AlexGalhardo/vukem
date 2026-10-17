package testutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTempDir is not parallel: it checks the directory after the subtest.
func TestTempDir(t *testing.T) {
	var dir string

	t.Run("sub/test with spaces", func(t *testing.T) {
		dir = TempDir(t)

		if base := filepath.Base(dir); !strings.HasPrefix(base, "TestTempDir_sub_test_with_spaces-") {
			t.Errorf("TempDir() base = %q, want a sanitized test name prefix", base)
		}

		if err := os.WriteFile(filepath.Join(dir, "app.db"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	})

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("Stat(%s) after cleanup = %v, want not exist", dir, err)
	}
}

func TestPatternTruncates(t *testing.T) {
	t.Parallel()

	if got := pattern(strings.Repeat("a", 100)); got != strings.Repeat("a", 64)+"-*" {
		t.Errorf("pattern(100 a) = %q, want 64 a and -*", got)
	}
}
