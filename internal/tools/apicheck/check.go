package main

import (
	"archive/tar"
	"bufio"
	"cmp"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// module is a published Go module of the repository.
type module struct {
	// Dir is the slash-separated module directory, "." for the core.
	Dir string
	// Path is the module path from go.mod.
	Path string
}

// tagPrefix is the release tag prefix: "" for the core, "drivers/x/" for a
// nested module (tags drivers/x/vX.Y.Z).
func (m module) tagPrefix() string {
	if m.Dir == "." {
		return ""
	}

	return m.Dir + "/"
}

// moduleParents hold the published nested modules; internal/bench and the
// examples are not published, so they make no compatibility promise.
var moduleParents = []string{"drivers", "contrib"} //nolint:gochecknoglobals // read-only list

// discoverModules returns the core module followed by every drivers/* and
// contrib/* module, sorted by directory.
func discoverModules(root string) ([]module, error) {
	core, err := readModule(root, ".")
	if err != nil {
		return nil, err
	}

	mods := []module{core}

	for _, parent := range moduleParents {
		entries, err := os.ReadDir(filepath.Join(root, parent))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}

		if err != nil {
			return nil, err
		}

		for _, e := range entries {
			if !e.IsDir() {
				continue
			}

			dir := parent + "/" + e.Name()
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(dir), "go.mod")); err != nil {
				continue
			}

			m, err := readModule(root, dir)
			if err != nil {
				return nil, err
			}

			mods = append(mods, m)
		}
	}

	slices.SortFunc(mods[1:], func(a, b module) int { return cmp.Compare(a.Dir, b.Dir) })

	return mods, nil
}

func readModule(root, dir string) (module, error) {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(dir), "go.mod"))
	if err != nil {
		return module{}, err
	}

	p := modulePath(b)
	if p == "" {
		return module{}, fmt.Errorf("%s/go.mod: no module directive", dir)
	}

	return module{Dir: dir, Path: p}, nil
}

// modulePath returns the module directive of a go.mod file.
func modulePath(gomod []byte) string {
	sc := bufio.NewScanner(strings.NewReader(string(gomod)))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if rest, ok := strings.CutPrefix(line, "module "); ok {
			return strings.Trim(strings.TrimSpace(rest), `"`)
		}
	}

	return ""
}

type semver struct{ major, minor, patch int }

// parseStable parses vMAJOR.MINOR.PATCH; pre-releases and build metadata
// are not stable and report false.
func parseStable(s string) (semver, bool) {
	rest, ok := strings.CutPrefix(s, "v")
	if !ok {
		return semver{}, false
	}

	parts := strings.Split(rest, ".")
	if len(parts) != 3 {
		return semver{}, false
	}

	var n [3]int

	for i, p := range parts {
		if p == "" || (len(p) > 1 && p[0] == '0') {
			return semver{}, false
		}

		v, err := strconv.Atoi(p)
		if err != nil || v < 0 {
			return semver{}, false
		}

		n[i] = v
	}

	return semver{n[0], n[1], n[2]}, true
}

func (v semver) compare(o semver) int {
	return cmp.Or(cmp.Compare(v.major, o.major), cmp.Compare(v.minor, o.minor), cmp.Compare(v.patch, o.patch))
}

// latestStable returns the highest stable tag prefix+vMAJOR.x.y among tags,
// where major is "v1", "v0"...
func latestStable(tags []string, prefix, major string) (string, bool) {
	want, err := strconv.Atoi(strings.TrimPrefix(major, "v"))
	if err != nil || !strings.HasPrefix(major, "v") {
		return "", false
	}

	var (
		best    string
		bestVer semver
	)

	for _, tag := range tags {
		rest, ok := strings.CutPrefix(strings.TrimSpace(tag), prefix)
		if !ok || strings.Contains(rest, "/") {
			continue
		}

		v, ok := parseStable(rest)
		if !ok || v.major != want {
			continue
		}

		if best == "" || v.compare(bestVer) > 0 {
			best, bestVer = prefix+rest, v
		}
	}

	return best, best != ""
}

// change is one line of an apidiff report.
type change struct {
	// Package is the package path relative to the module ("." or "./x/y").
	Package string
	// Subject is the identifier within the package (Name, (*T).M...).
	Subject      string
	Message      string
	Incompatible bool
	// Excepted marks an incompatible change allowed by an exception entry.
	Excepted bool
}

// parseReport parses `apidiff -m` output: "Incompatible changes:" and
// "Compatible changes:" headers followed by "- ./pkg.Name: message" lines.
// Without headers (apidiff -incompatible) every change is incompatible.
func parseReport(out string) []change {
	var changes []change

	incompatible := true

	for line := range strings.Lines(out) {
		line = strings.TrimRight(line, "\r\n")

		switch {
		case line == "Incompatible changes:":
			incompatible = true
		case line == "Compatible changes:":
			incompatible = false
		case strings.HasPrefix(line, "- "):
			subject, msg, _ := strings.Cut(strings.TrimPrefix(line, "- "), ": ")
			pkg, name := splitSubject(subject)
			changes = append(changes, change{Package: pkg, Subject: name, Message: msg, Incompatible: incompatible})
		}
	}

	return changes
}

