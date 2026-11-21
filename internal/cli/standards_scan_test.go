package cli

import (
	"os"
	"strings"
	"testing"
)

// Betterleaks 1.10 ignores .betterleaks.toml unless it is named, so a generated
// project would fail its own CI on every password label.
func TestGeneratedSecretScansNameTheirConfig(t *testing.T) {
	t.Parallel()

	sources := map[string]string{"minimal package.json": minimalPackageJSON}

	for _, path := range []string{"standards/githooks/pre-commit", "standards/workflows/ci.yml.tmpl", "standards.go"} {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}

		sources[path] = string(src)
	}

	for name, src := range sources {
		found := false

		for line := range strings.Lines(src) {
			if !strings.Contains(line, "betterleaks git") {
				continue
			}

			found = true

			if !strings.Contains(line, "--config .betterleaks.toml") {
				t.Errorf("%s runs the scan without its config: %s", name, strings.TrimSpace(line))
			}
		}

		if !found {
			t.Errorf("%s has no scan command", name)
		}
	}
}
