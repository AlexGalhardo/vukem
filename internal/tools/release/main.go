// Command release cuts a release at the end of a PLAN.md task group:
//
//	go run ./internal/tools/release [-push] [-ref <commit>] vX.Y.Z
//	go run ./internal/tools/release -prepare vX.Y.Z
//	go run ./internal/tools/release -tag [-push] vX.Y.Z
//	go run ./internal/tools/release -image-tags [-tags-file path] vX.Y.Z image...
//
// -prepare writes the changelog and the chore(release) commit on any branch
// (including release/vX.Y.Z) and does not tag. -tag creates the annotated tag
// only on main, and only when that commit is the tip and its changelog section
// is what the commits on main generate (a rebase merge replaces the commit ids
// -prepare linked: run -prepare again then). -publish-only refuses a section
// that links a commit the tag does not contain. -image-tags prints the
// registry tags for a version: X.Y and latest only when it is the highest
// stable vX.Y.Z already published.
//
// It requires a clean tree on main, generates the version's changelog
// section with git-cliff (Keep a Changelog 1.1.0), inserts it below
// [Unreleased], commits "chore(release): vX.Y.Z", creates an annotated tag
// (on -ref when cutting a retroactive release) and, with -push, pushes and
// publishes a GitHub release whose notes are that section. The release commit
// also refreshes the PLAN.md status rows; after the push, nested modules
// (contrib, drivers, examples) get their vukem requirement raised in a
// follow-up commit.
//
//	go run ./internal/tools/release -modules [-dry-run] [-push] [-only dir[=vX.Y.Z],...]
//
// The -modules mode tags the changed drivers/* and contrib/* modules instead:
// it plans each module's next <dir>/vX.Y.Z tag from git, tests the changed
// modules with GOWORK=off, creates annotated tags and, with -push, pushes one
// tag per git push. It never touches the core release flow.
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

var (
	errUsageModules   = errors.New("usage: release -modules [-dry-run] [-push] [-only dir[=vX.Y.Z],...]")
	errUsageImageTags = errors.New("usage: release -image-tags [-tags-file path] vX.Y.Z image")
)

// releaseFlags is the parsed command line of the release tool.
type releaseFlags struct {
	push        bool
	ref         string
	noCommit    bool
	publishOnly bool
	modules     bool
	dryRun      bool
	only        string
	prepare     bool
	tagOnly     bool
	imageTags   bool
	tagsFile    string
	args        []string
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "release:", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	f, err := parseReleaseFlags(args)
	if err != nil {
		return err
	}

	switch {
	case f.imageTags:
		return runImageTagMode(out, f)
	case f.modules || f.dryRun || f.only != "":
		return runModuleFlags(out, f)
	default:
		return runVersion(out, f)
	}
}

func parseReleaseFlags(args []string) (releaseFlags, error) {
	fs := flag.NewFlagSet("release", flag.ContinueOnError)
	f := releaseFlags{}
	fs.BoolVar(&f.push, "push", false, "push the commit and tag and create the GitHub release")
	fs.StringVar(&f.ref, "ref", "HEAD", "commit to tag (use an older commit for retroactive releases)")
	fs.BoolVar(&f.noCommit, "no-commit", false, "only update CHANGELOG.md (batch retroactive releases)")
	fs.BoolVar(&f.publishOnly, "publish-only", false, "only create the GitHub release for an existing pushed tag")
	fs.BoolVar(&f.modules, "modules", false, "tag changed drivers/* and contrib/* modules")
	fs.BoolVar(&f.dryRun, "dry-run", false, "with -modules: print the plan and exit")
	fs.StringVar(&f.only, "only", "", "with -modules: limit to dir[=vX.Y.Z],... (forces the version)")
	fs.BoolVar(&f.prepare, "prepare", false, "write the changelog commit on any branch; do not tag")
	fs.BoolVar(&f.tagOnly, "tag", false, "tag vX.Y.Z on main when HEAD is the chore(release) commit")
	fs.BoolVar(
		&f.imageTags,
		"image-tags",
		false,
		"print image tags; latest and X.Y only for the highest stable version",
	)
	fs.StringVar(&f.tagsFile, "tags-file", "", "with -image-tags: published tag names, one per line")

	if err := fs.Parse(args); err != nil {
		return releaseFlags{}, err
	}

	f.args = fs.Args()

	return f, nil
}

