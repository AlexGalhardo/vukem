package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const goneCommit = "0123456789abcdef0123456789abcdef01234567"

func TestTagCreatesTheAnnotatedTagOnMain(t *testing.T) {
	root, sha := releaseRepoWithChangelog(t, "")
	t.Chdir(root)
	stubSection(t, sha)

	var buf bytes.Buffer

	if err := run([]string{"-tag", "v0.1.0"}, &buf); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(buf.String(), "tagged v0.1.0 on main") {
		t.Fatalf("output = %q, want the tag confirmation", buf.String())
	}

	if got := gitOut(t, root, "tag", "-l", "v0.1.0"); got != "v0.1.0" {
		t.Fatalf("tag list = %q, want v0.1.0", got)
	}
}

// A rebase merge replaces the commits of the pull request, so the id that
// -prepare linked is no longer in main (VF-108).
func TestTagRefusesALinkThatIsNotAnAncestor(t *testing.T) {
	root, _ := releaseRepoWithChangelog(t, goneCommit)
	t.Chdir(root)
	stubSection(t, goneCommit)

	err := run([]string{"-tag", "v0.1.0"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "0123456") || !strings.Contains(err.Error(), "-prepare v0.1.0") {
		t.Fatalf("run(-tag) = %v, want the stale commit and the -prepare hint", err)
	}

	if tags := gitOut(t, root, "tag", "-l"); tags != "" {
		t.Fatalf("a tag was created: %q", tags)
	}
}

func TestTagRefusesASectionThatMissesCommits(t *testing.T) {
	root, sha := releaseRepoWithChangelog(t, "")
	t.Chdir(root)
	stubSection(t, sha, "a commit made after -prepare")

	err := run([]string{"-tag", "v0.1.0"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "differs") {
		t.Fatalf("run(-tag) = %v, want a stale section error", err)
	}
}

func TestPrepareRegeneratesAnUntaggedSection(t *testing.T) {
	root, sha := releaseRepoWithChangelog(t, goneCommit)
	t.Chdir(root)
	stubSection(t, sha)

	if err := run([]string{"-prepare", "v0.1.0"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(root, "CHANGELOG.md"))
	if err != nil {
		t.Fatal(err)
	}

	got := string(raw)
	if strings.Contains(got, goneCommit) || !strings.Contains(got, sha) {
		t.Fatalf("CHANGELOG.md was not regenerated:\n%s", got)
	}

	if n := strings.Count(got, "## [0.1.0]"); n != 1 {
		t.Fatalf("CHANGELOG.md has %d sections for 0.1.0, want 1:\n%s", n, got)
	}

	if err := verifyLinkedCommits(got, "v0.1.0", "HEAD"); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareRefusesATaggedSection(t *testing.T) {
	root, sha := releaseRepoWithChangelog(t, "")
	t.Chdir(root)
	stubSection(t, sha)
	gitOut(t, root, "tag", "-a", "v0.1.0", "-m", "v0.1.0")

	err := run([]string{"-prepare", "v0.1.0"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "already tagged") {
		t.Fatalf("run(-prepare) on a tagged version = %v", err)
	}
}

func TestDropSection(t *testing.T) {
	t.Parallel()

	in := "# C\n\n## [Unreleased]\n\n## [0.2.0] - x\n\n### Added\n\n- a\n\n## [0.1.0] - y\n\n- b\n\n[0.1.0]: u\n"
	want := "# C\n\n## [Unreleased]\n\n## [0.1.0] - y\n\n- b\n\n[0.1.0]: u\n"

	if got := dropSection(in, "v0.2.0"); got != want {
		t.Errorf("dropSection(middle) = %q, want %q", got, want)
	}

	last := "# C\n\n## [Unreleased]\n\n## [0.1.0] - y\n\n- b\n\n[0.1.0]: u\n"
	wantLast := "# C\n\n## [Unreleased]\n\n[0.1.0]: u\n"

	if got := dropSection(last, "v0.1.0"); got != wantLast {
		t.Errorf("dropSection(last) = %q, want %q", got, wantLast)
	}

	if got := dropSection(in, "v9.9.9"); got != in {
		t.Errorf("dropSection(missing) changed the text: %q", got)
	}
}

// releaseRepoWithChangelog makes a main branch with a "feat: x" commit and
// the chore(release) tip whose CHANGELOG.md links link (the id of the feat
// commit when link is empty). It returns the repository and the feat id.
func releaseRepoWithChangelog(t *testing.T, link string) (root, sha string) {
	t.Helper()

	root = gitReleaseRepo(t, "main", "feat: x")
	sha = gitOut(t, root, "rev-parse", "HEAD")

	if link == "" {
		link = sha
	}

	changelog := "# Changelog\n\n## [Unreleased]\n\n## [0.1.0] - 2026-01-01\n\n### Added\n\n- X ([" + link[:7] +
		"](" + repoURL + "/commit/" + link + "))\n\n[Unreleased]: " + repoURL + "/compare/v0.1.0...HEAD\n" +
		"[0.1.0]: " + repoURL + "/releases/tag/v0.1.0\n"
	if err := os.WriteFile(filepath.Join(root, "CHANGELOG.md"), []byte(changelog), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o750); err != nil {
		t.Fatal(err)
	}

	plan := "| Updated | 2026-01-01 |\n| Next task | none |\n"
	if err := os.WriteFile(filepath.Join(root, "docs", "PLAN.md"), []byte(plan), 0o600); err != nil {
		t.Fatal(err)
	}

	gitOut(t, root, "add", "CHANGELOG.md", "docs/PLAN.md")
	gitOut(t, root, "commit", "-m", "chore(release): v0.1.0")

	return root, sha
}

// stubSection replaces git-cliff with a section linking sha, plus one entry
// per extra.
func stubSection(t *testing.T, sha string, extra ...string) {
	t.Helper()

	prev := generateSection

	t.Cleanup(func() { generateSection = prev })

	generateSection = func(_, version string) (string, error) {
		var b strings.Builder

		b.WriteString("## [" + strings.TrimPrefix(version, "v") + "] - 2026-02-02\n\n### Added\n\n- X ([" + sha[:7] +
			"](" + repoURL + "/commit/" + sha + "))\n")

		for _, e := range extra {
			b.WriteString("- " + e + "\n")
		}

		return b.String(), nil
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}

	return strings.TrimSpace(string(out))
}
