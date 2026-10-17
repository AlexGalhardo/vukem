// Command fixinline checks that go fix inlines every //go:fix inline wrapper
// of the repository correctly:
//
//	go run ./internal/tools/fixinline [-v]
//
// An inliner argument swap was reported when a parameter name matches a
// caller's argument name: i18n.NewMiddleware(b, MiddlewareConfig{}) became
// Middleware(MiddlewareConfig{}, cfg) while the wrapper's parameters were
// named b and cfg (v0.26.0, decision B2; not reproduced on Go 1.27.2). For each
// wrapper the command adds a generated file to the wrapper's package through
// a go build overlay (the working tree is never written), with callers whose
// argument variables reuse the parameter names: in order, rotated by one, and
// with one argument replaced by an expression while the same-named variable
// is in scope. It runs go fix -diff on those packages and fails unless every
// generated call becomes exactly the wrapper's body with the arguments
// substituted.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
)

// generatedName is the file the overlay adds to each wrapper's package.
const generatedName = "zz_fixinline_check.go"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	err := run(ctx, os.Args[1:], os.Stdout)

	stop()

	if err != nil {
		fmt.Fprintln(os.Stderr, "fixinline:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("fixinline", flag.ContinueOnError)
	verbose := fs.Bool("v", false, "print every checked call")
	rootFlag := fs.String("root", "", "repository root (default: git rev-parse --show-toplevel)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NArg() != 0 {
		return errors.New("usage: fixinline [-v] [-root dir]")
	}

	root := *rootFlag
	if root == "" {
		top, err := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel").Output()
		if err != nil {
			return fmt.Errorf("git rev-parse --show-toplevel: %w", err)
		}

		root = filepath.FromSlash(strings.TrimSpace(string(top)))
	}

	return check(ctx, root, out, *verbose)
}

// check runs the go fix check on every wrapper under root.
func check(ctx context.Context, root string, out io.Writer, verbose bool) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}

	wrappers, err := findWrappers(root)
	if err != nil {
		return err
	}

	if len(wrappers) == 0 {
		fmt.Fprintln(out, "fixinline: no //go:fix inline wrappers")

		return nil
	}

	tmp, err := os.MkdirTemp("", "fixinline-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	overlay, gen, err := writeGenerated(root, tmp, wrappers)
	if err != nil {
		return err
	}

	diff, err := goFixDiff(ctx, root, overlay, gen)
	if err != nil {
		return err
	}

	var (
		errs  []error
		total int
	)

	for _, g := range gen {
		removed, added := changedLines(diff, g.Source)

		notes, cerrs := verify(g.Cases, removed, added)
		for _, err := range cerrs {
			errs = append(errs, fmt.Errorf("%s: %w", g.Dir, err))
		}

		for _, n := range notes {
			fmt.Fprintf(out, "note: %s: %s\n", g.Dir, n)
		}

		total += len(g.Cases)

		if verbose {
			for _, c := range g.Cases {
				fmt.Fprintf(out, "%s %s: %s -> %s\n", c.Wrapper, c.Kind, c.Call, c.Want)
			}
		}
	}

	if err := errors.Join(errs...); err != nil {
		return err
	}

	fmt.Fprintf(out, "fixinline: %d wrappers, %d generated calls, none mis-inlined\n", len(wrappers), total)

	return nil
}

// generated is the overlay file of one package.
type generated struct {
	// Dir is the package directory; Source the temporary file the overlay
	// maps to Dir/zz_fixinline_check.go (go fix -diff prints its name).
	Dir, Source string
	Cases       []caseCall
}

func writeGenerated(root, tmp string, wrappers []wrapper) (string, []generated, error) {
	var (
		dirs []string
		gen  []generated
	)

	for i := range wrappers {
		if !slices.Contains(dirs, wrappers[i].Dir) {
			dirs = append(dirs, wrappers[i].Dir)
		}
	}

	replace := map[string]string{}

	for i, dir := range dirs {
		target := filepath.Join(dir, generatedName)
		if _, err := os.Stat(target); err == nil {
			return "", nil, fmt.Errorf("%s exists; the overlay would hide it", target)
		}

		var (
			body    strings.Builder
			imports []string
			cases   []caseCall
			pkg     string
		)

		for i := range wrappers {
			w := &wrappers[i]
			if w.Dir != dir {
				continue
			}

			src, cs, err := genCases(*w)
			if err != nil {
				return "", nil, fmt.Errorf("%s: %w", w, err)
			}

			pkg = w.Package
			body.WriteString(src)

			cases = append(cases, cs...)

			for _, imp := range w.Imports {
				if !slices.Contains(imports, imp) {
					imports = append(imports, imp)
				}
			}
		}

		var file strings.Builder

		// No "Code generated" header: go fix skips generated files.
		// go caches go fix results by file content, and a cached diff names
		// the temporary file of the run that produced it: the nonce makes
		// every run's content new.
		fmt.Fprintf(&file, "// Callers written by internal/tools/fixinline (run %s).\n\npackage %s\n",
			rand.Text(), pkg)

		if len(imports) > 0 {
			slices.Sort(imports)
			fmt.Fprintf(&file, "\nimport (\n\t%s\n)\n", strings.Join(imports, "\n\t"))
		}

		file.WriteString(body.String())

		source := filepath.Join(tmp, fmt.Sprintf("pkg%d.go", i))
		if err := os.WriteFile(source, []byte(file.String()), 0o600); err != nil {
			return "", nil, err
		}

		replace[target] = source

		rel, err := filepath.Rel(root, dir)
		if err != nil {
			return "", nil, err
		}

		gen = append(gen, generated{Dir: filepath.ToSlash(rel), Source: source, Cases: cases})
	}

	b, err := json.Marshal(map[string]any{"Replace": replace})
	if err != nil {
		return "", nil, err
	}

	overlay := filepath.Join(tmp, "overlay.json")

	return overlay, gen, os.WriteFile(overlay, b, 0o600)
}

