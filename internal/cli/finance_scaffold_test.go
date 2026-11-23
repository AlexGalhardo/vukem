package cli

import (
	"strings"
	"testing"
)

// TestFinanceScaffoldMatchesManifest hashes the contents of the embedded
// archive and compares them with the embedded MANIFEST.sha256, so a changed,
// added or removed file fails. Replace both only with
// scripts/update-finance-scaffold.sh.
func TestFinanceScaffoldMatchesManifest(t *testing.T) {
	t.Parallel()

	files, err := loadFinanceScaffold()
	if err != nil {
		t.Fatal(err)
	}

	if len(files) < 1000 {
		t.Errorf("the finance scaffold has %d files, want the whole app", len(files))
	}

	var version string

	for _, f := range files {
		if f.rel == financeVersionName {
			version = strings.TrimSpace(string(f.content))
		}
	}

	if version != financeScaffoldTag {
		t.Errorf("VERSION in the archive = %q, financeScaffoldTag = %q", version, financeScaffoldTag)
	}
}

func TestFinanceScaffoldVerifyFailsOnChange(t *testing.T) {
	t.Parallel()

	files, err := financeScaffoldFiles()
	if err != nil {
		t.Fatal(err)
	}

	manifest, err := scaffoldFS.ReadFile(financeManifestPath)
	if err != nil {
		t.Fatal(err)
	}

	if err := verifyFinanceScaffold(files, manifest); err != nil {
		t.Fatalf("pristine archive: %v", err)
	}

	pick := func() int {
		for i, f := range files {
			if f.rel != financeManifestName && f.rel != financeVersionName {
				return i
			}
		}

		t.Fatal("no file to change")

		return -1
	}

	tests := map[string]func([]financeFile) []financeFile{
		"edited file": func(fs []financeFile) []financeFile {
			fs[pick()].content = append(append([]byte{}, fs[pick()].content...), '\n')
			return fs
		},
		"extra file": func(fs []financeFile) []financeFile {
			return append(fs, financeFile{rel: "extra.txt", content: []byte("x")})
		},
		"missing file": func(fs []financeFile) []financeFile {
			i := pick()
			return append(fs[:i], fs[i+1:]...)
		},
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			copied := append([]financeFile(nil), files...)
			if err := verifyFinanceScaffold(mutate(copied), manifest); err == nil {
				t.Error("verifyFinanceScaffold accepted a changed archive")
			}
		})
	}
}
