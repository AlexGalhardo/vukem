package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

const coreBump = "--- a/go.mod\n+++ b/go.mod\n@@ -3 +3 @@\n-\tgithub.com/AlexGalhardo/vukem v0.39.0\n+\tgithub.com/AlexGalhardo/vukem v0.40.0\n"

func TestPublishable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		files []string
		diff  string
		want  bool
		n     int
	}{
		{"tests and testdata", []string{"client_test.go", "testdata/x.json"}, "", false, 0},
		{"markdown", []string{"README.md"}, "", false, 0},
		{"go directive bump", []string{"go.mod"}, "-go 1.27.3\n+go 1.27.4\n", false, 0},
		{"core bump only", []string{"go.mod", "go.sum"}, coreBump, false, 0},
		{
			"other requirement",
			[]string{"go.mod", "go.sum"},
			coreBump + "-\tgithub.com/redis/go-redis/v9 v9.1.0\n+\tgithub.com/redis/go-redis/v9 v9.2.0\n",
			true,
			1,
		},
		{"code and test", []string{"client.go", "client_test.go"}, "", true, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, n := publishable(tt.files, tt.diff)
			if got != tt.want || n != tt.n {
				t.Fatalf("publishable = %v, %d; want %v, %d", got, n, tt.want, tt.n)
			}
		})
	}
}

func TestBreaking(t *testing.T) {
	t.Parallel()

	tests := []struct{ name, log, want string }{
		{"bang with scope", "feat(redis)!: drop v8\n\n\x00", "feat(redis)!: drop v8"},
		{"bang without scope", "fix!: x\n\x00", "fix!: x"},
		{"footer", "fix: a\n\x00feat(redis): add y\nBREAKING CHANGE: z\n\x00", "feat(redis): add y"},
		{"none", "feat(redis): add y\nbody\n\x00fix: z\n\x00", ""},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := breaking(tt.log); got != tt.want {
				t.Fatalf("breaking = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNextVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		last     string
		breaking bool
		want     string
		wantErr  bool
	}{
		{"", false, "v0.1.0", false},
		{"v0.6.0", false, "v0.6.1", false},
		{"v0.6.3", true, "v0.7.0", false},
		{"v1.2.3", true, "v2.0.0", false},
		{"v1.2.3", false, "v1.2.4", false},
		{"banana", false, "", true},
	}

	for _, tt := range tests {
		got, err := nextVersion(tt.last, tt.breaking)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf(
				"nextVersion(%q, %v) = %q, %v; want %q (err %v)",
				tt.last,
				tt.breaking,
				got,
				err,
				tt.want,
				tt.wantErr,
			)
		}
	}
}

func TestParseOnly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in      string
		want    map[string]string
		wantErr bool
	}{
		{"", map[string]string{}, false},
		{"drivers/redis", map[string]string{"drivers/redis": ""}, false},
		{
			"drivers/redis,contrib/stripe=v0.6.0",
			map[string]string{"drivers/redis": "", "contrib/stripe": "v0.6.0"},
			false,
		},
		{"contrib/stripe=0.6", nil, true},
		{"../x", nil, true},
		{"examples/api", nil, true},
	}

	for _, tt := range tests {
		got, err := parseOnly(tt.in)
		if (err != nil) != tt.wantErr {
			t.Errorf("parseOnly(%q) err = %v, wantErr %v", tt.in, err, tt.wantErr)

			continue
		}

		if diff := cmp.Diff(tt.want, got); diff != "" {
			t.Errorf("parseOnly(%q) mismatch (-want +got):\n%s", tt.in, diff)
		}
	}
}

func TestBumpStripePin(t *testing.T) {
	t.Parallel()

	got, ok := bumpStripePin("const (\n\tstripeVersion = \"v0.5.2\"\n)\n", "v0.6.0")
	if !ok || got != "const (\n\tstripeVersion = \"v0.6.0\"\n)\n" {
		t.Fatalf("bumpStripePin = %q, %v", got, ok)
	}

	const src = "const other = 1\n"

	got, ok = bumpStripePin(src, "v0.6.0")
	if ok || got != src {
		t.Fatalf("bumpStripePin without the constant = %q, %v", got, ok)
	}
}