// goFixDiff runs go fix -diff on the generated packages. A repository
// without go.work (the test fixture) runs with GOWORK=off.
func goFixDiff(ctx context.Context, root, overlay string, gen []generated) (string, error) {
	args := []string{"fix", "-diff", "-overlay", overlay}
	for _, g := range gen {
		args = append(args, "./"+g.Dir)
	}

	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOFLAGS=")

	if _, err := os.Stat(filepath.Join(root, "go.work")); err != nil {
		cmd.Env = append(cmd.Env, "GOWORK=off")
	}

	var stdout, stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// go fix -diff exits 1 when the diff is not empty, which is the
	// expected case; a build failure leaves stderr set instead.
	err := cmd.Run()
	if err != nil && strings.TrimSpace(stderr.String()) != "" {
		return "", fmt.Errorf("go %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}

	if err != nil && stdout.Len() == 0 {
		return "", fmt.Errorf("go %s: %w", strings.Join(args, " "), err)
	}

	return stdout.String(), nil
}

// changedLines returns the removed and added lines of file in a unified
// diff as printed by go fix -diff ("--- name (old)" headers), trimmed.
func changedLines(diff, file string) ([]string, []string) {
	var (
		removed, added []string
		inFile         bool
	)

	for line := range strings.Lines(diff) {
		line = strings.TrimRight(line, "\r\n")

		switch {
		case strings.HasPrefix(line, "--- "):
			name := strings.TrimSuffix(strings.TrimPrefix(line, "--- "), " (old)")
			inFile = strings.EqualFold(filepath.Clean(name), filepath.Clean(file))
		case strings.HasPrefix(line, "+++ "), strings.HasPrefix(line, "@@"):
		case !inFile:
		case strings.HasPrefix(line, "-"):
			removed = append(removed, strings.TrimSpace(line[1:]))
		case strings.HasPrefix(line, "+"):
			added = append(added, strings.TrimSpace(line[1:]))
		}
	}

	return removed, added
}

// verify pairs the generated calls, in order, with the lines go fix removed
// and added (whitespace ignored: go fix reformats). A call rewritten into
// anything but its expected line is an error, and so is a "same" call left
// alone: that is the shape most callers have. go fix may leave the rotated
// and literal calls alone when a name collision makes inlining unsafe; that
// is reported as a note, since the call keeps compiling and working.
func verify(cases []caseCall, removed, added []string) ([]string, []error) {
	var (
		notes []string
		errs  []error
	)

	norm := func(s string) string { return strings.Join(strings.Fields(s), "") }

	if len(removed) != len(added) {
		return nil, []error{fmt.Errorf(
			"go fix did not replace each call by one line (rename the wrapper's parameters): -%q +%q",
			removed, added,
		)}
	}

	j := 0

	for _, c := range cases {
		if j >= len(removed) || norm(removed[j]) != norm(c.Call) {
			if c.Kind == "same" {
				errs = append(errs, fmt.Errorf("%s %s: go fix did not inline %q", c.Wrapper, c.Kind, c.Call))
			} else {
				notes = append(notes, fmt.Sprintf("%s %s: go fix leaves %q as is", c.Wrapper, c.Kind, c.Call))
			}

			continue
		}

		if norm(added[j]) != norm(c.Want) {
			errs = append(errs, fmt.Errorf(
				"%s %s: go fix rewrote %q into %q, want %q; rename the wrapper's parameters",
				c.Wrapper, c.Kind, c.Call, added[j], c.Want,
			))
		}

		j++
	}

	if j < len(removed) {
		errs = append(errs, fmt.Errorf("go fix changed lines that are not generated calls: -%q +%q",
			removed[j:], added[j:]))
	}

	return notes, errs
}
