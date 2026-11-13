package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestAbsoluteBuildOutputDoesNotDoubleARelativeProjectDir(t *testing.T) {
	t.Parallel()

	env := &Env{Dir: "money"}
	rel := filepath.Join(".vukem", "bin", "app")

	for _, out := range []string{rel, filepath.Join("money", rel)} {
		got, err := absoluteBuildOutput(env, out)
		if err != nil {
			t.Fatal(err)
		}

		if !filepath.IsAbs(got) {
			t.Fatalf("absoluteBuildOutput(%q) = %s, want an absolute path", out, got)
		}

		if strings.Contains(got, filepath.Join("money", "money")) {
			t.Fatalf("absoluteBuildOutput(%q) = %s, joined the project directory twice", out, got)
		}

		if !strings.HasSuffix(got, filepath.Join("money", rel)) {
			t.Fatalf("absoluteBuildOutput(%q) = %s, want a suffix %s", out, got, filepath.Join("money", rel))
		}
	}
}
