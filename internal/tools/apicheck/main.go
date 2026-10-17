// Command apicheck compares the exported API of the core module and of every
// published module (drivers/*, contrib/*) with its latest stable release tag,
// using golang.org/x/exp/cmd/apidiff:
//
//	go run ./internal/tools/apicheck [-major v1] [-apidiff path]
//	go run ./internal/tools/apicheck -major v0 -report docs/api/report.md
//
// Gate mode (default) fails when a module has incompatible changes since its
// latest stable tag of -major (core vX.Y.Z, nested modules drivers/x/vX.Y.Z).
// The only sanctioned override is an entry in the exception file
// (-exceptions, default .config/apicheck-exceptions.json) for a
// security-driven break; the gate also fails when an entry is malformed,
// expired or unused.
// A module without such a tag is skipped with a notice, so the gate passes
// until the first v1 tag exists. Report mode (-report) writes compatible and
// incompatible changes as Markdown and never fails because of them.
//
// Both sides are type-checked with GOWORK=off, so each module is compared as
// users get it. The old side is extracted with git archive into a temporary
// directory; release tags must be fetched (actions/checkout fetch-depth: 0).
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	err := run(ctx, os.Args[1:], os.Stdout)

	stop()

	if err != nil {
		fmt.Fprintln(os.Stderr, "apicheck:", err)
		os.Exit(1)
	}
}

// errIncompatible reports that the gate found incompatible changes.
var errIncompatible = errors.New("incompatible API changes; a breaking change needs a new major version (/v2)")

