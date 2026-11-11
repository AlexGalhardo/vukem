package main

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const (
	beginMarker = "<!-- configref:begin -->"
	endMarker   = "<!-- configref:end -->"
)

type variable struct {
	Name, Default, Type, Doc, Field string
}

var sentenceEnd = regexp.MustCompile(`\.(\s|$)`)

// collect lists the environment variables of the package in dir: the `env`
// tags of exported struct fields and the literal names passed to
// `<x>.Source.Get`. Test files are ignored.
func collect(dir string) ([]variable, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	fset := token.NewFileSet()

	var vars []variable

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ParseComments)
		if err != nil {
			return nil, err
		}

		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.GenDecl:
				vars = append(vars, structVars(d)...)
			case *ast.FuncDecl:
				vars = append(vars, sourceVars(d, name)...)
			}
		}
	}

	slices.SortFunc(vars, func(a, b variable) int {
		if c := strings.Compare(a.Name, b.Name); c != 0 {
			return c
		}

		return strings.Compare(a.Field, b.Field)
	})

	// Files are parsed without build constraints, so a struct repeated in
	// _windows.go and _unix.go would print twice: refuse instead.
	for i := 1; i < len(vars); i++ {
		if vars[i].Name == vars[i-1].Name && vars[i].Field == vars[i-1].Field {
			return nil, fmt.Errorf("configref: %s (%s) is declared twice", vars[i].Name, vars[i].Field)
		}
	}

	return vars, nil
}

func structVars(d *ast.GenDecl) []variable {
	var vars []variable

	for _, spec := range d.Specs {
		ts, ok := spec.(*ast.TypeSpec)
		if !ok || !ts.Name.IsExported() {
			continue
		}

		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			continue
		}

		for _, f := range st.Fields.List {
			if f.Tag == nil || len(f.Names) == 0 {
				continue
			}

			tag, err := strconv.Unquote(f.Tag.Value)
			if err != nil {
				continue
			}

			st := reflect.StructTag(tag)

			name := st.Get("env")
			if name == "" {
				continue
			}

			for _, n := range f.Names {
				if !n.IsExported() {
					continue
				}

				vars = append(vars, variable{
					Name:    name,
					Default: st.Get("default"),
					Type:    types.ExprString(f.Type),
					Doc:     firstSentence(f.Doc.Text()),
					Field:   ts.Name.Name + "." + n.Name,
				})
			}
		}
	}

	return vars
}

// sourceVars finds `<x>.Source.Get("NAME")` calls with a literal argument.
func sourceVars(fn *ast.FuncDecl, file string) []variable {
	var vars []variable

	if fn.Body == nil {
		return nil
	}

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return true
		}

		get, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || get.Sel.Name != "Get" {
			return true
		}

		src, ok := get.X.(*ast.SelectorExpr)
		if !ok || src.Sel.Name != "Source" {
			return true
		}

		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}

		if name, err := strconv.Unquote(lit.Value); err == nil {
			vars = append(vars, variable{Name: name, Type: "string", Field: file + ":" + fn.Name.Name})
		}

		return true
	})

	return vars
}

// firstSentence returns the first sentence of a comment on one line; a full
// stop after "e.g" or "i.e" does not end it.
func firstSentence(comment string) string {
	text := strings.Join(strings.Fields(comment), " ")

	for _, m := range sentenceEnd.FindAllStringIndex(text, -1) {
		head := text[:m[0]]
		if strings.HasSuffix(head, "e.g") || strings.HasSuffix(head, "i.e") || strings.HasSuffix(head, "etc") {
			continue
		}

		return head + "."
	}

	return text
}

// render formats the variables as a Markdown table. A config.Secret never
// shows its default.
func render(vars []variable) string {
	var b strings.Builder

	b.WriteString("| Variable | Type | Default | Description | Source |\n")
	b.WriteString("| --- | --- | --- | --- | --- |\n")

	for _, v := range vars {
		def := "—"
		if v.Default != "" && v.Type != "config.Secret" {
			def = "`" + v.Default + "`"
		}

		doc := "—"
		if v.Doc != "" {
			doc = strings.ReplaceAll(v.Doc, "|", `\|`)
		}

		fmt.Fprintf(&b, "| `%s` | `%s` | %s | %s | `%s` |\n", v.Name, v.Type, def, doc, v.Field)
	}

	return b.String()
}

// splice replaces what is between the configref markers in doc with table.
func splice(doc, table string) (string, error) {
	i := strings.Index(doc, beginMarker)
	if i < 0 {
		return "", errors.New("missing " + beginMarker)
	}

	j := strings.Index(doc, endMarker)
	if j < i {
		return "", errors.New("missing " + endMarker + " after " + beginMarker)
	}

	return doc[:i+len(beginMarker)] + "\n" + table + doc[j:], nil
}

// check fails when the generated table differs from the one in path. It never
// writes.
func check(path, table string) error {
	cur, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	want, err := splice(string(cur), table)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}

	if want != string(cur) {
		return fmt.Errorf("%s is out of date: run task config:docs", path)
	}

	return nil
}
