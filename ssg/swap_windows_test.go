//go:build windows

package ssg

import (
	"os"
	"path/filepath"
	"testing"
)

// TestGenerateSwapWithOpenHandleOnWindows only observes whether Windows lets
// swap rename a directory that holds an open file; it never fails on either outcome.
func TestGenerateSwapWithOpenHandleOnWindows(t *testing.T) {
	parent := t.TempDir()
	dst := filepath.Join(parent, "dist")
	src := filepath.Join(parent, "dist.ssg-tmp-x")

	for dir, content := range map[string]string{dst: "old", src: "new"} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(filepath.Join(dir, markerFile), []byte("m"), 0o644); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(filepath.Join(dir, "page"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	f, err := os.Open(filepath.Join(dst, "page"))
	if err != nil {
		t.Fatal(err)
	}

	defer f.Close()

	swapErr := swap(dst, src)
	t.Logf("swap with an open file in the destination: %v", swapErr)

	want := "new"
	if swapErr != nil {
		want = "old"
	}

	got, err := os.ReadFile(filepath.Join(dst, "page"))
	if err != nil {
		t.Fatal(err)
	}

	if string(got) != want {
		t.Fatalf("page = %q, want %q (swap err %v)", got, want, swapErr)
	}
}
