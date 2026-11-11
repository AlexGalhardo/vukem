package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
)

const stripeDir = "contrib/stripe"

var (
	// The go and toolchain directives follow a repo-wide Go bump (every module's go.mod changes
	// at once), like the core requirement, so they alone do not warrant a tag.
	toolchainRe  = regexp.MustCompile(`^(go|toolchain)\s+\S+\s*$`)
	breakingRe   = regexp.MustCompile(`^[a-z]+(\([^)]*\))?!:`)
	stripePinRe  = regexp.MustCompile(`(stripeVersion\s*=\s*")v\d+\.\d+\.\d+(")`)
	moduleTagRe  = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)
	onlyModuleRe = regexp.MustCompile(`^(drivers|contrib)/[A-Za-z0-9._-]+$`)
)

// moduleTag is one row of the plan.
type moduleTag struct {
	Dir    string // "drivers/redis"
	Last   string // "v0.6.0", "" when the module has no tag
	Next   string // "v0.6.1", "" when unchanged
	Reason string // "unchanged", "first tag", "3 files changed", "breaking: <subject>", "forced"
}

// moduleDirs lists the published nested modules under root, relative to it
// with forward slashes, sorted: drivers/* and contrib/* that hold a go.mod.
// examples/* are not published.
func moduleDirs(root string) []string {
	var dirs []string

	for _, group := range []string{"drivers", "contrib"} {
		mods, _ := filepath.Glob(filepath.Join(root, group, "*", "go.mod"))
		for _, m := range mods {
			dirs = append(dirs, group+"/"+filepath.Base(filepath.Dir(m)))
		}
	}

	slices.Sort(dirs)

	return dirs
}

// publishable reports whether a module changed in a way consumers can see.
// files are the changed paths relative to the module directory; gomodDiff is
// the unified diff of its go.mod ("" when unchanged). Tests, testdata,
// Markdown and go.sum never count; go.mod counts only when a changed line is
// not the core module requirement or a go/toolchain directive.
func publishable(files []string, gomodDiff string) (bool, int) {
	n := 0

	for _, f := range files {
		f = filepath.ToSlash(f)

		switch {
		case strings.HasSuffix(f, "_test.go"), strings.HasSuffix(f, ".md"), f == "go.sum",
			f == "testdata" || strings.HasPrefix(f, "testdata/") || strings.Contains(f, "/testdata/"):
		case f == "go.mod":
			if gomodChanged(gomodDiff) {
				n++
			}
		default:
			n++
		}
	}

	return n > 0, n
}

func gomodChanged(diff string) bool {
	for line := range strings.SplitSeq(diff, "\n") {
		if line == "" || strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") {
			continue
		}

		if (line[0] == '+' || line[0] == '-') && !requireRe.MatchString(line[1:]) &&
			!toolchainRe.MatchString(line[1:]) {
			return true
		}
	}

	return false
}

// breaking returns the subject of the first breaking commit in log (the
// output of git log --format=%s%n%b%x00), or "". A "!" buried in the body of
// a squash commit is not detected; force the version with -only instead.
func breaking(log string) string {
	for commit := range strings.SplitSeq(log, "\x00") {
		lines := strings.Split(strings.TrimLeft(commit, "\n"), "\n")
		if lines[0] == "" {
			continue
		}

		if breakingRe.MatchString(lines[0]) {
			return lines[0]
		}

		for _, l := range lines[1:] {
			if strings.HasPrefix(l, "BREAKING CHANGE:") {
				return lines[0]
			}
		}
	}

	return ""
}

func parseTag(v string) ([3]int, error) {
	m := moduleTagRe.FindStringSubmatch(v)
	if m == nil {
		return [3]int{}, fmt.Errorf("invalid version %q", v)
	}

	var n [3]int

	for i := range n {
		n[i], _ = strconv.Atoi(m[i+1])
	}

	return n, nil
}

// nextVersion returns the tag after last: v0.1.0 when last is "", a minor
// bump for a breaking change below v1 (a major bump from v1 on), a patch bump
// otherwise.
func nextVersion(last string, isBreaking bool) (string, error) {
	if last == "" {
		return "v0.1.0", nil
	}

	n, err := parseTag(last)
	if err != nil {
		return "", err
	}

	switch {
	case isBreaking && n[0] == 0:
		n[1]++
		n[2] = 0
	case isBreaking:
		n[0]++
		n[1], n[2] = 0, 0
	default:
		n[2]++
	}

	return fmt.Sprintf("v%d.%d.%d", n[0], n[1], n[2]), nil
}

