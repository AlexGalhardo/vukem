package main

import (
	"bufio"
	"cmp"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"strconv"
	"strings"
)

// module is the import path prefix of core packages.
const module = "github.com/AlexGalhardo/vukem"

// defaultGate applies to every core package not listed in strictGates
// (docs/testing.md).
const defaultGate = 80.0

// strictGates are the packages held to a higher bar (docs/testing.md);
// "validate" in the docs is package z. They must appear in the profile.
var strictGates = map[string]float64{
	"router":  90,
	"z":       90,
	"session": 90,
	"orm":     90,
	"schema":  90,
	"auth":    90,
	"config":  90,
}

// excluded reports whether pkg (relative to the module, "" for the root)
// is outside the gate: commands, internal tooling and the conformance
// helpers whose code runs from other packages' tests.
func excluded(pkg string) bool {
	if pkg == "cmd" || strings.HasPrefix(pkg, "cmd/") || pkg == "internal" || strings.HasPrefix(pkg, "internal/") {
		return true
	}

	switch path.Base(pkg) {
	case "cachetest", "queuetest", "storagetest", "dbtest":
		return true
	}

	return false
}

// Result is the coverage of one core package against its gate.
type Result struct {
	Package    string
	Statements int
	Covered    int
	Gate       float64
}

// Percent is the statement coverage, 0 for a package without statements.
func (r Result) Percent() float64 {
	if r.Statements == 0 {
		return 0
	}

	return 100 * float64(r.Covered) / float64(r.Statements)
}

// Pass reports whether the package meets its gate (unrounded, so 89.96%
// fails a 90% gate even though go test prints 90.0%).
func (r Result) Pass() bool { return r.Percent() >= r.Gate }

type block struct {
	stmts   int
	covered bool
}

// Parse reads a coverprofile and returns per-package results for core
// packages, sorted by package. Blocks listed more than once (several test
// binaries covering the same package) count once, covered if any run hit
// them.
func Parse(r io.Reader) ([]Result, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	blocks := map[string]map[string]block{} // package -> block position -> block
	line := 0

	for sc.Scan() {
		line++

		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "mode:") {
			continue
		}

		pos, stmts, count, err := parseLine(text)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}

		file, _, _ := strings.Cut(pos, ":")
		if file != module && !strings.HasPrefix(file, module+"/") {
			continue
		}

		pkg := strings.TrimPrefix(strings.TrimPrefix(path.Dir(file), module), "/")
		if excluded(pkg) {
			continue
		}

		if blocks[pkg] == nil {
			blocks[pkg] = map[string]block{}
		}

		b := blocks[pkg][pos]
		b.stmts = stmts
		b.covered = b.covered || count > 0
		blocks[pkg][pos] = b
	}

	if err := sc.Err(); err != nil {
		return nil, err
	}

	if line == 0 {
		return nil, errors.New("empty coverprofile")
	}

	out := make([]Result, 0, len(blocks))

	for pkg, bs := range blocks {
		res := Result{Package: pkg, Gate: gateFor(pkg)}

		for _, b := range bs {
			res.Statements += b.stmts
			if b.covered {
				res.Covered += b.stmts
			}
		}

		out = append(out, res)
	}

	slices.SortFunc(out, func(a, b Result) int { return cmp.Compare(a.Package, b.Package) })

	return out, nil
}

func gateFor(pkg string) float64 {
	if g, ok := strictGates[pkg]; ok {
		return g
	}

	return defaultGate
}

// parseLine splits "file:1.2,3.4 stmts count".
func parseLine(text string) (pos string, stmts, count int, err error) {
	fields := strings.Fields(text)
	if len(fields) != 3 || !strings.Contains(fields[0], ":") {
		return "", 0, 0, fmt.Errorf("malformed coverprofile line %q", text)
	}

	if stmts, err = strconv.Atoi(fields[1]); err != nil {
		return "", 0, 0, fmt.Errorf("statements in %q: %w", text, err)
	}

	if count, err = strconv.Atoi(fields[2]); err != nil {
		return "", 0, 0, fmt.Errorf("count in %q: %w", text, err)
	}

	return fields[0], stmts, count, nil
}

// Missing returns the strict-gate packages absent from results: a package
// whose tests did not run must not pass silently.
func Missing(results []Result) []string {
	seen := map[string]bool{}
	for _, r := range results {
		seen[r.Package] = true
	}

	var out []string

	for pkg := range strictGates {
		if !seen[pkg] {
			out = append(out, pkg)
		}
	}

	slices.Sort(out)

	return out
}
