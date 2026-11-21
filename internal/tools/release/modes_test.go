package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestPreviousDescribeArgsMatchProductTags(t *testing.T) {
	t.Parallel()

	args := previousDescribeArgs("HEAD^")
	if !slices.Contains(args, "--match") || !slices.Contains(args, "v[0-9]*") {
		t.Fatalf("previousDescribeArgs = %v, want --match v[0-9]*", args)
	}
}

func TestPrepareAllowsAReleaseBranch(t *testing.T) {
	root := gitReleaseRepo(t, "release/v0.1.0", "feat: start")
	t.Chdir(root)

	err := run([]string{"-prepare", "v0.1.0"}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("prepare with no changelog succeeded")
	}

	if strings.Contains(err.Error(), "releases are cut from main") || strings.Contains(err.Error(), "created on main") {
		t.Fatalf("prepare on release/v0.1.0 = %v, want it to pass the branch check", err)
	}
}

func TestTagRefusesAReleaseBranch(t *testing.T) {
	root := gitReleaseRepo(t, "release/v0.1.0", "chore(release): v0.1.0")
	t.Chdir(root)

	err := run([]string{"-tag", "v0.1.0"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "main") {
		t.Fatalf("run(-tag) on a release branch = %v, want an error naming main", err)
	}
}

func TestTagRequiresThePrepareCommit(t *testing.T) {
	root := gitReleaseRepo(t, "main", "feat: not a release")
	t.Chdir(root)

	err := run([]string{"-tag", "v0.1.0"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "chore(release): v0.1.0") {
		t.Fatalf("run(-tag) = %v, want the prepare subject", err)
	}
}

func TestGoReleaserExcludesNonProductTags(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}

	patterns := ignoreTagPatterns(t, filepath.Join(repoRoot(t), ".config", "goreleaser.yaml"))
	if len(patterns) == 0 {
		t.Fatal("goreleaser.yaml has no git.ignore_tags")
	}

	if len(patterns) == 1 && patterns[0] == "ffmpeg-*" {
		t.Fatal("ignore_tags only special-cases ffmpeg-*")
	}

	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()

		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t",
			"GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t",
			"GIT_COMMITTER_EMAIL=t@example.com",
		)

		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}

	git("init", "-b", "main")
	git("commit", "--allow-empty", "-m", "first")

	for _, tag := range []string{"v0.1.0", "v0.2.0-rc.1", "ffmpeg-9.0.2-2", "contrib/engine/v0.2.0", "nightly"} {
		git("tag", tag)
	}

	git("commit", "--allow-empty", "-m", "second")
	git("tag", "v0.2.0")

	args := []string{"describe", "--tags", "--abbrev=0", "HEAD^"}
	for _, pattern := range patterns {
		args = append(args, "--exclude="+pattern)
	}

	cmd := exec.Command("git", args...)
	cmd.Dir = root

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git describe: %v: %s", err, out)
	}

	if got := strings.TrimSpace(string(out)); got != "v0.2.0-rc.1" && got != "v0.1.0" {
		t.Fatalf("previous tag = %q, want a product version (v0.1.0 or v0.2.0-rc.1)", got)
	}

	for _, banned := range []string{"ffmpeg-9.0.2-2", "contrib/engine/v0.2.0", "nightly"} {
		if strings.TrimSpace(string(out)) == banned {
			t.Fatalf("previous tag = %q, want a product version", banned)
		}
	}
}

func TestRequiredJobNamesAreStatic(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	ci, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}

	rules, err := os.ReadFile(filepath.Join(root, ".config", "ruleset-protect-main.json"))
	if err != nil {
		t.Fatal(err)
	}

	var doc struct {
		Rules []struct {
			Type       string `json:"type"`
			Parameters struct {
				Checks []struct {
					Context string `json:"context"`
				} `json:"required_status_checks"`
			} `json:"parameters"`
		} `json:"rules"`
	}

	if err := json.Unmarshal(rules, &doc); err != nil {
		t.Fatal(err)
	}

	var contexts []string

	for _, rule := range doc.Rules {
		if rule.Type != "required_status_checks" {
			continue
		}

		for _, check := range rule.Parameters.Checks {
			contexts = append(contexts, check.Context)
		}
	}

	if len(contexts) == 0 {
		t.Fatal("ruleset lists no required checks")
	}

	text := string(ci)

	for _, name := range contexts {
		if strings.Contains(name, "${{") {
			t.Errorf("ruleset context %q contains an expression", name)
		}

		if !strings.Contains(text, "name: "+name+"\n") && !strings.Contains(text, "name: "+name+"\r\n") {
			t.Errorf("ci.yml has no static job name %q", name)
		}
	}

	if strings.Contains(text, "name: Test (${{ matrix") {
		t.Error("a required Test job name still uses a matrix expression")
	}
}

func TestReleaseDocsStateThePRFlow(t *testing.T) {
	t.Parallel()

	maint := readRepoFile(t, "docs/maintenance.md")

	for _, phrase := range []string{"-prepare", "-tag", "100", "draft", "same commit", "one pull request"} {
		if !strings.Contains(strings.ToLower(maint), strings.ToLower(phrase)) {
			t.Errorf("docs/maintenance.md lacks %q", phrase)
		}
	}

	for _, rel := range []string{
		".github/workflows/release.yml",
		".agents/skills/vukem-release/SKILL.md",
		".agents/skills/vukem-release/assets/app-release.yml",
	} {
		body := readRepoFile(t, rel)

		if !strings.Contains(body, "-image-tags") || !strings.Contains(strings.ToLower(body), "highest stable") {
			t.Errorf("%s does not state the highest-stable -image-tags rule", rel)
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}

	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(repoRoot(t), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}

	return string(body)
}

func ignoreTagPatterns(t *testing.T, path string) []string {
	t.Helper()

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var patterns []string

	in := false

	for line := range strings.Lines(string(body)) {
		trim := strings.TrimSpace(line)
		if !in {
			if trim == "ignore_tags:" {
				in = true
			}

			continue
		}

		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}

		if !strings.HasPrefix(trim, "-") {
			break
		}

		item := strings.Trim(strings.TrimSpace(strings.TrimPrefix(trim, "-")), `"'`)
		if item != "" {
			patterns = append(patterns, item)
		}
	}

	return patterns
}

func gitReleaseRepo(t *testing.T, branch, message string) string {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}

	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()

		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t",
			"GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t",
			"GIT_COMMITTER_EMAIL=t@example.com",
		)

		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}

	run("init", "-b", branch)
	run("config", "user.name", "t")
	run("config", "user.email", "t@example.com")
	run("commit", "--allow-empty", "-m", message)

	return root
}
