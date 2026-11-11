package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	imageRef  = regexp.MustCompile(`^\S+:\S+@sha256:[0-9a-f]{64}$`)
	fromLine  = regexp.MustCompile(`^FROM\s+(\S+)`)
	imageLine = regexp.MustCompile(`^\s*image:\s*(\S+)`)
)

func TestScaffoldImagesArePinnedByDigest(t *testing.T) {
	t.Parallel()

	refs := map[string]string{"nodeImage": nodeImage, "bunImage": bunImage}

	for _, file := range []string{
		filepath.Join("docker", "Dockerfile.tmpl"),
		filepath.Join("standards", "compose.yaml.tmpl"),
	} {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}

		for i, line := range strings.Split(string(b), "\n") {
			m := fromLine.FindStringSubmatch(line)
			if m == nil {
				m = imageLine.FindStringSubmatch(line)
			}

			// {{.JSImage}} is covered by nodeImage and bunImage; a user-chosen
			// --assets-image is validated by pinnedAssetsImage.
			if len(m) == 0 || strings.Contains(m[1], "{{") {
				continue
			}

			refs[fmt.Sprintf("%s:%d", file, i+1)] = m[1]
		}
	}

	for where, ref := range refs {
		if !imageRef.MatchString(ref) {
			t.Errorf("%s: image %q is not pinned as name:tag@sha256:<64 hex>", where, ref)
		}
	}
}
