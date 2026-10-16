// Package codemod edits Go source the way a developer would: it appends an
// element to the slice literal returned by a known function and adds the
// imports it needs. Registration in Lumek apps is plain code (no init(), no
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
// equals expr is not added twice.
func AppendToReturn(src []byte, funcName, expr string, imports ...string) ([]byte, error) {
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

	edits = append(edits, importEdits(src, file, off, imports)...)

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

func importEdits(src []byte, file *ast.File, off func(token.Pos) int, paths []string) []edit {
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

	var b bytes.Buffer
	for _, p := range missing {
		b.WriteString("\n" + strconv.Quote(p))
	}

	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.IMPORT {
			continue
		}

		if gd.Lparen.IsValid() {
			return []edit{{at: off(gd.Rparen), text: b.String() + "\n"}}
		}

		// import "x" becomes import ( "x" ... ).
		spec := string(src[off(gd.Specs[0].Pos()):off(gd.Specs[0].End())])

		return []edit{
			{at: off(gd.Pos()), del: off(gd.End()) - off(gd.Pos()), text: "import (\n" + spec + b.String() + "\n)"},
		}
	}

	// No imports yet: add a block after the package clause.
	return []edit{{at: off(file.Name.End()), text: "\n\nimport (" + b.String() + "\n)\n"}}
}

func normalize(s string) string { return strings.Join(strings.Fields(s), "") }
