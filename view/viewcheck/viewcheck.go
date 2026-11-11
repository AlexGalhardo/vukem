// Package viewcheck statically checks html/template views against the Go
// types their handlers pass: unknown fields and methods, route names and
// parameters in {{route ...}}, declared blocks and htmx #id targets. It
// reads reflect types and parse trees only; it never executes a template or
// calls a method.
//
// The rule is "when in doubt, say nothing": an expression whose type the
// analyser cannot determine (any, map[string]any, call, and, or, ...) is
// skipped, and listed as a warning only in strict mode.
package viewcheck

import (
	"cmp"
	"fmt"
	"html/template"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"text/template/parse"

	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/view"
)

// codeTarget is the code of a missing htmx target, the one problem that is a
// warning unless Options.Strict is set.
const codeTarget = "V005"

// Problem is one finding of Check.
type Problem struct {
	File    string `json:"file"` // path inside the views FS, with "/"
	Line    int    `json:"line"`
	Col     int    `json:"col"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Warning bool   `json:"warning,omitempty"` // strict-only, does not fail the run
}

// Options configures Check.
type Options struct {
	Engine *view.Engine
	Decls  []view.Decl
	Routes []router.RouteInfo
	Funcs  template.FuncMap // the engine's functions, for return types
	Ext    string           // view file extension (".html")
	Strict bool
}

// Check analyses every declared view and returns the problems, sorted by
// file, line and column.
func Check(o Options) []Problem {
	r := &session{o: o, routes: map[string]router.RouteInfo{}, seen: map[string]bool{}}
	for _, ri := range o.Routes {
		if ri.Name != "" {
			r.routes[ri.Name] = ri
		}
	}

	declared := map[string]bool{}

	for _, d := range o.Decls {
		declared[d.Name] = true
		r.page(d)
	}

	if o.Strict {
		for _, name := range o.Engine.Names() {
			if !declared[name] {
				r.add(name+o.Ext, 1, 1, "V007", fmt.Sprintf("view %q has no typed declaration", name), true)
			}
		}
	}

	slices.SortFunc(r.out, func(a, b Problem) int {
		return cmp.Or(cmp.Compare(a.File, b.File), cmp.Compare(a.Line, b.Line), cmp.Compare(a.Col, b.Col),
			cmp.Compare(a.Code, b.Code), cmp.Compare(a.Message, b.Message))
	})

	return r.out
}

// session is the state shared by every page of one Check call.
type session struct {
	o      Options
	routes map[string]router.RouteInfo
	seen   map[string]bool
	out    []Problem
}

// add records a problem once. Warnings are listed only in strict mode,
// except V005, which is a warning by default.
func (r *session) add(file string, line, col int, code, msg string, warn bool) {
	if warn && !r.o.Strict && code != codeTarget {
		return
	}

	p := Problem{File: file, Line: line, Col: col, Code: code, Message: msg, Warning: warn}
	if key := fmt.Sprint(p); !r.seen[key] {
		r.seen[key] = true
		r.out = append(r.out, p)
	}
}

// page analyses one declared view.
func (r *session) page(d view.Decl) {
	file := d.Name + r.o.Ext

	root, trees, err := r.o.Engine.Trees(d.Name)
	if err != nil {
		r.add(file, 1, 1, "V001", fmt.Sprintf("view %q not found", d.Name), false)
		return
	}

	for _, b := range d.Blocks {
		if trees[b] == nil {
			r.add(file, 1, 1, "V006", fmt.Sprintf("block %q is not defined in %s", b, d.Name), false)
		}
	}

	c := &checker{session: r, decl: d, trees: trees, visited: map[visit]bool{}, reached: map[string]bool{}}
	c.visit(root, deref(d.Data))

	// A fragment is rendered on its own, with the page's data, wherever the
	// page itself calls the block.
	for _, b := range d.Blocks {
		c.visit(b, deref(d.Data))
	}

	c.htmx()
}

// visit is a template analysed with a given dot; it stops repeats and
// recursion.
type visit struct {
	name string
	dot  reflect.Type
}

// checker walks the templates one page reaches.
type checker struct {
	*session

	decl    view.Decl
	trees   map[string]*parse.Tree
	visited map[visit]bool
	reached map[string]bool
	scopes  []map[string]reflect.Type
	tr      *parse.Tree // the tree being walked, for positions
}

func (c *checker) visit(name string, dot reflect.Type) {
	tr := c.trees[name]
	if tr == nil || c.visited[visit{name, dot}] {
		return
	}

	c.visited[visit{name, dot}] = true
	c.reached[name] = true

	outerTree, outerScopes := c.tr, c.scopes
	c.tr, c.scopes = tr, []map[string]reflect.Type{{"$": dot}}

	c.list(tr.Root, dot)

	c.tr, c.scopes = outerTree, outerScopes
}

// where converts a node position to file, line and 1-based column.
func (c *checker) where(tr *parse.Tree, n parse.Node) (file string, line, col int) {
	loc, _ := tr.ErrorContext(n)

	// loc is "name:line:col" with a 0-based column; the name may hold ':'.
	i := strings.LastIndex(loc, ":")
	col, _ = strconv.Atoi(loc[i+1:])
	rest := loc[:i]
	j := strings.LastIndex(rest, ":")
	line, _ = strconv.Atoi(rest[j+1:])

	return tr.ParseName + c.o.Ext, line, col + 1
}

// nodeAt is a stand-in node for a byte position, which is what where needs.
func (*checker) nodeAt(pos parse.Pos) parse.Node {
	return &parse.TextNode{NodeType: parse.NodeText, Pos: pos}
}

func (c *checker) report(n parse.Node, code, msg string, warn bool) {
	file, line, col := c.where(c.tr, n)
	if code == "V002" && c.tr.ParseName != c.decl.Name {
		msg += " (rendering " + c.decl.Name + ")"
	}

	c.add(file, line, col, code, msg, warn)
}

func (c *checker) push() { c.scopes = append(c.scopes, map[string]reflect.Type{}) }
func (c *checker) pop()  { c.scopes = c.scopes[:len(c.scopes)-1] }

func (c *checker) lookupVar(name string) reflect.Type {
	for _, sc := range slices.Backward(c.scopes) {
		if t, ok := sc[name]; ok {
			return t
		}
	}

	return nil
}

// declare binds a variable in the innermost scope, or, for "=", updates the
// scope that already holds it.
func (c *checker) declare(name string, t reflect.Type, assign bool) {
	if assign {
		for _, sc := range slices.Backward(c.scopes) {
			if old, ok := sc[name]; ok {
				if old != t {
					t = nil // reassigned with another type: not checked any more
				}

				sc[name] = t

				return
			}
		}
	}

	c.scopes[len(c.scopes)-1][name] = t
}

func (c *checker) list(l *parse.ListNode, dot reflect.Type) {
	if l == nil {
		return
	}

	for _, n := range l.Nodes {
		c.node(n, dot)
	}
}

func (c *checker) node(n parse.Node, dot reflect.Type) {
	switch n := n.(type) {
	case *parse.ActionNode:
		c.pipe(n.Pipe, dot, true)
	case *parse.IfNode:
		c.ifNode(n, dot)
	case *parse.RangeNode:
		c.rangeNode(n, dot)
	case *parse.WithNode:
		c.withNode(n, dot)
	case *parse.TemplateNode:
		c.templateNode(n, dot)
	}
}

func (c *checker) ifNode(n *parse.IfNode, dot reflect.Type) {
	c.push()
	defer c.pop()

	c.pipe(n.Pipe, dot, true)
	c.list(n.List, dot)
	c.list(n.ElseList, dot)
}

func (c *checker) withNode(n *parse.WithNode, dot reflect.Type) {
	c.push()
	defer c.pop()

	t := c.pipe(n.Pipe, dot, true)
	c.list(n.List, t)
	c.list(n.ElseList, dot)
}

func (c *checker) rangeNode(n *parse.RangeNode, dot reflect.Type) {
	c.push()
	defer c.pop()

	key, elem, _ := rangeTypes(c.pipe(n.Pipe, dot, false))

	switch len(n.Pipe.Decl) {
	case 1:
		c.declare(n.Pipe.Decl[0].Ident[0], elem, false)
	case 2:
		c.declare(n.Pipe.Decl[0].Ident[0], key, false)
		c.declare(n.Pipe.Decl[1].Ident[0], elem, false)
	}

	c.list(n.List, elem)
	c.list(n.ElseList, dot)
}

func (c *checker) templateNode(n *parse.TemplateNode, dot reflect.Type) {
	var t reflect.Type
	if n.Pipe != nil {
		t = deref(c.pipe(n.Pipe, dot, true))
	}

	if c.trees[n.Name] == nil {
		c.report(n, "V009", fmt.Sprintf("template %q is not defined", n.Name), false)
		return
	}

	c.visit(n.Name, t)
}

// pipe returns the type of the last command and, when bind is set, binds
// the pipeline's declared variables to it.
func (c *checker) pipe(p *parse.PipeNode, dot reflect.Type, bind bool) reflect.Type {
	var t reflect.Type

	for i, cmd := range p.Cmds {
		t = c.command(cmd, dot, i > 0)
	}

	if bind {
		for _, d := range p.Decl {
			c.declare(d.Ident[0], t, p.IsAssign)
		}
	}

	return t
}

func (c *checker) command(cmd *parse.CommandNode, dot reflect.Type, piped bool) reflect.Type {
	if id, ok := cmd.Args[0].(*parse.IdentifierNode); ok {
		return c.call(id, cmd.Args[1:], dot, piped)
	}

	t := c.operand(cmd.Args[0], dot)

	for _, a := range cmd.Args[1:] {
		c.operand(a, dot)
	}

	return t
}

// operand returns the static type of one argument; nil when unknown.
func (c *checker) operand(n parse.Node, dot reflect.Type) reflect.Type {
	switch n := n.(type) {
	case *parse.DotNode:
		return dot
	case *parse.FieldNode:
		// The parser positions a chain of fields at its second segment.
		first := n.Pos
		if len(n.Ident) > 1 {
			first -= parse.Pos(len(n.Ident[0]) + 1)
		}

		return c.fields(n, dot, n.Ident, first, first)
	case *parse.VariableNode:
		base := c.lookupVar(n.Ident[0])
		if len(n.Ident) == 1 {
			return base
		}

		return c.fields(n, base, n.Ident[1:], n.Pos, n.Pos-parse.Pos(len(n.Ident[0])))
	case *parse.ChainNode:
		return c.fields(n, c.operand(n.Node, dot), n.Field, n.Pos, n.Pos)
	case *parse.PipeNode:
		return c.pipe(n, dot, false)
	case *parse.IdentifierNode:
		return c.call(n, nil, dot, false)
	case *parse.StringNode:
		return reflect.TypeFor[string]()
	case *parse.BoolNode:
		return reflect.TypeFor[bool]()
	case *parse.NumberNode:
		return numberType(n)
	}

	return nil
}

func numberType(n *parse.NumberNode) reflect.Type {
	switch {
	case n.IsInt:
		return reflect.TypeFor[int]()
	case n.IsFloat:
		return reflect.TypeFor[float64]()
	default:
		return nil
	}
}

// fields resolves a chain of names from base. A name the type lacks is
// V002; a type the analyser cannot see through stops the chain silently
// (V008 in strict mode).
//
// first is the position of the first name (with its dot) and begin where
// the expression starts; V002 points at the offending name.
func (c *checker) fields(n parse.Node, base reflect.Type, names []string, first, begin parse.Pos) reflect.Type {
	t := base
	at := first

	for _, name := range names {
		next, res := member(t, name)
		if res == missing {
			c.report(
				c.nodeAt(at),
				"V002",
				fmt.Sprintf("field or method %q not found in %s", name, typeName(deref(t))),
				false,
			)
			return nil
		}

		at += parse.Pos(len(name) + 1)

		t = next
		if res == unknown {
			break
		}
	}

	if isUnknown(t) {
		c.report(c.nodeAt(begin), "V008", fmt.Sprintf("%s: type is %s, not checked", n.String(), typeName(t)), true)
	}

	return t
}

// call analyses a function call: its arguments, the route rules and the
// type of its result.
func (c *checker) call(id *parse.IdentifierNode, args []parse.Node, dot reflect.Type, piped bool) reflect.Type {
	if strings.HasPrefix(id.Ident, "_html_template_") {
		return nil
	}

	types := make([]reflect.Type, len(args))
	for i, a := range args {
		types[i] = c.operand(a, dot)
	}

	if id.Ident == "route" {
		c.route(args, piped)
	}

	if fn, ok := c.o.Funcs[id.Ident]; ok {
		return funcResult(fn)
	}

	return builtinResult(id.Ident, types)
}
