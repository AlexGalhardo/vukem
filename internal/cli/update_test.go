package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func requireGoMod(version, extra string) []byte {
	return []byte(
		"module example.com/app\n\ngo 1.27.4\n\nrequire github.com/AlexGalhardo/vukem " + version + "\n" + extra,
	)
}

func TestFrameworkState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		gomod        []byte
		cli          string
		wantRequired string
		wantState    string
		wantErr      string
	}{
		{"up to date", requireGoMod("v0.40.0", ""), "v0.40.0", "v0.40.0", "up_to_date", ""},
		{"behind", requireGoMod("v0.38.0", ""), "v0.40.0", "v0.38.0", "behind", ""},
		{"behind numerically", requireGoMod("v0.9.0", ""), "v0.10.0", "v0.9.0", "behind", ""},
		{"ahead", requireGoMod("v0.41.0", ""), "v0.40.0", "v0.41.0", "ahead", ""},
		{
			"replaced",
			requireGoMod("v0.0.0", "\nreplace github.com/AlexGalhardo/vukem => ../vukem\n"),
			"v0.40.0", "v0.0.0", "replaced", "",
		},
		{"development build", requireGoMod("v0.38.0", ""), "devel", "v0.38.0", "devel", ""},
		{"pseudo-version CLI", requireGoMod("v0.38.0", ""), "v0.40.1-0.20261111-abcdef", "v0.38.0", "devel", ""},
		{
			"not required", []byte("module example.com/app\n\ngo 1.27.4\n"), "v0.40.0", "", "",
			"this project does not require github.com/AlexGalhardo/vukem",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			required, state, err := frameworkState(tt.gomod, tt.cli)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
			} else if err != nil {
				t.Fatal(err)
			}

			if required != tt.wantRequired || state != tt.wantState {
				t.Fatalf("got (%q, %q), want (%q, %q)", required, state, tt.wantRequired, tt.wantState)
			}
		})
	}
}

func TestFrameworkModules(t *testing.T) {
	t.Parallel()

	gomod := requireGoMod("v0.40.0", `
require (
	github.com/AlexGalhardo/vukem/drivers/postgres v0.7.0
	github.com/AlexGalhardo/vukem/contrib/stripe v0.5.2 // indirect
	github.com/other/lib v1.0.0
)
`)

	want := []moduleVersion{
		{Path: "github.com/AlexGalhardo/vukem/drivers/postgres", Version: "v0.7.0"},
		{Path: "github.com/AlexGalhardo/vukem/contrib/stripe", Version: "v0.5.2"},
	}

	if diff := cmp.Diff(want, frameworkModules(gomod)); diff != "" {
		t.Fatalf("modules mismatch (-want +got):\n%s", diff)
	}
}

func TestScaffoldDiff(t *testing.T) {
	t.Parallel()

	project, starter := t.TempDir(), t.TempDir()
	write := func(dir, rel, body string) {
		t.Helper()

		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write(starter, "same.txt", "a\nb\n")
	write(project, "same.txt", "a\nb\n")
	write(starter, "crlf.txt", "a\nb\n")
	write(project, "crlf.txt", "a\r\nb\r\n")
	write(starter, "routes/web.go", "new")
	write(project, "routes/web.go", "old")
	write(starter, "starter-only.txt", "x")
	write(project, "project-only.txt", "x")
	write(starter, "go.mod", "x")
	write(project, "go.mod", "y")
	write(starter, "storage/data.txt", "x")
	write(project, "storage/data.txt", "y")

	got, err := scaffoldDiff(project, starter)
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff([]string{"routes/web.go"}, got); diff != "" {
		t.Fatalf("scaffoldDiff mismatch (-want +got):\n%s", diff)
	}
}

func TestUpdateSteps(t *testing.T) {
	t.Parallel()

	changes := []depChange{
		{"go", "github.com/AlexGalhardo/vukem", "v0.1.0", "v0.2.0"},
		{"js", "vite", "^6.0.0", "7.0.0"},
		{"go", "github.com/other/lib", "v1.0.0", "v1.1.0"},
	}
	want := [][]string{
		{"go", "get", "github.com/AlexGalhardo/vukem@v0.2.0", "github.com/other/lib@v1.1.0"},
		{"go", "mod", "tidy"},
	}

	if diff := cmp.Diff(want, updateSteps(changes)); diff != "" {
		t.Fatalf("steps (-want +got):\n%s", diff)
	}

	if got := updateSteps(changes[1:2]); got != nil {
		t.Errorf("updateSteps(js only) = %v, want nil", got)
	}
}

// updateProject writes a minimal project: a go.mod and a vite.config.ts that differs from the starter.
func updateProject(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "go.mod"), requireGoMod("v0.1.0", ""), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "vite.config.ts"), []byte("export default {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	return dir
}