// parseOnly parses the -only value: dir[=vX.Y.Z],...
func parseOnly(s string) (map[string]string, error) {
	only := map[string]string{}

	if s == "" {
		return only, nil
	}

	for sel := range strings.SplitSeq(s, ",") {
		dir, version, forced := strings.Cut(sel, "=")
		if !onlyModuleRe.MatchString(dir) {
			return nil, fmt.Errorf("-only %q: want drivers/<name> or contrib/<name>", sel)
		}

		if forced && !validVersion(version) {
			return nil, fmt.Errorf("-only %q: invalid version %q", sel, version)
		}

		only[dir] = version
	}

	return only, nil
}

// bumpStripePin rewrites the stripeVersion constant in src (the text of
// internal/cli/billing.go) and reports whether it matched.
func bumpStripePin(src, version string) (string, bool) {
	if !stripePinRe.MatchString(src) {
		return src, false
	}

	return stripePinRe.ReplaceAllString(src, "${1}"+version+"${2}"), true
}

func lastTag(root, dir string) (string, error) {
	out, err := gitIn(root, "tag", "-l", dir+"/v*", "--sort=-v:refname")
	if err != nil {
		return "", err
	}

	first, _, _ := strings.Cut(strings.TrimSpace(out), "\n")

	return strings.TrimPrefix(strings.TrimSpace(first), dir+"/"), nil
}

// planOne decides the tag for one module; forced is "" when the version is computed.
func planOne(root, dir, forced string) (moduleTag, error) {
	last, err := lastTag(root, dir)
	if err != nil {
		return moduleTag{}, err
	}

	row := moduleTag{Dir: dir, Last: last}

	if forced != "" {
		return forceVersion(root, row, forced)
	}

	if last == "" {
		row.Next, row.Reason = "v0.1.0", "first tag"

		return row, nil
	}

	row.Reason = "unchanged"
	since := dir + "/" + last + "..HEAD"

	names, err := gitIn(root, "diff", "--name-only", since, "--", dir)
	if err != nil {
		return moduleTag{}, err
	}

	var files []string

	for f := range strings.SplitSeq(strings.TrimSpace(names), "\n") {
		if f != "" {
			files = append(files, strings.TrimPrefix(f, dir+"/"))
		}
	}

	gomod, err := gitIn(root, "diff", since, "--", dir+"/go.mod")
	if err != nil {
		return moduleTag{}, err
	}

	changed, n := publishable(files, gomod)
	if !changed {
		return row, nil
	}

	log, err := gitIn(root, "log", "--format=%s%n%b%x00", since, "--", dir)
	if err != nil {
		return moduleTag{}, err
	}

	subject := breaking(log)

	row.Next, err = nextVersion(last, subject != "")
	if err != nil {
		return moduleTag{}, fmt.Errorf("%s: %w", dir, err)
	}

	row.Reason = fmt.Sprintf("%d files changed", n)
	if subject != "" {
		row.Reason = "breaking: " + subject
	}

	return row, nil
}

func forceVersion(root string, row moduleTag, forced string) (moduleTag, error) {
	want, err := parseTag(forced)
	if err != nil {
		return moduleTag{}, fmt.Errorf("%s: %w", row.Dir, err)
	}

	if row.Last != "" {
		have, err := parseTag(row.Last)
		if err != nil {
			return moduleTag{}, fmt.Errorf("%s: %w", row.Dir, err)
		}

		if slices.Compare(want[:], have[:]) <= 0 {
			return moduleTag{}, fmt.Errorf(
				"%s: forced %s is not greater than the last tag %s",
				row.Dir,
				forced,
				row.Last,
			)
		}
	}

	exists, err := gitIn(root, "tag", "-l", row.Dir+"/"+forced)
	if err != nil {
		return moduleTag{}, err
	}

	if strings.TrimSpace(exists) != "" {
		return moduleTag{}, fmt.Errorf("%s: tag %s/%s already exists", row.Dir, row.Dir, forced)
	}

	row.Next, row.Reason = forced, "forced"

	return row, nil
}

// planModules computes the plan from git in root. only maps a module
// directory to a forced version ("" = computed); an empty map means every
// module.
func planModules(root string, only map[string]string) ([]moduleTag, error) {
	dirs := moduleDirs(root)

	for dir := range only {
		if !slices.Contains(dirs, dir) {
			return nil, fmt.Errorf("-only %s: not a published module", dir)
		}
	}

	var plan []moduleTag

	for _, dir := range dirs {
		forced, selected := only[dir]
		if len(only) > 0 && !selected {
			continue
		}

		row, err := planOne(root, dir, forced)
		if err != nil {
			return nil, err
		}

		plan = append(plan, row)
	}

	return plan, nil
}

func printPlan(out io.Writer, plan []moduleTag) error {
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)

	fmt.Fprintln(tw, "MODULE\tLAST\tNEXT\tREASON")

	for _, r := range plan {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Dir, dash(r.Last), dash(r.Next), r.Reason)
	}

	return tw.Flush()
}

