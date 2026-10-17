package main

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// skipDirs are never searched: tests data, the docs site and VCS metadata.
var skipDirs = []string{"testdata", "node_modules", "site", "vendor"} //nolint:gochecknoglobals // read-only list

// param is one parameter (or the receiver) of a wrapper.
type param struct {
	Name string
	// Type is the parameter type as written in the wrapper's file.
	Type     string
	Variadic bool
}

// wrapper is a function or method marked //go:fix inline.
type wrapper struct {
	// Dir is the package directory, File the source file (both OS paths).
	Dir, File string
	Package   string
	Name      string
	// Recv is the receiver of a method, nil for a function.
	Recv    *param
	Params  []param
	Results int
	// Body is the source of the call the wrapper forwards to, the
	// expression go fix substitutes for each call.
	Body string
	// Imports are the import specs the parameter types need, as written in
	// the generated file ("name \"path\"").
	Imports []string
}

func (w wrapper) String() string {
	if w.Recv != nil {
		return fmt.Sprintf("%s.(%s).%s", w.Package, w.Recv.Type, w.Name)
	}

	return w.Package + "." + w.Name
}

// findWrappers parses every non-test Go file under root and returns the
// functions and methods whose doc comment has a //go:fix inline directive.
func findWrappers(root string) ([]wrapper, error) {
	var (
		wrappers []wrapper
		errs     []error
	)

	fset := token.NewFileSet()

	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	err = fs.WalkDir(r.FS(), ".", func(rel string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			name := d.Name()
			if rel != "." && (slices.Contains(skipDirs, name) || strings.HasPrefix(name, ".") ||
				strings.HasPrefix(name, "_")) {
				return fs.SkipDir
			}

			return nil
		}

		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
			return nil
		}

		src, err := fs.ReadFile(r.FS(), rel)
		if err != nil {
			return err
		}

		p := filepath.Join(root, filepath.FromSlash(rel))

		if !bytes.Contains(src, []byte("//go:fix inline")) {
			return nil
		}

		f, err := parser.ParseFile(fset, p, src, parser.ParseComments)
		if err != nil {
			return err
		}

		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || !hasInlineDirective(fn.Doc) {
				continue
			}

			w, err := newWrapper(fset, f, fn, src)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %s: %w", p, fn.Name.Name, err))

				continue
			}

			w.Dir, w.File = filepath.Dir(p), p
			wrappers = append(wrappers, w)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return wrappers, errors.Join(errs...)
}

func hasInlineDirective(doc *ast.CommentGroup) bool {
	if doc == nil {
		return false
	}

	return slices.ContainsFunc(doc.List, func(c *ast.Comment) bool { return c.Text == "//go:fix inline" })
}

// newWrapper describes fn. Only the shapes the check can predict are
// supported: no type parameters, no build constraints, and a body that is a
// single return or expression statement of one call.
func newWrapper(fset *token.FileSet, f *ast.File, fn *ast.FuncDecl, src []byte) (wrapper, error) {
	w := wrapper{Package: f.Name.Name, Name: fn.Name.Name}

	if fn.Type.TypeParams != nil && len(fn.Type.TypeParams.List) > 0 {
		return w, errors.New("unsupported: type parameters")
	}

	if slices.ContainsFunc(f.Comments, func(g *ast.CommentGroup) bool {
		return g.Pos() < f.Package && strings.HasPrefix(g.List[0].Text, "//go:build")
	}) {
		return w, errors.New("unsupported: build constraint")
	}

	text := func(n ast.Node) string {
		return string(src[fset.Position(n.Pos()).Offset:fset.Position(n.End()).Offset])
	}

	var typeExprs []ast.Expr

	if fn.Recv != nil && len(fn.Recv.List) == 1 {
		rf := fn.Recv.List[0]
		if _, ok := rf.Type.(*ast.IndexExpr); ok {
			return w, errors.New("unsupported: generic receiver")
		}

		name := "recv"
		if len(rf.Names) == 1 && rf.Names[0].Name != "_" {
			name = rf.Names[0].Name
		}

		w.Recv = &param{Name: name, Type: text(rf.Type)}
		typeExprs = append(typeExprs, rf.Type)
	}

	for _, field := range fn.Type.Params.List {
		typ := field.Type
		variadic := false

		if el, ok := typ.(*ast.Ellipsis); ok {
			typ, variadic = el.Elt, true
		}

		typeExprs = append(typeExprs, typ)

		names := field.Names
		if len(names) == 0 {
			names = []*ast.Ident{nil}
		}

		for _, n := range names {
			name := "arg" + strconv.Itoa(len(w.Params))
			if n != nil && n.Name != "_" {
				name = n.Name
			}

			w.Params = append(w.Params, param{Name: name, Type: text(typ), Variadic: variadic})
		}
	}

	if fn.Type.Results != nil {
		for _, field := range fn.Type.Results.List {
			w.Results += max(1, len(field.Names))
		}
	}

	body, err := forwardedCall(fn.Body, w.Results)
	if err != nil {
		return w, err
	}

	w.Body = text(body)

	imports, err := neededImports(f, typeExprs)
	if err != nil {
		return w, err
	}

	w.Imports = imports

	return w, nil
}

