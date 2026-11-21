// Command paritycheck checks PARITY.md, the ledger that maps every capability
// of the old server-rendered pages to the tests that cover it now, so that deleting the
// old pages and their tests lost no coverage.
//
// Run it from contrib/studio:
//
//	go run ./scripts/paritycheck PARITY.md
//
// Each table row has three cells (area, capability, covered by). The last one
// lists references separated by semicolons, each in backticks:
//
//	go:TestName            a Go test of this package (it is run)
//	gocmd:TestName         a Go test under ./cmd/... (it is run)
//	goint:TestName         a Go test behind the integration build tag (checked to exist only)
//	e2e:file.spec.ts#title a Playwright test under web/e2e (checked to exist; -e2e runs the files)
//	bun:path#title         a bun test under web/ (checked to exist; -bun runs them)
//
// It exits 0 when every row names at least one reference, every reference
// exists, and every Go test it runs passes.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

type ref struct{ kind, name, title string }

type row struct {
	line       int
	capability string
	refs       []ref
}

var refRe = regexp.MustCompile("`([a-z0-9]+):([^`]+)`")

func main() {
	run := flag.Bool("run", true, "run the Go tests the ledger names")
	e2e := flag.Bool("e2e", false, "also run the Playwright files the ledger names (needs the services and a built UI)")
	bun := flag.Bool("bun", false, "also run the bun test files the ledger names")
	flag.Parse()

	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: paritycheck [-run=false] [-e2e] [-bun] PARITY.md")
		os.Exit(2)
	}

	rows, err := parse(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "paritycheck:", err)
		os.Exit(1)
	}

	problems := check(rows)

	if len(problems) == 0 && *run {
		problems = append(problems, runGo(rows)...)
	}

	if len(problems) == 0 && *bun {
		problems = append(
			problems,
			runFiles(rows, "bun", "web", []string{"bun", "test"}, func(r ref) string { return r.name })...)
	}

	if len(problems) == 0 && *e2e {
		problems = append(problems, runFiles(rows, "e2e", "web", []string{"bunx", "playwright", "test"},
			func(r ref) string { return filepath.ToSlash(filepath.Join("e2e", r.name)) })...)
	}

	for _, p := range problems {
		fmt.Fprintln(os.Stderr, "paritycheck:", p)
	}

	if len(problems) > 0 {
		os.Exit(1)
	}

	refs := 0
	for _, r := range rows {
		refs += len(r.refs)
	}

	fmt.Printf("paritycheck: %d capabilities, %d references, all present", len(rows), refs)

	if *run {
		fmt.Print(", Go tests passed")
	}

	fmt.Println()
}

func parse(path string) ([]row, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var rows []row

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)

	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "|") || strings.HasPrefix(line, "| ---") || strings.HasPrefix(line, "| Area") {
			continue
		}

		cells := strings.Split(strings.Trim(line, "|"), "|")
		if len(cells) != 3 {
			return nil, fmt.Errorf("line %d: %d cells, want 3 (area, capability, covered by)", n, len(cells))
		}

		r := row{line: n, capability: strings.TrimSpace(cells[1])}

		for _, m := range refRe.FindAllStringSubmatch(cells[2], -1) {
			x := ref{kind: m[1], name: m[2]}
			if file, title, ok := strings.Cut(m[2], "#"); ok {
				x.name, x.title = file, title
			}

			r.refs = append(r.refs, x)
		}

		rows = append(rows, r)
	}

	return rows, sc.Err()
}

func check(rows []row) []string {
	var problems []string

	goTests := collectGoTests()

	for _, r := range rows {
		if len(r.refs) == 0 {
			problems = append(problems, fmt.Sprintf("line %d (%s): no test is named", r.line, r.capability))
		}

		for _, x := range r.refs {
			if msg := checkRef(x, goTests); msg != "" {
				problems = append(problems, fmt.Sprintf("line %d (%s): %s", r.line, r.capability, msg))
			}
		}
	}

	return problems
}

