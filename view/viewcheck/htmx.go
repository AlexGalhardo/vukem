package viewcheck

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"text/template/parse"
)

//nolint:gochecknoglobals // compiled once
var (
	idRe = regexp.MustCompile(`(?:^|\s)id=(?:"([^"]+)"|'([^']+)'|([^\s"'>=]+))`)
	// dynIDRe matches an id whose value continues into an action: id="row-{{.ID}}".
	dynIDRe = regexp.MustCompile(`(?:^|\s)id="[A-Za-z0-9_-]*$`)
	// hxRe matches the htmx attributes holding a selector; only a whole
	// "#id" value is checked, so "closest li", "this" and class selectors are ignored.
	hxRe = regexp.MustCompile(
		`\b(hx-target|hx-select|hx-indicator|hx-include)=(?:"(#[A-Za-z][A-Za-z0-9_-]*)"|'(#[A-Za-z][A-Za-z0-9_-]*)')`,
	)
)

// target is an hx-* reference to an id found in a text node.
type target struct {
	attr, id string
	node     *parse.TextNode
	tr       *parse.Tree
	offset   int
}

// htmx checks the #id targets of every template the page reached against
// the ids of those same templates. An id built by an action (id="row-{{.ID}}")
// may be what the target means, so then a missing id is only a warning.
func (c *checker) htmx() {
	var (
		ids     []string
		targets []target
		dynamic bool
	)

	for _, name := range slices.Sorted(maps.Keys(c.reached)) {
		tr := c.trees[name]

		eachText(tr.Root, func(n *parse.TextNode) {
			text := string(n.Text)

			for _, m := range idRe.FindAllStringSubmatch(text, -1) {
				ids = append(ids, m[1]+m[2]+m[3])
			}

			dynamic = dynamic || dynIDRe.MatchString(text)

			for _, m := range hxRe.FindAllStringSubmatchIndex(text, -1) {
				// The selector is in the double-quoted group or in the single-quoted one.
				start, end := m[4], m[5]
				if start < 0 {
					start, end = m[6], m[7]
				}

				targets = append(targets, target{
					attr: text[m[2]:m[3]], id: text[start+1 : end], node: n, tr: tr, offset: start,
				})
			}
		})
	}

	// A missing target is a warning: the id may come from script, from
	// template.HTML or from a partial reused elsewhere. Strict mode makes it
	// an error, unless an id is built by an action on this page.
	warn := !c.o.Strict || dynamic

	for _, t := range targets {
		if !slices.Contains(ids, t.id) {
			c.targetMissing(t, warn)
		}
	}
}

func (c *checker) targetMissing(t target, warn bool) {
	file, line, col := c.where(t.tr, t.node)

	pre := string(t.node.Text[:t.offset])
	if nl := strings.Count(pre, "\n"); nl == 0 {
		col += t.offset
	} else {
		line += nl
		col = t.offset - strings.LastIndex(pre, "\n")
	}

	c.add(
		file,
		line,
		col,
		codeTarget,
		fmt.Sprintf("%s \"#%s\": no element with that id in this view", t.attr, t.id),
		warn,
	)
}

// eachText calls fn for every text node of l, descending into if, range
// and with bodies but not into other templates.
func eachText(l *parse.ListNode, fn func(*parse.TextNode)) {
	if l == nil {
		return
	}

	for _, n := range l.Nodes {
		switch n := n.(type) {
		case *parse.TextNode:
			fn(n)
		case *parse.IfNode:
			eachText(n.List, fn)
			eachText(n.ElseList, fn)
		case *parse.RangeNode:
			eachText(n.List, fn)
			eachText(n.ElseList, fn)
		case *parse.WithNode:
			eachText(n.List, fn)
			eachText(n.ElseList, fn)
		}
	}
}
