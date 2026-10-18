// Package codemod edits Go source the way a developer would: it appends an
// element to the slice literal returned by a known function and adds the
// imports it needs. Registration in Vukem apps is plain code (no init(), no
// reflection), so generators use this to wire what they create.
package codemod

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"slices"
	"strconv"
	"strings"
)

// ErrMarkerNotFound means the file no longer has the expected shape
// (func Name() []T { return []T{ ... } }); callers print the snippet for the
// user to add by hand instead.
var ErrMarkerNotFound = errors.New("codemod: marker not found")

// edit replaces src[at:at+del] with text.
type edit struct {
	at, del int
	text    string
}

// AppendToReturn appends expr to the composite literal returned by funcName
// and adds the import paths. It is idempotent: an element whose source text
// equals expr is not added twice. local is the module path of the project
// being edited, so an added import lands in the group gci sorts it into; ""
// treats every path as belonging to another module.
func AppendToReturn(src []byte, funcName, expr, local string, imports ...string) ([]byte, error) {
	if _, err := parser.ParseExpr(expr); err != nil {
		return nil, fmt.Errorf("codemod: invalid expression %q: %w", expr, err)
	}

	fset := token.NewFileSet()

	file, err := parser.ParseFile(fset, "", src, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("codemod: parse: %w", err)
	}

	lit := returnedLiteral(file, funcName)
	if lit == nil {
		return nil, fmt.Errorf("%w: func %s() returning a slice literal", ErrMarkerNotFound, funcName)
	}

	off := func(p token.Pos) int { return fset.Position(p).Offset }

	var edits []edit

	exists := slices.ContainsFunc(lit.Elts, func(e ast.Expr) bool {
		return normalize(string(src[off(e.Pos()):off(e.End())])) == normalize(expr)
	})
	if !exists {
		edits = append(edits, appendEdits(src, lit, off, expr)...)
	}

	edits = append(edits, importEdits(src, file, off, imports, local)...)

	slices.SortFunc(edits, func(a, b edit) int { return b.at - a.at })

	out := slices.Clone(src)
	for _, e := range edits {
		out = slices.Replace(out, e.at, e.at+e.del, []byte(e.text)...)
	}

	formatted, err := format.Source(out)
	if err != nil {
		return nil, fmt.Errorf("codemod: format: %w", err)
	}

	return formatted, nil
}

// appendEdits insert expr as the last element, on its own line.
func appendEdits(src []byte, lit *ast.CompositeLit, off func(token.Pos) int, expr string) []edit {
	rbrace := off(lit.Rbrace)

	if n := len(lit.Elts); n > 0 && !strings.Contains(string(src[off(lit.Elts[n-1].End()):rbrace]), ",") {
		// "{a, b}" on one line: break it into one element per line.
		return []edit{{at: rbrace, text: ",\n" + expr + ",\n"}, {at: off(lit.Lbrace) + 1, text: "\n"}}
	}

	// When the closing brace has its own line, insert at the start of it.
	i := rbrace
	for i > 0 && (src[i-1] == ' ' || src[i-1] == '\t') {
		i--
	}

	if i > 0 && src[i-1] == '\n' {
		return []edit{{at: i, text: expr + ",\n"}}
	}

	return []edit{{at: rbrace, text: "\n" + expr + ",\n"}}
}

func returnedLiteral(file *ast.File, funcName string) *ast.CompositeLit {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || fn.Name.Name != funcName || fn.Body == nil {
			continue
		}

		for _, stmt := range fn.Body.List {
			ret, ok := stmt.(*ast.ReturnStmt)
			if !ok || len(ret.Results) != 1 {
				continue
			}

			if lit, ok := ret.Results[0].(*ast.CompositeLit); ok {
				if _, isSlice := lit.Type.(*ast.ArrayType); isSlice {
					return lit
				}
			}
		}
	}

	return nil
}