func runUpdate(t *testing.T, dir, cliVersion string, args ...string) (int, string, string) {
	t.Helper()

	var out, errOut bytes.Buffer

	code := Run(t.Context(), append([]string{"update"}, args...), Env{
		Dir: dir, Stdout: &out, Stderr: &errOut, Now: fixedNow, Version: cliVersion,
	})

	return code, out.String(), errOut.String()
}

func TestUpdateCheckWritesNothing(t *testing.T) {
	t.Parallel()

	dir := updateProject(t)
	before, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}

	code, out, errOut := runUpdate(t, dir, "v0.2.0", "--check")
	if code != 1 || !strings.Contains(out, "behind") || !strings.Contains(out, "vite.config.ts") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
	}

	if _, err := os.Stat(filepath.Join(dir, ".vukem")); !os.IsNotExist(err) {
		t.Fatalf(".vukem was created: %v", err)
	}

	after, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(before, after) {
		t.Fatal("go.mod changed")
	}
}

func TestUpdateCheckUpToDate(t *testing.T) {
	t.Parallel()

	if code, out, errOut := runUpdate(t, updateProject(t), "v0.1.0", "--check"); code != 0 {
		t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
	}
}

func TestUpdateJSON(t *testing.T) {
	t.Parallel()

	code, out, errOut := runUpdate(t, updateProject(t), "v0.2.0", "--check", "--json")
	if code != 1 {
		t.Fatalf("code=%d err=%q", code, errOut)
	}

	var got updateReport
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}

	want := updateReport{}
	want.Framework.Required, want.Framework.CLI, want.Framework.State = "v0.1.0", "v0.2.0", "behind"
	want.Modules = []moduleVersion{}
	want.Changes = []depChange{{"go", frameworkModule, "v0.1.0", "v0.2.0"}}
	want.Scaffold.Differ = []string{"vite.config.ts"}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("report mismatch (-want +got):\n%s", diff)
	}
}

func fileHashes(t *testing.T, dir string) map[string][32]byte {
	t.Helper()

	snap := map[string][32]byte{}

	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		rel, _ := filepath.Rel(dir, p)
		if strings.HasPrefix(filepath.ToSlash(rel), ".vukem/") {
			return nil
		}

		b, err := os.ReadFile(p)
		snap[filepath.ToSlash(rel)] = sha256.Sum256(b)

		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	return snap
}

func TestSecurity_A01_UpdateNeverWritesProjectFiles(t *testing.T) {
	t.Parallel()

	dir := updateProject(t)
	before := fileHashes(t, dir)

	// Up to date, so upgrade does not run go get.
	if code, out, errOut := runUpdate(t, dir, "v0.1.0"); code != 0 {
		t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
	}

	if diff := cmp.Diff(before, fileHashes(t, dir)); diff != "" {
		t.Fatalf("a project file changed (-before +after):\n%s", diff)
	}

	if _, err := os.Stat(filepath.Join(dir, ".vukem", "upgrade", "starter", "vite.config.ts")); err != nil {
		t.Fatalf("reference starter not written: %v", err)
	}
}
