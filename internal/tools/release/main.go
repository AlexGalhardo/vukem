// Command release cuts a release at the end of a PLAN.md task group:
//
//	go run ./internal/tools/release [-push] [-ref <commit>] vX.Y.Z
//
// It requires a clean tree on main, generates the version's changelog
// section with git-cliff (Keep a Changelog 1.1.0), inserts it below
// [Unreleased], commits "chore(release): vX.Y.Z", creates an annotated tag
// (on -ref when cutting a retroactive release) and, with -push, pushes and
// publishes a GitHub release whose notes are that section.
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

	fmt.Fprintf(out, "CHANGELOG.md updated for %s (previous %s)\n", version, orNone(previous))

	if *noCommit {
		return nil
	}

	msg := "chore(release): " + version
	steps := [][]string{
		{"git", "add", "CHANGELOG.md"},
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

		return nil
	}

	fmt.Fprintf(out, "tagged %s; push with: git push origin main %s\n", version, version)

	return nil
}

// publish creates the GitHub release with notes; v0.x and pre-releases are
// marked as pre-release.
func publish(version, notes string) error {
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