// importEdits adds the missing import paths, each in the group gci orders
// the file by: standard library, then other modules, then the project's own
// packages (local). gofmt sorts within a group but never moves an import
// between groups, so putting a path in the wrong one leaves the generated
// file reporting "File is not properly formatted".
func importEdits(src []byte, file *ast.File, off func(token.Pos) int, paths []string, local string) []edit {
	var missing []string

	for _, p := range paths {
		has := slices.ContainsFunc(file.Imports, func(s *ast.ImportSpec) bool {
			v, _ := strconv.Unquote(s.Path.Value)
			return v == p
		})
		if !has && !slices.Contains(missing, p) {
			missing = append(missing, p)
		}
	}

	if len(missing) == 0 {
		return nil
	}

	gd := importDecl(file)

	switch {
	case gd == nil:
		// No imports yet: add a block after the package clause.
		return []edit{{at: off(file.Name.End()), text: "\n\nimport (\n" + importBlock(nil, missing, local) + ")\n"}}
	case gd.Lparen.IsValid():
		return blockEdits(gd, off, missing, local)
	default:
		// import "x" becomes import ( "x" ... ).
		kept := importLine{
			group: importGroup(specPath(gd.Specs[0]), local),
			text:  string(src[off(gd.Specs[0].Pos()):off(gd.Specs[0].End())]),
		}

		return []edit{{
			at:   off(gd.Pos()),
			del:  off(gd.End()) - off(gd.Pos()),
			text: "import (\n" + importBlock([]importLine{kept}, missing, local) + ")",
		}}
	}
}

// blockEdits inserts the missing paths into an existing parenthesized
// block, after the last import of their own group or, when the block has
// none, as a new group in gci's order.
func blockEdits(gd *ast.GenDecl, off func(token.Pos) int, missing []string, local string) []edit {
	var edits []edit

	for g := range groups {
		var lines []string

		for _, p := range missing {
			if importGroup(p, local) == g {
				lines = append(lines, strconv.Quote(p))
			}
		}

		if len(lines) == 0 {
			continue
		}

		body := strings.Join(lines, "\n")

		// Every existing import belongs to a later group: the new one goes
		// first, and the newline before the block's first import becomes
		// the blank line separating the two groups.
		at, text := off(gd.Lparen)+1, "\n"+body+"\n"

		for _, s := range gd.Specs {
			if sg := importGroup(specPath(s), local); sg <= g {
				at, text = off(s.End()), "\n"+body
				if sg < g {
					text = "\n" + text
				}
			}
		}

		edits = append(edits, edit{at: at, text: text})
	}

	return edits
}

// groups is the number of gci groups: see importGroup.
const groups = 3

// importLine is one import spec of a block, with the group it belongs to.
type importLine struct {
	group int
	text  string
}

// importBlock renders the body of an import block, a blank line between
// groups; gofmt sorts within each group afterwards.
func importBlock(kept []importLine, missing []string, local string) string {
	lines := slices.Clone(kept)
	for _, p := range missing {
		lines = append(lines, importLine{group: importGroup(p, local), text: strconv.Quote(p)})
	}

	slices.SortStableFunc(lines, func(a, b importLine) int { return a.group - b.group })

	var b bytes.Buffer

	prev := -1
	for _, l := range lines {
		if prev >= 0 && l.group != prev {
			b.WriteString("\n")
		}

		b.WriteString(l.text + "\n")
		prev = l.group
	}

	return b.String()
}

// importGroup classifies a path the way the repository's gci configuration
// does: 0 the standard library, 1 another module, 2 the project's own
// packages. A standard-library path has no dot in its first element.
func importGroup(p, local string) int {
	first, _, _ := strings.Cut(p, "/")

	switch {
	case local != "" && (p == local || strings.HasPrefix(p, local+"/")):
		return 2
	case strings.Contains(first, "."):
		return 1
	default:
		return 0
	}
}

func importDecl(file *ast.File) *ast.GenDecl {
	for _, decl := range file.Decls {
		if gd, ok := decl.(*ast.GenDecl); ok && gd.Tok == token.IMPORT {
			return gd
		}
	}

	return nil
}

func specPath(s ast.Spec) string {
	imp, ok := s.(*ast.ImportSpec)
	if !ok {
		return ""
	}

	v, _ := strconv.Unquote(imp.Path.Value)

	return v
}

func normalize(s string) string { return strings.Join(strings.Fields(s), "") }
