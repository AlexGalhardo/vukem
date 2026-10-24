// Command release cuts a release at the end of a PLAN.md task group:
//
//	go run ./internal/tools/release [-push] [-ref <commit>] vX.Y.Z
//
// It requires a clean tree on main, generates the version's changelog
// section with git-cliff (Keep a Changelog 1.1.0), inserts it below
// [Unreleased], commits "chore(release): vX.Y.Z", creates an annotated tag
// (on -ref when cutting a retroactive release) and, with -push, pushes and
// publishes a GitHub release whose notes are that section. The release commit
// also refreshes the PLAN.md status rows; after the push, nested modules
// (contrib, drivers, examples) get their vukem requirement raised in a
// follow-up commit.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "release:", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("release", flag.ContinueOnError)
	push := fs.Bool("push", false, "push the commit and tag and create the GitHub release")
	ref := fs.String("ref", "HEAD", "commit to tag (use an older commit for retroactive releases)")
	noCommit := fs.Bool("no-commit", false, "only update CHANGELOG.md (batch retroactive releases)")
	publishOnly := fs.Bool("publish-only", false, "only create the GitHub release for an existing pushed tag")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NArg() != 1 || !validVersion(fs.Arg(0)) {
		return errors.New("usage: release [-push] [-ref commit] vX.Y.Z")
	}

	version := fs.Arg(0)

	if *publishOnly {
		current, err := os.ReadFile("CHANGELOG.md")
		if err != nil {
			return err
		}

		notes := releaseNotes(string(current), version)
		if notes == "" {
			return fmt.Errorf("CHANGELOG.md has no section for %s", version)
		}

		return publish(version, notes)
	}

	if branch := git("rev-parse", "--abbrev-ref", "HEAD"); branch != "main" {
		return fmt.Errorf("releases are cut from main, not %q", branch)
	}

	if dirty := git("status", "--porcelain", "--untracked-files=no"); dirty != "" && !*noCommit {
		return fmt.Errorf("working tree is not clean:\n%s", dirty)
	}

	// Only core tags: driver and contrib tags (drivers/x/v0.1.0) are not releases.
	previous := git("describe", "--tags", "--abbrev=0", "--match", "v[0-9]*", *ref+"^")

	rangeArg := *ref
	if previous != "" {
		rangeArg = previous + ".." + *ref
	}

	section, err := output(
		"git",
		"cliff",
		"--config",
		".config/cliff.toml",
		rangeArg,
		"--tag",
		version,
		"--strip",
		"all",
	)
	if err != nil {
		return fmt.Errorf("git cliff: %w", err)
	}

	if !strings.Contains(section, "### ") {
		section = strings.TrimSpace(section) + "\n\n### Changed\n\n- Maintenance release (no user-facing changes).\n"
	}

	current, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		return err
	}

	updated, err := insertRelease(string(current), section, version, previous)
	if err != nil {
		return err
	}

	if err := os.WriteFile("CHANGELOG.md", []byte(updated), 0o600); err != nil {
		return err
	}

	if !*noCommit {
		if err := bumpPlanFile(version); err != nil {
			return err
		}
	}

	fmt.Fprintf(out, "CHANGELOG.md updated for %s (previous %s)\n", version, orNone(previous))

	if *noCommit {
		return nil
	}

	msg := "chore(release): " + version
	steps := [][]string{
		{"git", "add", "CHANGELOG.md", "docs/PLAN.md"},
		{"git", "commit", "-m", msg},
		{"git", "tag", "-a", version, "-m", version, tagTarget(*ref)},
	}

	if *push {
		steps = append(steps,
			[]string{"git", "push", "origin", "main"},
			[]string{"git", "push", "origin", version},
		)
	}

	for _, s := range steps {
		if _, err := output(s[0], s[1:]...); err != nil {
			return fmt.Errorf("%s: %w", strings.Join(s, " "), err)
		}
	}

	if *push {
		if err := publish(version, releaseNotes(updated, version)); err != nil {
			return err
		}

		fmt.Fprintln(out, "released", version)

		return bumpNestedModules(out, version)
	}

	fmt.Fprintf(out, "tagged %s; push with: git push origin main %s\n", version, version)

	return nil
}

func bumpPlanFile(version string) error {
	const path = "docs/PLAN.md"

	plan, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	updated, err := updatePlanStatus(string(plan), version, time.Now().Format(time.DateOnly))
	if err != nil {
		return err
	}

	return os.WriteFile(path, []byte(updated), 0o600)
}

// bumpNestedModules runs once the tag is pushed: go mod tidy needs the
// version to resolve from the module proxy to write go.sum.
func bumpNestedModules(out io.Writer, version string) error {
	changed, err := bumpModules(".", version)
	if err != nil || len(changed) == 0 {
		return err
	}

	if err := tidyModules(changed); err != nil {
		return err
	}

	steps := [][]string{
		{"git", "add", "contrib", "drivers", "examples"},
		{"git", "commit", "-m", "chore(deps): require vukem " + version + " in nested modules"},
		{"git", "push", "origin", "main"},
	}

	for _, s := range steps {
		if _, err := output(s[0], s[1:]...); err != nil {
			return fmt.Errorf("%s: %w", strings.Join(s, " "), err)
		}
	}

	fmt.Fprintf(out, "bumped %d nested modules to %s\n", len(changed), version)

	return nil
}

// publish creates the GitHub release with notes; v0.x and pre-releases are
// marked as pre-release. Empty notes are refused instead of published: a
// release with an empty body looks exactly like a successful release, so a
// broken extraction would stay invisible for as long as nobody reads the
// releases page.
func publish(version, notes string) error {
	if strings.TrimSpace(notes) == "" {
		return fmt.Errorf("refusing to publish %s with an empty release body", version)
	}

	f, err := os.CreateTemp("", "release-notes-*.md")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())

	if _, err := f.WriteString(notes); err != nil {
		return err
	}

	if err := f.Close(); err != nil {
		return err
	}

	args := []string{"release", "create", version, "--title", version, "--notes-file", f.Name(), "--verify-tag"}
	if strings.HasPrefix(version, "v0.") || strings.Contains(version, "-") {
		args = append(args, "--prerelease")
	}

	_, err = output("gh", args...)

	return err
}

func tagTarget(ref string) string {
	if ref == "" {
		return "HEAD"
	}

	return ref
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}

	return s
}

func git(args ...string) string {
	s, _ := output("git", args...)
	return strings.TrimSpace(s)
}

func output(name string, args ...string) (string, error) {
	var stdout, stderr bytes.Buffer

	cmd := exec.Command(name, args...) //nolint:noctx // maintainer tool: fixed binaries, no request context
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}

	return stdout.String(), nil
}
