// Command snippetcheck keeps the Go examples of the documentation site in
// step with the framework API.
//
// It extracts every ```go block that has a title="*.go" attribute from
// site/content/docs/**/*.mdx and type-checks it against the checkout. Blocks
// marked "nocompile" in the fence info string are skipped.
//
// The examples are fragments (a route, a handler body, a constructor call),
// not whole programs, so they cannot be compiled as written: variables they
// use are declared elsewhere on the page. snippetcheck therefore type-checks
// each block, wrapped in a function when it is not a file, and reports only
// the errors that mean the example disagrees with the API: an unknown
// package member (web.NoSuchThing), an unknown field or method (c.NoSuchThing)
// and a wrong argument count. Undefined local variables are ignored.
//
// Run from the repository root:
//
//	go run ./internal/tools/snippetcheck
package main

import (
	"bufio"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	docsDir    = "site/content/docs"
	modulePath = "github.com/AlexGalhardo/vukem"
)

var (
	fenceRe = regexp.MustCompile("^```go\\b(.*)$")
	titleRe = regexp.MustCompile(`title="[^"]*\.go"`)
	usedRe  = regexp.MustCompile(`\b([a-z][a-z0-9]*)\.[A-Z]`)
	// Messages that mean the snippet disagrees with the API. Everything
	// else is noise from the missing surrounding page.
	relevantRe = regexp.MustCompile(
		`has no field or method|^undefined: [a-z][a-z0-9]*\.[A-Z]|not enough arguments in call|too many arguments in call`,
	)
)

// stdImports are the standard packages snippets use without importing.
var stdImports = map[string]string{
	"http":    "net/http",
	"context": "context",
	"errors":  "errors",
	"fmt":     "fmt",
	"json":    "encoding/json",
	"slog":    "log/slog",
	"strings": "strings",
	"time":    "time",
	"os":      "os",
}

// block is one extracted snippet.
type block struct {
	file string
	line int
	code string
}

func main() {
	if err := run(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "snippetcheck:", err)
		os.Exit(1)
	}
}

var errMismatch = errors.New("documentation snippets disagree with the API; fix them or mark the block nocompile")

func run(out io.Writer) error {
	var blocks []block

	err := filepath.WalkDir(docsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".mdx") {
			return err
		}

		f, err := os.Open(path) //nolint:gosec // G304: walked from the docs tree
		if err != nil {
			return err
		}
		defer f.Close()

		b, err := extract(path, f)
		blocks = append(blocks, b...)

		return err
	})
	if err != nil {
		return err
	}

	c, err := newChecker()
	if err != nil {
		return err
	}

	failed := 0

	for _, b := range blocks {
		for _, msg := range c.check(b.code) {
			failed++

			fmt.Fprintf(out, "%s:%d: %s\n", b.file, b.line, msg)
		}
	}

	fmt.Fprintf(out, "snippetcheck: %d blocks, %d problems\n", len(blocks), failed)

	if failed > 0 {
		return errMismatch
	}

	return nil
}

// extract returns the checked blocks of one MDX file; line is the line of the
// first code line.
func extract(name string, r io.Reader) ([]block, error) {
	var (
		blocks []block
		cur    []string
		start  int
		in     bool
		skip   bool
	)

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)

	for n := 1; sc.Scan(); n++ {
		line := sc.Text()

		switch {
		case !in:
			if m := fenceRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
				in = true
				skip = !titleRe.MatchString(m[1]) || strings.Contains(m[1], "nocompile")
				cur, start = nil, n+1
			}
		case strings.HasPrefix(strings.TrimSpace(line), "```"):
			in = false

			if !skip {
				blocks = append(blocks, block{file: name, line: start, code: strings.Join(cur, "\n")})
			}
		default:
			cur = append(cur, line)
		}
	}

	return blocks, sc.Err()
}

type checker struct {
	fset *token.FileSet
	imp  types.Importer
	pkgs map[string]bool
}

func newChecker() (*checker, error) {
	fset := token.NewFileSet()

	imp, err := exportImporter(fset)
	if err != nil {
		return nil, err
	}

	return &checker{fset: fset, imp: imp, pkgs: localPackages()}, nil
}

// localPackages lists the root package directories of the module.
func localPackages() map[string]bool {
	set := map[string]bool{}

	entries, _ := os.ReadDir(".")
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		if m, _ := filepath.Glob(filepath.Join(e.Name(), "*.go")); len(m) > 0 {
			set[e.Name()] = true
		}
	}

	return set
}

// check returns the API disagreements of one snippet, or a parse problem.
func (c *checker) check(code string) []string {
	files := c.parse(code)
	if files == nil {
		return []string{"cannot parse the block; fix it or mark it nocompile"}
	}

	var msgs []string

	cfg := types.Config{
		Importer: c.imp,
		Error: func(err error) {
			var te types.Error
			if errors.As(err, &te) && relevantRe.MatchString(te.Msg) {
				msgs = append(msgs, te.Msg)
			}
		},
	}
	_, _ = cfg.Check("snippet", c.fset, files, nil)

	sort.Strings(msgs)

	return msgs
}

// parse tries the block as a file, as top-level declarations, then as
// statements, adding the imports the fragment relies on.
func (c *checker) parse(code string) []*ast.File {
	if strings.HasPrefix(strings.TrimSpace(code), "package ") {
		if f, err := parser.ParseFile(c.fset, "snippet.go", code, 0); err == nil {
			return []*ast.File{f}
		}

		return nil
	}

	header := "package snippet\n" + c.imports(code)

	for _, src := range []string{header + code, header + "func _() {\n" + code + "\n}"} {
		if f, err := parser.ParseFile(c.fset, "snippet.go", src, 0); err == nil {
			return []*ast.File{f}
		}
	}

	return nil
}

// imports renders an import block for the package names code refers to.
func (c *checker) imports(code string) string {
	if strings.Contains(code, "import ") {
		return ""
	}

	seen := map[string]bool{}

	var b strings.Builder

	for _, m := range usedRe.FindAllStringSubmatch(code, -1) {
		name := m[1]
		if seen[name] {
			continue
		}

		seen[name] = true

		switch path, ok := stdImports[name]; {
		case c.pkgs[name]:
			fmt.Fprintf(&b, "import %q\n", modulePath+"/"+name)
		case ok:
			fmt.Fprintf(&b, "import %q\n", path)
		}
	}

	return b.String()
}