func runImageTagMode(out io.Writer, f releaseFlags) error {
	mixed := f.modules || f.prepare || f.tagOnly || f.publishOnly || f.noCommit || f.push || f.dryRun || f.only != "" ||
		f.ref != "HEAD"
	if mixed || len(f.args) < 2 || !validVersion(canonicalTag(f.args[0])) {
		return errUsageImageTags
	}

	return printImageTags(out, f.args[0], f.args[1:], f.tagsFile)
}

func runModuleFlags(out io.Writer, f releaseFlags) error {
	if !f.modules {
		return errUsageModules
	}

	plain := len(f.args) == 0 && f.ref == "HEAD" && !f.noCommit && !f.publishOnly && !f.prepare && !f.tagOnly

	return runModulesMode(plain, out, f.dryRun, f.push, f.only)
}

func runVersion(out io.Writer, f releaseFlags) error {
	if len(f.args) != 1 || !validVersion(f.args[0]) {
		return errors.New("usage: release [-push] [-ref commit] vX.Y.Z")
	}

	version := f.args[0]

	switch {
	case f.publishOnly:
		return publishExisting(version)
	case f.prepare && f.tagOnly:
		return errors.New("use -prepare and -tag in separate runs")
	case f.prepare && (f.push || f.noCommit):
		return errors.New("-prepare only writes the changelog commit; open one pull request, then run -tag on main")
	case f.tagOnly:
		return tagOnMain(out, version, f.push)
	default:
		return cutRelease(out, f, version)
	}
}

func cutRelease(out io.Writer, f releaseFlags, version string) error {
	branch, err := releaseBranch(f.prepare)
	if err != nil {
		return err
	}

	if dirty := git("status", "--porcelain", "--untracked-files=no"); dirty != "" && !f.noCommit {
		return fmt.Errorf("working tree is not clean:\n%s", dirty)
	}

	updated, err := writeChangelog(f.ref, version, !f.noCommit)
	if err != nil {
		return err
	}

	fmt.Fprintf(
		out,
		"CHANGELOG.md updated for %s (previous %s)\n",
		version,
		orNone(git(previousDescribeArgs(f.ref+"^")...)),
	)

	if f.noCommit {
		return nil
	}

	if err := commitRelease(f, version); err != nil {
		return err
	}

	return finishRelease(out, f, version, branch, updated)
}

func releaseBranch(prepare bool) (string, error) {
	branch := git("rev-parse", "--abbrev-ref", "HEAD")
	if prepare {
		if branch == "" || branch == "HEAD" {
			return "", fmt.Errorf("-prepare needs a branch, not %q", branch)
		}

		return branch, nil
	}

	if branch != "main" {
		return "", fmt.Errorf("releases are cut from main, not %q", branch)
	}

	return branch, nil
}

// generateSection renders the changelog section of version from a git range
// with git-cliff. Tests replace it.
var generateSection = func(rangeArg, version string) (string, error) {
	return output(
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
}

// buildSection generates version's section from the commits between the
// previous release and ref, and returns it with that previous tag.
func buildSection(ref, version string) (section, previous string, err error) {
	previous = git(previousDescribeArgs(ref + "^")...)
	rangeArg := ref

	if previous != "" {
		rangeArg = previous + ".." + ref
	}

	section, err = generateSection(rangeArg, version)
	if err != nil {
		return "", "", fmt.Errorf("git cliff: %w", err)
	}

	if !strings.Contains(section, "### ") {
		section = strings.TrimSpace(section) + "\n\n### Changed\n\n- Maintenance release (no user-facing changes).\n"
	}

	return section, previous, nil
}

func writeChangelog(ref, version string, bumpPlan bool) (string, error) {
	section, previous, err := buildSection(ref, version)
	if err != nil {
		return "", err
	}

	current, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		return "", err
	}

	// A section that a previous -prepare wrote and that was never tagged is
	// regenerated: after a rebase merge its commit links point at commits
	// the tag will not contain.
	text := string(current)
	if strings.Contains(text, "## ["+strings.TrimPrefix(version, "v")+"]") {
		if git("rev-parse", "--verify", "--quiet", "refs/tags/"+version) != "" {
			return "", fmt.Errorf("%s is already tagged: its section is never regenerated", version)
		}

		text = dropSection(text, version)
	}

	updated, err := insertRelease(text, section, version, previous)
	if err != nil {
		return "", err
	}

	if err := os.WriteFile("CHANGELOG.md", []byte(updated), 0o600); err != nil {
		return "", err
	}

	if bumpPlan {
		if err := bumpPlanFile(); err != nil {
			return "", err
		}
	}

	return updated, nil
}

