package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"runtime"
	"strings"
	"testing"
)

func financeTestArchive(t *testing.T, name string, kind byte) []byte {
	t.Helper()

	var buf bytes.Buffer

	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	body := []byte("x")
	if kind != tar.TypeReg {
		body = nil
	}

	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: kind}); err != nil {
		t.Fatal(err)
	}

	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}

	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	return buf.Bytes()
}

func TestFinanceArchiveRefusesUnsafeEntries(t *testing.T) {
	t.Parallel()

	unsafe := []string{"../evil", "a/../../evil", "/etc/passwd", `a\b`, "a/./b"}
	if runtime.GOOS == "windows" {
		unsafe = append(unsafe, "C:/evil", "NUL")
	}

	for _, name := range unsafe {
		if _, err := readFinanceArchive(financeTestArchive(t, name, tar.TypeReg)); err == nil {
			t.Errorf("the archive entry %q was accepted", name)
		}
	}

	if _, err := readFinanceArchive(financeTestArchive(t, "link", tar.TypeSymlink)); err == nil {
		t.Error("a symbolic link was accepted")
	}

	if _, err := readFinanceArchive([]byte("not a gzip stream")); err == nil {
		t.Error("a corrupt archive was accepted")
	}

	files, err := readFinanceArchive(financeTestArchive(t, "app/main.go", tar.TypeReg))
	if err != nil || len(files) != 1 || files[0].rel != "app/main.go" {
		t.Errorf("a plain entry: files %v, err %v", files, err)
	}
}

func TestParseFinanceManifestRefusesBadLines(t *testing.T) {
	t.Parallel()

	sum := strings.Repeat("a", 64)

	for name, src := range map[string]string{
		"no separator": sum + " file",
		"short hash":   "abc  file",
		"no name":      sum + "  ",
		"listed twice": sum + "  file\n" + sum + "  file",
	} {
		if _, err := parseManifest([]byte(src)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestFinanceFlagChecks(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		o    newOpts
		kind string
		ok   bool
	}{
		{"no edition on any example", newOpts{}, exampleTodo, true},
		{"personal", newOpts{edition: editionPersonal}, exampleFinance, true},
		{"saas", newOpts{edition: editionSaaS}, exampleFinance, true},
		{"unknown edition", newOpts{edition: "team"}, exampleFinance, false},
		{"edition on another example", newOpts{edition: editionSaaS}, exampleTodo, false},
	} {
		if err := checkEdition(tt.o, tt.kind); (err == nil) != tt.ok {
			t.Errorf("checkEdition %s: %v", tt.name, err)
		}
	}

	for _, tt := range []struct {
		name string
		o    newOpts
		ok   bool
	}{
		{"defaults", newOpts{}, true},
		{"bun", newOpts{pmFlag: "BUN"}, true},
		{"npm", newOpts{pmFlag: "npm"}, false},
		{"assets image", newOpts{assetsImage: "node:24"}, false},
	} {
		if err := checkFinanceFlags(tt.o); (err == nil) != tt.ok {
			t.Errorf("checkFinanceFlags %s: %v", tt.name, err)
		}
	}
}

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