func dash(s string) string {
	if s == "" {
		return "-"
	}

	return s
}

func checkPreconditions(root string) error {
	branch, err := gitIn(root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return err
	}

	if b := strings.TrimSpace(branch); b != "main" {
		return fmt.Errorf("releases are cut from main, not %q", b)
	}

	dirty, err := gitIn(root, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return err
	}

	if strings.TrimSpace(dirty) != "" {
		return fmt.Errorf("working tree is not clean:\n%s", dirty)
	}

	head, _ := gitIn(root, "rev-parse", "HEAD")
	origin, err := gitIn(root, "rev-parse", "origin/main")

	if err != nil || strings.TrimSpace(head) != strings.TrimSpace(origin) {
		return errors.New("HEAD is not origin/main: pull or push main first")
	}

	return nil
}

// testModules runs the tests of every module in the plan against the
// published core (no workspace) before any tag exists.
func testModules(root string, out io.Writer, plan []moduleTag) error {
	for _, r := range plan {
		if r.Next == "" {
			continue
		}

		fmt.Fprintf(out, "testing %s (GOWORK=off go test ./...)... ", r.Dir)

		cmd := exec.Command("go", "test", "./...") //nolint:noctx // maintainer tool
		cmd.Dir = filepath.Join(root, filepath.FromSlash(r.Dir))
		cmd.Env = append(os.Environ(), "GOWORK=off")

		if b, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintln(out, "FAILED")

			return fmt.Errorf(
				"%s: tests fail against the published core (warm the proxy if the core is new): %w\n%s",
				r.Dir,
				err,
				b,
			)
		}

		fmt.Fprintln(out, "ok")
	}

	return nil
}

func createTags(root string, out io.Writer, plan []moduleTag) ([]string, error) {
	var tags []string

	for _, r := range plan {
		if r.Next == "" {
			continue
		}

		tag := r.Dir + "/" + r.Next
		if _, err := gitIn(root, "tag", "-a", tag, "-m", tag); err != nil {
			return tags, err
		}

		fmt.Fprintln(out, "tagged", tag)

		tags = append(tags, tag)
	}

	return tags, nil
}

// pushTags pushes one tag per git push: GitHub starts no workflow for a
// large batch, and a pushed tag is never moved.
func pushTags(root string, out io.Writer, tags []string) error {
	for i, tag := range tags {
		if _, err := gitIn(root, "push", "origin", tag); err != nil {
			return fmt.Errorf("push %s: %w\nnot pushed yet: %s", tag, err, strings.Join(tags[i:], " "))
		}

		fmt.Fprintln(out, "pushed", tag)
	}

	return nil
}

func pinStripe(root string, out io.Writer, plan []moduleTag) error {
	for _, r := range plan {
		if r.Dir != stripeDir || r.Next == "" {
			continue
		}

		p := filepath.Join(root, "internal", "cli", "billing.go")

		src, err := os.ReadFile(p)
		if err != nil {
			return err
		}

		updated, ok := bumpStripePin(string(src), r.Next)
		if !ok {
			return errors.New("internal/cli/billing.go: stripeVersion constant not found")
		}

		if err := os.WriteFile(p, []byte(updated), 0o644); err != nil { //nolint:gosec // G306: source file
			return err
		}

		fmt.Fprintf(
			out,
			"internal/cli/billing.go: stripeVersion -> %s (commit it through a PR: fix(cli): pin contrib/stripe %s in install:billing)\n",
			r.Next,
			r.Next,
		)
	}

	return nil
}

// runModules is the -modules mode.
func runModules(root string, out io.Writer, dryRun, push bool, only string) error {
	sel, err := parseOnly(only)
	if err != nil {
		return err
	}

	if !dryRun {
		if err := checkPreconditions(root); err != nil {
			return err
		}
	}

	plan, err := planModules(root, sel)
	if err != nil {
		return err
	}

	if err := printPlan(out, plan); err != nil {
		return err
	}

	if dryRun {
		return nil
	}

	if !slices.ContainsFunc(plan, func(r moduleTag) bool { return r.Next != "" }) {
		fmt.Fprintln(out, "nothing to tag")

		return nil
	}

	if err := testModules(root, out, plan); err != nil {
		return err
	}

	tags, err := createTags(root, out, plan)
	if err != nil {
		return err
	}

	if push {
		if err := pushTags(root, out, tags); err != nil {
			return err
		}
	} else {
		fmt.Fprintln(out, "push each tag on its own: git push origin <tag>")

		for _, t := range tags {
			fmt.Fprintln(out, "  "+t)
		}
	}

	return pinStripe(root, out, plan)
}
