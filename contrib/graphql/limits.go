package graphql

import "fmt"

// cost is the measured size of a selection set.
type cost struct {
	depth      int
	complexity int
	aliases    int
	// introDepth is the depth of __schema / __type selections, bounded by
	// maxIntrospectionDepth instead of MaxDepth so the standard
	// IntrospectionQuery always fits.
	introDepth int
}

// maxIntrospectionDepth fits IntrospectionQuery (depth 14) and stops
// deeper __Type chains, whose list fan-out grows exponentially.
const maxIntrospectionDepth = 16

// limitWalker measures an operation; fragment results are memoized so
// fragments spread many times cost linear, not exponential, work.
type limitWalker struct {
	s     *Schema
	frags map[string]*fragmentDef
	vars  map[string]any
	memo  map[string]cost
}

// checkLimits enforces Limits on op (after validation, so fragments exist
// and are acyclic). Directives are not evaluated: the bound is an upper
// one.
func (s *Schema) checkLimits(doc *document, op *operation, vars map[string]any) []*Error {
	l := s.limits
	if l.MaxDepth == 0 && l.MaxComplexity == 0 && l.MaxAliases == 0 {
		return nil
	}

	w := &limitWalker{s: s, frags: map[string]*fragmentDef{}, vars: vars, memo: map[string]cost{}}
	for _, f := range doc.fragments {
		w.frags[f.name] = f
	}

	root := s.query
	switch op.kind { //nolint:exhaustive // the remaining kinds take the common path below
	case opMutation:
		root = s.mutation
	case opSubscription:
		root = s.subscr
	}

	c := w.selection(op.sel, root)

	var errs []*Error

	exceeded := func(what string, got, limit int) {
		errs = append(errs, &Error{
			Message:    fmt.Sprintf("Query %s %d exceeds the maximum of %d.", what, got, limit),
			Locations:  []Location{op.pos},
			Extensions: map[string]any{"code": CodeLimitExceeded, "limit": what, "value": got, "max": limit},
		})
	}

	if l.MaxDepth > 0 && c.depth > l.MaxDepth {
		exceeded("depth", c.depth, l.MaxDepth)
	}

	if l.MaxComplexity > 0 && c.complexity > l.MaxComplexity {
		exceeded("complexity", c.complexity, l.MaxComplexity)
	}

	if l.MaxAliases > 0 && c.aliases > l.MaxAliases {
		exceeded("aliases", c.aliases, l.MaxAliases)
	}

	if c.introDepth > maxIntrospectionDepth {
		exceeded("introspection depth", c.introDepth, maxIntrospectionDepth)
	}

	return errs
}

// saturating keeps the complexity sum from overflowing on hostile input.
func saturating(a, b int) int {
	const ceiling = 1 << 40
	if a > ceiling-b {
		return ceiling
	}

	return a + b
}

func (w *limitWalker) selection(sel []selection, parent *namedType) cost {
	var out cost

	add := func(c cost) {
		out.depth = max(out.depth, c.depth)
		out.complexity = saturating(out.complexity, c.complexity)
		out.aliases = saturating(out.aliases, c.aliases)
		out.introDepth = max(out.introDepth, c.introDepth)
	}

	for _, s := range sel {
		switch s := s.(type) {
		case *field:
			add(w.field(s, parent))
		case *inlineFragment:
			t := parent
			if s.typeCond != "" {
				t = w.s.types[s.typeCond]
			}

			add(w.selection(s.sel, t))
		case *fragmentSpread:
			if c, ok := w.memo[s.name]; ok {
				add(c)
				continue
			}

			f := w.frags[s.name]
			w.memo[s.name] = cost{} // cycle guard (validation forbids cycles)
			c := w.selection(f.sel, w.s.types[f.typeCond])
			w.memo[s.name] = c
			add(c)
		}
	}

	return out
}

func (w *limitWalker) field(f *field, parent *namedType) cost {
	var c cost
	if f.alias != "" {
		c.aliases = 1
	}

	fd := w.s.fieldFor(parent, f.name)
	if fd == nil || fd == w.s.metaTypename {
		return c
	}

	c.depth = 1
	c.complexity = fd.cost

	if f.sel == nil {
		return c
	}

	sub := w.selection(f.sel, fd.typ.base())
	c.depth += sub.depth
	c.aliases = saturating(c.aliases, sub.aliases)
	c.complexity = saturating(c.complexity, multiply(sub.complexity, w.multiplier(f)))
	c.introDepth = sub.introDepth

	if fd == w.s.metaSchema || fd == w.s.metaType {
		// Introspection is deep by design: its depth has its own bound, but
		// its aliases and fields still count (a fragment chain over __Type
		// otherwise grows the response exponentially).
		c.introDepth = max(c.introDepth, c.depth)
		c.depth = 0
	}

	return c
}

func multiply(a, b int) int {
	const ceiling = 1 << 40
	if b != 0 && a > ceiling/b {
		return ceiling
	}

	return a * b
}

// multiplier reads a page-size argument (first, last, limit) so list
// fields cost their requested size.
func (w *limitWalker) multiplier(f *field) int {
	for _, a := range f.args {
		if a.name != "first" && a.name != "last" && a.name != "limit" {
			continue
		}

		var v any

		switch a.value.kind { //nolint:exhaustive // the remaining kinds take the common path below
		case valVariable:
			v = w.vars[a.value.raw]
		case valInt:
			v, _ = literalToGeneric(a.value, nil)
		}

		if n, ok := toInt(v); ok && n > 1 {
			if n > 1<<20 {
				return 1 << 20
			}

			return int(n)
		}
	}

	return 1
}