func TestModuleDirs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	for _, rel := range []string{
		"drivers/pg/go.mod", "contrib/new/go.mod", "examples/api/go.mod", "internal/bench/go.mod", "drivers/nomod/x.go",
	} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if diff := cmp.Diff([]string{"contrib/new", "drivers/pg"}, moduleDirs(root)); diff != "" {
		t.Fatalf("moduleDirs mismatch (-want +got):\n%s", diff)
	}
}

// gitRepo builds a temporary repository: drivers/a tagged v0.2.0 then a test-only change,
// drivers/b tagged v0.1.4 then a breaking change, contrib/c never tagged.
func gitRepo(t *testing.T) string {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}

	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()

		cmd := exec.Command("git", append([]string{
			"-c",
			"user.name=t",
			"-c",
			"user.email=t@example.com",
			"-c",
			"commit.gpgsign=false",
			"-c",
			"tag.gpgsign=false",
		}, args...)...)
		cmd.Dir = root

		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	commit := func(msg string, files map[string]string) {
		t.Helper()

		for rel, body := range files {
			p := filepath.Join(root, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}

			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}

		run("add", "-A")
		run("commit", "-m", msg)
	}

	run("init", "-b", "main")
	commit("feat: add modules", map[string]string{
		"drivers/a/go.mod": "module a\n", "drivers/a/a.go": "package a\n",
		"drivers/b/go.mod": "module b\n", "drivers/b/b.go": "package b\n",
		"contrib/c/go.mod": "module c\n", "contrib/c/c.go": "package c\n",
	})
	run("tag", "-a", "drivers/a/v0.2.0", "-m", "x")
	run("tag", "-a", "drivers/b/v0.1.4", "-m", "x")
	commit("test(a): cover x", map[string]string{"drivers/a/a_test.go": "package a\n"})
	commit("feat(b)!: drop y", map[string]string{"drivers/b/b.go": "package b\n\nvar Y = 1\n"})

	return root
}

func TestPlanModules(t *testing.T) {
	t.Parallel()

	root := gitRepo(t)

	got, err := planModules(root, nil)
	if err != nil {
		t.Fatal(err)
	}

	want := []moduleTag{
		{Dir: "contrib/c", Next: "v0.1.0", Reason: "first tag"},
		{Dir: "drivers/a", Last: "v0.2.0", Reason: "unchanged"},
		{Dir: "drivers/b", Last: "v0.1.4", Next: "v0.2.0", Reason: "breaking: feat(b)!: drop y"},
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("plan mismatch (-want +got):\n%s", diff)
	}

	got, err = planModules(root, map[string]string{"drivers/a": "v0.3.0"})
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(
		[]moduleTag{{Dir: "drivers/a", Last: "v0.2.0", Next: "v0.3.0", Reason: "forced"}},
		got,
	); diff != "" {
		t.Fatalf("forced plan mismatch (-want +got):\n%s", diff)
	}

	if _, err := planModules(root, map[string]string{"drivers/b": "v0.1.0"}); err == nil {
		t.Fatal("a forced version below the last tag was accepted")
	}
}

func TestRunModulesDryRun(t *testing.T) {
	t.Parallel()

	root := gitRepo(t)

	var buf bytes.Buffer

	if err := runModules(root, &buf, true, false, ""); err != nil {
		t.Fatal(err)
	}

	for _, line := range []string{"contrib/c", "first tag", "drivers/a", "unchanged", "drivers/b", "v0.2.0"} {
		if !strings.Contains(buf.String(), line) {
			t.Errorf("plan lacks %q:\n%s", line, buf.String())
		}
	}

	cmd := exec.Command("git", "tag", "-l")
	cmd.Dir = root

	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}

	if got := strings.Fields(string(out)); len(got) != 2 {
		t.Fatalf("dry run changed the tags: %v", got)
	}
}

func TestRunModulesRefusesWhenHeadIsNotOriginMain(t *testing.T) {
	t.Parallel()

	err := runModules(gitRepo(t), &bytes.Buffer{}, false, false, "")
	if err == nil || !strings.Contains(err.Error(), "origin/main") {
		t.Fatalf("err = %v, want one naming origin/main", err)
	}
}

func TestRunRejectsBadFlagCombinations(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"-modules", "v1.2.3"},
		{"-modules", "-publish-only"},
		{"-dry-run", "v1.2.3"},
		{"-only", "drivers/a", "v1.2.3"},
	} {
		err := run(args, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "usage") {
			t.Errorf("run(%v) = %v, want a usage error", args, err)
		}
	}
}