func run(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("apicheck", flag.ContinueOnError)
	major := fs.String("major", "v1", "compare with the latest stable tag of this major version")
	report := fs.String("report", "", "write a Markdown report of all changes to this file instead of gating")
	head := fs.String("head", "HEAD", "label of the compared tree in the report title")
	exceptionsFlag := fs.String(
		"exceptions",
		filepath.Join(".config", "apicheck-exceptions.json"),
		"exception file for security-driven breaks, relative to the repository root",
	)
	apidiffFlag := fs.String(
		"apidiff",
		"apidiff",
		"apidiff binary (looked up in PATH, then in go env GOBIN or GOPATH/bin)",
	)

	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NArg() != 0 {
		return errors.New(
			"usage: apicheck [-major v1] [-report file.md] [-head label] [-exceptions file.json] [-apidiff path]",
		)
	}

	apidiff, err := lookTool(ctx, *apidiffFlag)
	if err != nil {
		return err
	}

	root, err := gitOutput(ctx, "", "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}

	root = filepath.FromSlash(root)

	mods, err := discoverModules(root)
	if err != nil {
		return err
	}

	exceptionsFile := *exceptionsFlag
	if !filepath.IsAbs(exceptionsFile) {
		exceptionsFile = filepath.Join(root, exceptionsFile)
	}

	// Validated in report mode too, so a bad entry fails before any release.
	exceptions, err := loadExceptions(exceptionsFile, mods)
	if err != nil {
		return err
	}

	tags, err := gitOutput(ctx, root, "tag", "--list")
	if err != nil {
		return err
	}

	tmp, err := os.MkdirTemp("", "apicheck-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	var results []result

	for i, m := range mods {
		base, ok := latestStable(strings.Split(tags, "\n"), m.tagPrefix(), *major)
		if !ok {
			// GitHub Actions renders ::notice lines as annotations.
			fmt.Fprintf(out, "::notice title=apidiff::%s: no stable %s tag yet (%sv%s.x.y), skipping\n",
				m.Path, *major, m.tagPrefix(), strings.TrimPrefix(*major, "v"))
			results = append(results, result{Module: m, Skipped: "no stable " + *major + " tag"})

			continue
		}

		changes, err := compare(ctx, root, filepath.Join(tmp, strconv.Itoa(i)), m, base, apidiff, *report == "")
		if err != nil {
			return fmt.Errorf("%s: %w", m.Path, err)
		}

		results = append(results, result{Module: m, Base: base, Changes: changes})
	}

	if *report != "" {
		for _, r := range results {
			if r.Skipped == "" {
				fmt.Fprintf(out, "%s: %d incompatible, %d compatible changes since %s\n",
					r.Module.Path, r.count(true), r.count(false), r.Base)
			}
		}

		return writeReport(*report, *major, *head, results)
	}

	return gate(out, results, exceptions)
}

// gate applies the exceptions to the incompatible changes, prints one
// annotation per change (::warning when excepted, ::error otherwise) and
// fails on any remaining incompatible change and on expired or unused
// exceptions.
func gate(out io.Writer, results []result, exceptions []exception) error {
	var (
		used []int
		errs []error
	)

	for i := range results {
		r := &results[i]
		if r.Skipped != "" {
			continue
		}

		u, err := applyExceptions(r, exceptions)
		if err != nil {
			errs = append(errs, err)
		}

		used = append(used, u...)

		// The gate passes -incompatible to apidiff, so compatible changes
		// are not listed.
		fmt.Fprintf(out, "%s: %d incompatible, unchecked compatible changes since %s\n",
			r.Module.Path, r.count(true), r.Base)

		for _, c := range r.Changes {
			if !c.Incompatible {
				continue
			}

			level, note := "error", ""
			if c.Excepted {
				level, note = "warning", " (allowed by the apicheck exception file)"
			}

			fmt.Fprintf(out, "::%s title=apidiff::%s: %s%s\n",
				level, importPath(r.Module.Path, c.Package)+"."+c.Subject, c.Message, note)
		}
	}

	if err := unusedExceptions(exceptions, used); err != nil {
		errs = append(errs, err)
	}

	for _, r := range results {
		if r.count(true) > 0 {
			errs = append(errs, errIncompatible)

			break
		}
	}

	return errors.Join(errs...)
}

func writeReport(file, major, head string, results []result) error {
	var buf bytes.Buffer

	title := "API diff: latest " + major + " tags to " + head
	if len(results) > 0 && results[0].Base != "" {
		title = "API diff: " + results[0].Base + " to " + head
	}

	command := "go run ./internal/tools/apicheck -major " + major + " -head " + head + " -report " + filepath.ToSlash(
		file,
	)
	if err := renderMarkdown(&buf, title, command, results); err != nil {
		return err
	}

	return os.WriteFile(file, buf.Bytes(), 0o600)
}

// compare extracts m at tag into work, writes the export data of both sides
// and returns the apidiff changes (only incompatible ones when gating).
func compare(
	ctx context.Context,
	root, work string,
	m module,
	tag, apidiff string,
	onlyIncompatible bool,
) ([]change, error) {
	if err := os.MkdirAll(work, 0o750); err != nil {
		return nil, err
	}

	tarball := filepath.Join(work, "old.tar")

	archive := []string{"archive", "--format=tar", "-o", tarball, tag}
	if m.Dir != "." {
		archive = append(archive, "--", m.Dir)
	}

	if _, err := gitOutput(ctx, root, archive...); err != nil {
		return nil, err
	}

	if err := extractFile(tarball, filepath.Join(work, "old")); err != nil {
		return nil, fmt.Errorf("extract %s: %w", tag, err)
	}

	oldExport := filepath.Join(work, "old.export")
	newExport := filepath.Join(work, "new.export")

	// -mod=mod lets the extracted copy record missing go.sum lines; the
	// working tree is type-checked as is.
	oldDir := filepath.Join(work, "old", filepath.FromSlash(m.Dir))
	if _, err := runTool(
		ctx,
		oldDir,
		[]string{"GOFLAGS=-mod=mod"},
		apidiff,
		"-m",
		"-w",
		oldExport,
		m.Path,
	); err != nil {
		return nil, fmt.Errorf("export %s: %w", tag, err)
	}

	newDir := filepath.Join(root, filepath.FromSlash(m.Dir))
	if _, err := runTool(ctx, newDir, nil, apidiff, "-m", "-w", newExport, m.Path); err != nil {
		return nil, fmt.Errorf("export working tree: %w", err)
	}

	args := []string{"-m"}
	if onlyIncompatible {
		args = append(args, "-incompatible")
	}

	report, err := runTool(ctx, work, nil, apidiff, append(args, oldExport, newExport)...)
	if err != nil {
		return nil, err
	}

	return parseReport(report), nil
}

// runTool runs name with GOWORK=off in dir and returns its stdout.
func runTool(ctx context.Context, dir string, env []string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), "GOWORK=off"), env...)

	var stdout, stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}

	return stdout.String(), nil
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir

	var stderr bytes.Buffer

	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}

	return strings.TrimSpace(string(out)), nil
}

// lookTool resolves a bare tool name through PATH and then the directory go
// install writes to, so a freshly installed apidiff works without changing
// PATH (Windows included).
func lookTool(ctx context.Context, name string) (string, error) {
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}

	if strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("%s not found", name)
	}

	env, err := runTool(ctx, "", nil, "go", "env", "GOBIN", "GOPATH")
	if err != nil {
		return "", err
	}

	gobin, gopath, _ := strings.Cut(strings.TrimSpace(strings.ReplaceAll(env, "\r", "")), "\n")
	if gobin == "" {
		gopath, _, _ = strings.Cut(gopath, string(os.PathListSeparator))
		gobin = filepath.Join(gopath, "bin")
	}

	p, err := exec.LookPath(filepath.Join(gobin, name))
	if err != nil {
		return "", fmt.Errorf(
			"%s not found in PATH or %s (go install golang.org/x/exp/cmd/apidiff@<version>)",
			name,
			gobin,
		)
	}

	return p, nil
}