func forwardedCall(body *ast.BlockStmt, results int) (ast.Expr, error) {
	if body == nil || len(body.List) != 1 {
		return nil, errors.New("unsupported: body is not a single statement")
	}

	var expr ast.Expr

	switch s := body.List[0].(type) {
	case *ast.ReturnStmt:
		if len(s.Results) == 1 && results > 0 {
			expr = s.Results[0]
		}
	case *ast.ExprStmt:
		if results == 0 {
			expr = s.X
		}
	}

	if _, ok := expr.(*ast.CallExpr); !ok {
		return nil, errors.New("unsupported: body is not a single forwarded call")
	}

	return expr, nil
}

// neededImports returns the import specs of f that the parameter types use,
// named explicitly so the generated file does not depend on package names.
func neededImports(f *ast.File, types []ast.Expr) ([]string, error) {
	var names []string

	for _, t := range types {
		ast.Inspect(t, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				if id, ok := sel.X.(*ast.Ident); ok && !slices.Contains(names, id.Name) {
					names = append(names, id.Name)
				}

				return false
			}

			return true
		})
	}

	var specs []string

	for _, name := range names {
		spec, ok := findImport(f, name)
		if !ok {
			return nil, fmt.Errorf("no import for %s", name)
		}

		specs = append(specs, spec)
	}

	slices.Sort(specs)

	return specs, nil
}

func findImport(f *ast.File, name string) (string, bool) {
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}

		if imp.Name != nil {
			if imp.Name.Name == name {
				return name + " " + imp.Path.Value, true
			}

			continue
		}

		base := path.Base(p)
		if strings.HasPrefix(base, "v") && path.Dir(p) != "." {
			if _, err := strconv.Atoi(base[1:]); err == nil {
				base = path.Base(path.Dir(p))
			}
		}

		if base == name {
			return name + " " + imp.Path.Value, true
		}
	}

	return "", false
}

// caseCall is one generated call of a wrapper and the line go fix must turn
// it into.
type caseCall struct {
	Wrapper string
	Kind    string
	// Call and Want are the statement before and after inlining.
	Call, Want string
}

