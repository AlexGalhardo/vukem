package viewcheck

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"text/template/parse"
)

// paramRe finds the path parameters of a route pattern ({id}, {path...});
// {$} does not match.
var paramRe = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)(?:\.{3})?\}`) //nolint:gochecknoglobals // compiled once

// route checks {{route "name" "param" value ...}}. Only literal names and
// parameter names are checked; a value passed in a variable is left alone.
func (c *checker) route(args []parse.Node, piped bool) {
	if len(args) == 0 {
		return
	}

	nameNode, ok := args[0].(*parse.StringNode)
	if !ok {
		return
	}

	ri, ok := c.routes[nameNode.Text]
	if !ok {
		c.report(nameNode, "V003", fmt.Sprintf("route %q is not registered", nameNode.Text), false)
		return
	}

	rest := args[1:]
	if piped || len(rest)%2 != 0 {
		return
	}

	given, nodes, literal := paramNames(rest)
	if !literal {
		return
	}

	want := pathParams(ri.Path)

	for _, p := range want {
		if !slices.Contains(given, p) {
			c.report(nameNode, "V004", fmt.Sprintf("route %q needs parameter %q", ri.Name, p), false)
		}
	}

	for i, g := range given {
		if !slices.Contains(want, g) {
			c.report(nodes[i], "V004",
				fmt.Sprintf("route %q has no parameter %q (has: %s)", ri.Name, g, strings.Join(want, ", ")), false)
		}
	}
}

// paramNames returns the parameter names in the even positions of rest;
// literal is false when any of them is not a string literal.
func paramNames(rest []parse.Node) (names []string, nodes []parse.Node, literal bool) {
	for i := 0; i < len(rest); i += 2 {
		s, ok := rest[i].(*parse.StringNode)
		if !ok {
			return nil, nil, false
		}

		names = append(names, s.Text)
		nodes = append(nodes, s)
	}

	return names, nodes, true
}

func pathParams(path string) []string {
	var out []string
	for _, m := range paramRe.FindAllStringSubmatch(path, -1) {
		out = append(out, m[1])
	}

	return out
}