// splitSubject splits "./auth/hash.Name" into "./auth/hash" and "Name"; a
// subject of the module root package ("./.Name" or ".Name") yields ".".
func splitSubject(s string) (string, string) {
	slash := strings.LastIndex(s, "/")

	dot := strings.Index(s[slash+1:], ".")
	if dot < 0 {
		return ".", s
	}

	pkg := s[:slash+1+dot]
	name := s[slash+1+dot+1:]

	if pkg == "" || pkg == "." || pkg == "./" {
		pkg = "."
	}

	return pkg, name
}

// importPath joins the module path and a report package path.
func importPath(modPath, pkg string) string {
	if pkg == "." {
		return modPath
	}

	return modPath + "/" + strings.TrimPrefix(pkg, "./")
}

// result is the comparison of one module.
type result struct {
	Module module
	// Base is the tag compared against; empty when Skipped.
	Base    string
	Skipped string
	Changes []change
}

// count returns the compatible or incompatible changes; excepted
// incompatible changes are not counted.
func (r result) count(incompatible bool) int {
	n := 0

	for _, c := range r.Changes {
		if c.Incompatible == incompatible && !c.Excepted {
			n++
		}
	}

	return n
}

// renderMarkdown writes results as a report grouped by module and package.
//
//nolint:gocognit // report rendering; one section per change kind
func renderMarkdown(w io.Writer, title, command string, results []result) error {
	var b strings.Builder

	fmt.Fprintf(&b, "# %s\n\n", title)
	fmt.Fprintf(&b, "Generated with `%s`; each change is reviewed in [review.md](review.md).\n\n", command)
	b.WriteString("| Module | Base tag | Incompatible | Compatible |\n| --- | --- | --- | --- |\n")

	for _, r := range results {
		if r.Skipped != "" {
			fmt.Fprintf(&b, "| `%s` | none (%s) | - | - |\n", r.Module.Path, r.Skipped)

			continue
		}

		fmt.Fprintf(&b, "| `%s` | `%s` | %d | %d |\n", r.Module.Path, r.Base, r.count(true), r.count(false))
	}

	for _, r := range results {
		if r.Skipped != "" {
			continue
		}

		fmt.Fprintf(&b, "\n## %s\n\nCompared with `%s`.\n", r.Module.Path, r.Base)

		if len(r.Changes) == 0 {
			b.WriteString("\nNo API changes.\n")

			continue
		}

		var pkgs []string
		for _, c := range r.Changes {
			if !slices.Contains(pkgs, c.Package) {
				pkgs = append(pkgs, c.Package)
			}
		}

		slices.Sort(pkgs)

		for _, pkg := range pkgs {
			fmt.Fprintf(&b, "\n### %s\n", importPath(r.Module.Path, pkg))

			for _, incompatible := range []bool{true, false} {
				label := "Compatible"
				if incompatible {
					label = "Incompatible"
				}

				var lines []string

				for _, c := range r.Changes {
					if c.Package == pkg && c.Incompatible == incompatible {
						lines = append(lines, fmt.Sprintf("- %#q: %s", c.Subject, c.Message))
					}
				}

				if len(lines) > 0 {
					fmt.Fprintf(&b, "\n%s:\n\n%s\n", label, strings.Join(lines, "\n"))
				}
			}
		}
	}

	_, err := io.WriteString(w, b.String())

	return err
}

// extractFile extracts the tar file at name into dst, creating dst.
func extractFile(name, dst string) error {
	if err := os.MkdirAll(dst, 0o750); err != nil {
		return err
	}

	f, err := os.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()

	return extractTar(f, dst)
}

// extractTar writes the regular files of a tar stream (git archive) under
// dst, rejecting entries that would escape it.
func extractTar(r io.Reader, dst string) error {
	root, err := os.OpenRoot(dst)
	if err != nil {
		return err
	}
	defer root.Close()

	tr := tar.NewReader(r)

	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}

		if err != nil {
			return err
		}

		name := path.Clean(hdr.Name)
		if !filepath.IsLocal(filepath.FromSlash(name)) {
			return fmt.Errorf("archive entry %q escapes the destination", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(name, 0o750); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := writeEntry(root, name, tr); err != nil {
				return err
			}
		}
	}
}

func writeEntry(root *os.Root, name string, r io.Reader) error {
	if dir := path.Dir(name); dir != "." {
		if err := root.MkdirAll(dir, 0o750); err != nil {
			return err
		}
	}

	f, err := root.Create(name)
	if err != nil {
		return err
	}

	if _, err := io.Copy(f, r); err != nil {
		_ = f.Close()

		return err
	}

	return f.Close()
}