// genCases returns the caller functions for w and their expected rewrites:
//
//   - same: every argument is a variable named like the parameter it fills;
//   - rotated: argument i is a variable named like parameter i+1, so a mix-up
//     between a parameter and a same-named argument shows as swapped names;
//   - literal-N: argument N is an expression while a variable named like
//     parameter N is in scope (the NewMiddleware(b, MiddlewareConfig{}) case).
func genCases(w wrapper) (string, []caseCall, error) {
	slots := w.Params
	if w.Recv != nil {
		slots = append([]param{*w.Recv}, w.Params...)
	}

	names := make([]string, len(slots))
	for i, p := range slots {
		names[i] = p.Name
	}

	var (
		b     strings.Builder
		cases []caseCall
	)

	emit := func(kind string, declNames, args []string, prelude string) error {
		subst := map[string]string{}
		for i, p := range slots {
			subst[p.Name] = args[i]
		}

		want, err := substitute(w.Body, subst)
		if err != nil {
			return err
		}

		call := callExpr(w, args)
		lhs := ""

		if w.Results > 0 {
			lhs = strings.TrimSuffix(strings.Repeat("_, ", w.Results), ", ") + " = "
		}

		var decls []string

		for i, p := range slots {
			typ := p.Type
			if p.Variadic {
				typ = "..." + typ
			}

			decls = append(decls, declNames[i]+" "+typ)
		}

		fmt.Fprintf(
			&b,
			"\n// %s %s\nfunc _(%s) {\n%s\t%s%s\n}\n",
			w,
			kind,
			strings.Join(decls, ", "),
			prelude,
			lhs,
			call,
		)
		cases = append(cases, caseCall{Wrapper: w.String(), Kind: kind, Call: lhs + call, Want: lhs + want})

		return nil
	}

	if err := emit("same", names, names, ""); err != nil {
		return "", nil, err
	}

	if len(names) > 1 {
		rotated := append(slices.Clone(names[1:]), names[0])
		if err := emit("rotated", rotated, rotated, ""); err != nil {
			return "", nil, err
		}
	}

	for i, p := range slots {
		if p.Variadic || (w.Recv != nil && i == 0) || !onlyCallArgument(w.Body, p.Name) {
			continue
		}

		args := slices.Clone(names)
		args[i] = "*new(" + p.Type + ")"

		if err := emit("literal-"+strconv.Itoa(i), names, args, "\t_ = "+p.Name+"\n"); err != nil {
			return "", nil, err
		}
	}

	return b.String(), cases, nil
}

func callExpr(w wrapper, args []string) string {
	params := args

	fun := w.Name
	if w.Recv != nil {
		fun = args[0] + "." + w.Name
		params = args[1:]
	}

	list := strings.Join(params, ", ")
	if len(w.Params) > 0 && w.Params[len(w.Params)-1].Variadic {
		list += "..."
	}

	return fun + "(" + list + ")"
}

// substitute prints expr with every identifier named in subst replaced by
// its value, the result a correct inliner produces for the generated call.
// Selector fields and composite literal keys are not identifiers to replace.
func substitute(body string, subst map[string]string) (string, error) {
	e, err := parser.ParseExpr(body)
	if err != nil {
		return "", err
	}

	var replace func(n ast.Node)

	replace = func(n ast.Node) {
		ast.Inspect(n, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.SelectorExpr:
				replace(x.X)

				return false
			case *ast.KeyValueExpr:
				if _, ok := x.Key.(*ast.Ident); !ok {
					replace(x.Key)
				}

				replace(x.Value)

				return false
			case *ast.Ident:
				if v, ok := subst[x.Name]; ok {
					x.Name = v
				}
			}

			return true
		})
	}

	replace(e)

	var b bytes.Buffer
	if err := format.Node(&b, token.NewFileSet(), e); err != nil {
		return "", err
	}

	return b.String(), nil
}

// onlyCallArgument reports whether every use of name in expr is a whole
// argument of a call, so replacing it by an expression needs no parentheses
// and keeps the evaluation order.
func onlyCallArgument(body, name string) bool {
	expr, err := parser.ParseExpr(body)
	if err != nil {
		return false
	}

	ok := true

	ast.Inspect(expr, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			for _, a := range x.Args {
				if id, isID := a.(*ast.Ident); isID && id.Name == name {
					continue
				}

				ast.Inspect(a, func(n ast.Node) bool {
					if id, isID := n.(*ast.Ident); isID && id.Name == name {
						ok = false
					}

					return ok
				})
			}

			ast.Inspect(x.Fun, func(n ast.Node) bool {
				if id, isID := n.(*ast.Ident); isID && id.Name == name {
					ok = false
				}

				return ok
			})

			return false
		case *ast.Ident:
			if x.Name == name {
				ok = false
			}
		}

		return ok
	})

	return ok
}
