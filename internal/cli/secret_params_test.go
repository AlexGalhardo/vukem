package cli

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var routeWithToken = regexp.MustCompile(`\br\.(Get|Post|Put|Patch|Delete)\(.*\{token\}`)

func TestSecurity_A09_ScaffoldSecretParams(t *testing.T) {
	t.Parallel()

	err := filepath.WalkDir("scaffolds", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".tmpl") {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(data), "\n") {
			if routeWithToken.MatchString(line) && !strings.Contains(line, `.SecretParams("token")`) {
				t.Errorf("%s:%d: route with {token} is missing .SecretParams(\"token\")", path, i+1)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