// collectGoTests maps every Go test function to the directory of its package.
// Only the root package and the commands under cmd/ hold tests the ledger names.
func collectGoTests() map[string]string {
	out := map[string]string{}
	fn := regexp.MustCompile(`(?m)^func (Test\w+)\(`)

	for _, pattern := range []string{"*_test.go", filepath.Join("cmd", "*", "*_test.go")} {
		files, _ := filepath.Glob(pattern)

		for _, path := range files {
			src, err := os.ReadFile(path)
			if err != nil {
				continue
			}

			for _, m := range fn.FindAllStringSubmatch(string(src), -1) {
				out[m[1]] = filepath.Dir(path)
			}
		}
	}

	return out
}

func checkRef(x ref, goTests map[string]string) string {
	switch x.kind {
	case "go", "gocmd", "goint":
		dir, ok := goTests[x.name]

		switch {
		case !ok:
			return fmt.Sprintf("Go test %s does not exist", x.name)
		case x.kind == "go" && dir != ".":
			return fmt.Sprintf("Go test %s is in %s, not in the root package (use gocmd:)", x.name, dir)
		case x.kind == "gocmd" && !strings.HasPrefix(filepath.ToSlash(dir), "cmd/"):
			return fmt.Sprintf("Go test %s is in %s, not under cmd/", x.name, dir)
		}

		return ""
	case "e2e", "bun":
		base := "web"
		if x.kind == "e2e" {
			base = filepath.Join("web", "e2e")
		}

		src, err := os.ReadFile(filepath.Join(base, x.name))
		if err != nil {
			return fmt.Sprintf("%s file %s does not exist", x.kind, x.name)
		}

		if x.title == "" {
			return fmt.Sprintf("%s reference %s has no #title", x.kind, x.name)
		}

		for line := range strings.SplitSeq(string(src), "\n") {
			t := strings.TrimSpace(line)
			if (strings.HasPrefix(t, "test(") || strings.HasPrefix(t, "test.skip(")) && strings.Contains(t, x.title) {
				return ""
			}
		}

		return fmt.Sprintf("%s test %q does not exist in %s", x.kind, x.title, x.name)
	default:
		return fmt.Sprintf("unknown reference kind %q", x.kind)
	}
}

// runGo runs the named Go tests once per package and requires each to pass.
func runGo(rows []row) []string {
	goTests := collectGoTests()
	byPkg := map[string][]string{}

	for _, r := range rows {
		for _, x := range r.refs {
			if (x.kind == "go" || x.kind == "gocmd") && !slices.Contains(byPkg[goTests[x.name]], x.name) {
				byPkg[goTests[x.name]] = append(byPkg[goTests[x.name]], x.name)
			}
		}
	}

	var problems []string

	for _, dir := range slices.Sorted(maps.Keys(byPkg)) {
		names := byPkg[dir]
		pattern := "^(" + strings.Join(names, "|") + ")$"

		cmd := exec.CommandContext(
			context.Background(),
			"go",
			"test",
			"-count=1",
			"-v",
			"-run",
			pattern,
			"./"+filepath.ToSlash(dir),
		)
		cmd.Env = append(os.Environ(), "GOWORK=off")

		out, err := cmd.CombinedOutput()
		if err != nil {
			problems = append(problems, fmt.Sprintf("go test in %s failed:\n%s", dir, tail(string(out), 40)))
			continue
		}

		for _, n := range names {
			if !strings.Contains(string(out), "--- PASS: "+n+" ") {
				problems = append(problems, fmt.Sprintf("Go test %s did not run or did not pass in %s", n, dir))
			}
		}
	}

	return problems
}

func runFiles(rows []row, kind, dir string, command []string, file func(ref) string) []string {
	seen := map[string]bool{}

	var files []string

	for _, r := range rows {
		for _, x := range r.refs {
			if f := file(x); x.kind == kind && !seen[f] {
				seen[f] = true
				files = append(files, f)
			}
		}
	}

	slices.Sort(files)

	if len(files) == 0 {
		return nil
	}

	//nolint:gosec // the command and its arguments are fixed by this program and the ledger of this repository
	cmd := exec.CommandContext(context.Background(), command[0], append(command[1:], files...)...)
	cmd.Dir = dir

	if out, err := cmd.CombinedOutput(); err != nil {
		return []string{fmt.Sprintf("%s failed:\n%s", strings.Join(command, " "), tail(string(out), 60))}
	}

	return nil
}

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}

	return strings.Join(lines, "\n")
}