func commitRelease(f releaseFlags, version string) error {
	steps := [][]string{
		{"git", "add", "CHANGELOG.md", "docs/PLAN.md"},
		{"git", "commit", "-m", "chore(release): " + version},
	}

	if !f.prepare {
		steps = append(steps, []string{"git", "tag", "-a", version, "-m", version, tagTarget(f.ref)})
	}

	if f.push {
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

	return nil
}

func finishRelease(out io.Writer, f releaseFlags, version, branch, updated string) error {
	if f.push {
		if err := publish(version, releaseNotes(updated, version)); err != nil {
			return err
		}

		fmt.Fprintln(out, "released", version)

		return bumpNestedModules(out, version)
	}

	if f.prepare {
		fmt.Fprintf(
			out,
			"prepared %s on %s; open one pull request and rebase-merge it (GitHub refuses a rebase merge above 100 commits), then run -tag on main\n",
			version,
			branch,
		)

		return nil
	}

	fmt.Fprintf(out, "tagged %s; push with: git push origin main %s\n", version, version)

	return nil
}

// previousDescribeArgs finds the previous core product tag. The match keeps
// module tags and pins (ffmpeg-*, anything that is not vX.Y.Z) out of the
// range git-cliff reads.
func previousDescribeArgs(ref string) []string {
	return []string{"describe", "--tags", "--abbrev=0", "--match", "v[0-9]*", ref}
}

// tagOnMain creates the annotated tag for version. It refuses every branch
// other than main, and it refuses a tip that is not the prepare commit.
func tagOnMain(out io.Writer, version string, push bool) error {
	branch := git("rev-parse", "--abbrev-ref", "HEAD")
	if branch != "main" {
		return fmt.Errorf("the tag is created on main, not %q", branch)
	}

	subject := git("log", "-1", "--format=%s")
	want := "chore(release): " + version

	if subject != want {
		return fmt.Errorf("HEAD is %q, want %q", subject, want)
	}

	if dirty := git("status", "--porcelain", "--untracked-files=no"); dirty != "" {
		return fmt.Errorf("working tree is not clean:\n%s", dirty)
	}

	current, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		return err
	}

	if err := verifySectionCurrent(string(current), version, "HEAD"); err != nil {
		return err
	}

	if _, err := output("git", "tag", "-a", version, "-m", version, "HEAD"); err != nil {
		return fmt.Errorf("git tag: %w", err)
	}

	if push {
		if _, err := output("git", "push", "origin", version); err != nil {
			return fmt.Errorf("git push origin %s: %w", version, err)
		}
	}

	fmt.Fprintf(out, "tagged %s on main\n", version)

	return nil
}

func bumpPlanFile() error {
	const path = "docs/PLAN.md"

	plan, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	updated, err := updatePlanStatus(string(plan), time.Now().Format(time.DateOnly))
	if err != nil {
		return err
	}

	return os.WriteFile(path, []byte(updated), 0o600) //nolint:gosec // G703: fixed repo path
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

// isPrerelease reports whether version carries a SemVer pre-release suffix.
// v0.x tags are normal releases: published releases have always followed that.
func isPrerelease(version string) bool {
	return strings.Contains(version, "-")
}

// publish creates the GitHub release with notes; only tags with a pre-release
// suffix (-rc.N, -beta.N) are marked as pre-release. Empty notes are refused instead of published: a
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
	if isPrerelease(version) {
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

func publishExisting(version string) error {
	current, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		return err
	}

	notes := releaseNotes(string(current), version)
	if notes == "" {
		return fmt.Errorf("CHANGELOG.md has no section for %s", version)
	}

	if err := verifyLinkedCommits(string(current), version, version); err != nil {
		return err
	}

	return publish(version, notes)
}

func runModulesMode(validFlags bool, out io.Writer, dryRun, push bool, only string) error {
	if !validFlags {
		return errUsageModules
	}

	return runModules(".", out, dryRun, push, only)
}

// gitIn runs git in dir.
func gitIn(dir string, args ...string) (string, error) {
	var stdout, stderr bytes.Buffer

	cmd := exec.Command("git", args...) //nolint:noctx // maintainer tool: fixed binaries, no request context
	cmd.Dir = dir
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}

	return stdout.String(), nil
}
