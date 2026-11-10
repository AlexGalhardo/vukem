package cli

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	routeLine   = regexp.MustCompile(`\br\.(Get|Post|Put|Patch|Delete)\(`)
	secretParam = regexp.MustCompile(`\{([a-zA-Z_]*(?i:token|secret|code|key|signature)[a-zA-Z_]*)\}`)
)

// TestSecurity_A09_ScaffoldSecretParams checks the scaffolds and the
// examples built from them: a path parameter named like a secret is marked
// with SecretParams so its value never reaches the logs.
func TestSecurity_A09_ScaffoldSecretParams(t *testing.T) {
	t.Parallel()

	for _, root := range []string{"scaffolds", filepath.Join("..", "..", "examples")} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			if d.IsDir() {
				if d.Name() == "node_modules" {
					return filepath.SkipDir
				}

				return nil
			}

			if !strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, ".go.tmpl") {
				return nil
			}

			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}

			for i, line := range strings.Split(string(data), "\n") {
				if !routeLine.MatchString(line) {
					continue
				}

				_, marked, _ := strings.Cut(line, ".SecretParams(")
				for _, m := range secretParam.FindAllStringSubmatch(line, -1) {
					if !strings.Contains(marked, `"`+m[1]+`"`) {
						t.Errorf("%s:%d: route with {%s} is missing .SecretParams(%q)", path, i+1, m[1], m[1])
					}
				}
			}

			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
