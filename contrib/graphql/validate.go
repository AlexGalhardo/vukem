package graphql

import (
	"fmt"
	"slices"
	"strings"
)

// maxValidationErrors stops validation of hopeless documents early.
const maxValidationErrors = 100

// validator implements the validation rules of spec §5.
type validator struct {
	s             *Schema
	doc           *document
	frags         map[string]*fragmentDef
	errs          []*Error
	introspection bool

	scopes map[any]*scope // *operation or *fragmentDef
}

// scope collects what a definition uses, for the variable and fragment
// rules that need transitive closure.
type scope struct {
	usages  []varUsage
	spreads []string
}

type varUsage struct {
	name       string
	typ        *gtype
	hasDefault bool // the location (argument / input field) has a default
	pos        Location
}

func (v *validator) report(msg string, locs ...Location) {
	if len(v.errs) < maxValidationErrors {
		v.errs = append(v.errs, validationError(msg, locs...))
	}
}

// validate checks doc against the schema. Errors are returned in document
// order.
func (s *Schema) validate(doc *document, introspection bool) []*Error {
	v := &validator{
		s: s, doc: doc, frags: map[string]*fragmentDef{}, scopes: map[any]*scope{},
		introspection: introspection,
	}

	v.operationNames()
	v.fragmentNames()

	for _, f := range doc.fragments {
		sc := &scope{}
		v.scopes[f] = sc

		t := v.s.types[f.typeCond]

		switch {
		case t == nil:
			v.report(fmt.Sprintf("Unknown type %q.", f.typeCond), f.pos)
		case !t.isComposite():
			v.report(fmt.Sprintf("Fragment %q cannot condition on non composite type %q.", f.name, f.typeCond), f.pos)
		default:
			v.directives(f.directives, "FRAGMENT_DEFINITION", sc)
			v.selectionSet(f.sel, t, sc)
		}
	}

	for _, op := range doc.operations {
		sc := &scope{}
		v.scopes[op] = sc

		root := v.root(op)
		if root == nil {
			v.report(fmt.Sprintf("Schema is not configured to execute %s operation.", op.kind), op.pos)
			continue
		}

		v.variableDefinitions(op, sc)
		v.directives(op.directives, strings.ToUpper(string(op.kind)), sc)
		v.selectionSet(op.sel, root, sc)

		if op.kind == opSubscription {
			if n := len(collectFieldsStatic(op.sel, v.frags)); n != 1 {
				v.report(subscriptionMessage(op), op.pos)
			}
		}
	}

	v.fragmentCycles()
	v.unusedFragments()

	for _, op := range doc.operations {
		if v.root(op) != nil {
			v.variableUsages(op)
		}
	}

	return v.errs
}

func subscriptionMessage(op *operation) string {
	if op.name == "" {
		return "Anonymous Subscription must select only one top level field."
	}

	return fmt.Sprintf("Subscription %q must select only one top level field.", op.name)
}

func (v *validator) root(op *operation) *namedType {
	switch op.kind { //nolint:exhaustive // the remaining kinds take the common path below
	case opMutation:
		return v.s.mutation
	case opSubscription:
		return v.s.subscr
	}

	return v.s.query
}

func (v *validator) operationNames() {
	seen := map[string]bool{}

	for _, op := range v.doc.operations {
		if op.name == "" {
			if len(v.doc.operations) > 1 {
				v.report("This anonymous operation must be the only defined operation.", op.pos)
			}

			continue
		}

		if seen[op.name] {
			v.report(fmt.Sprintf("There can be only one operation named %q.", op.name), op.pos)
		}

		seen[op.name] = true
	}
}

func (v *validator) fragmentNames() {
	for _, f := range v.doc.fragments {
		if v.frags[f.name] != nil {
			v.report(fmt.Sprintf("There can be only one fragment named %q.", f.name), v.frags[f.name].pos, f.pos)
			continue
		}

		v.frags[f.name] = f
	}
}

func (v *validator) variableDefinitions(op *operation, sc *scope) {
	seen := map[string]bool{}

	for _, vd := range op.vars {
		if seen[vd.name] {
			v.report(fmt.Sprintf("There can be only one variable named \"$%s\".", vd.name), vd.pos)
		}

		seen[vd.name] = true

		v.directives(vd.directives, "VARIABLE_DEFINITION", sc)

		t := v.typeOf(vd.typ)
		if t == nil {
			v.report(fmt.Sprintf("Unknown type %q.", vd.typ.named()), vd.typ.pos)
			continue
		}

		if !t.base().isInput() {
			v.report(fmt.Sprintf("Variable \"$%s\" cannot be non-input type %q.", vd.name, vd.typ), vd.typ.pos)
			continue
		}

		if vd.def != nil {
			if _, err := coerceLiteral(vd.def, t, nil); err != nil {
				v.report(fmt.Sprintf("Variable \"$%s\" has invalid default value: %s.", vd.name, err), vd.def.pos)
			}
		}
	}
}

// typeOf resolves a source type reference, or nil when unknown.
func (v *validator) typeOf(r *typeRef) *gtype {
	return v.s.typeOf(r)
}

func (s *Schema) typeOf(r *typeRef) *gtype {
	var t *gtype

	if r.elem != nil {
		of := s.typeOf(r.elem)
		if of == nil {
			return nil
		}

		t = &gtype{kind: kindList, ofType: of}
	} else {
		n := s.types[r.name]
		if n == nil {
			return nil
		}

		t = &gtype{kind: n.kind, named: n}
	}

	if r.nonNull {
		t = &gtype{kind: kindNonNull, ofType: t}
	}

	return t
}

func (v *validator) selectionSet(sel []selection, parent *namedType, sc *scope) {
	for _, s := range sel {
		switch s := s.(type) {
		case *field:
			v.field(s, parent, sc)
		case *fragmentSpread:
			v.directives(s.directives, "FRAGMENT_SPREAD", sc)
			sc.spreads = append(sc.spreads, s.name)

			f := v.frags[s.name]
			if f == nil {
				v.report(fmt.Sprintf("Unknown fragment %q.", s.name), s.pos)
				continue
			}

			if t := v.s.types[f.typeCond]; t != nil && t.isComposite() && !overlap(parent, t) {
				v.report(fmt.Sprintf("Fragment %q cannot be spread here as objects of type %q can never be of type %q.",
					s.name, parent.name, t.name), s.pos)
			}
		case *inlineFragment:
			v.directives(s.directives, "INLINE_FRAGMENT", sc)

			t := parent
			if s.typeCond != "" {
				t = v.s.types[s.typeCond]

				switch {
				case t == nil:
					v.report(fmt.Sprintf("Unknown type %q.", s.typeCond), s.pos)
					continue
				case !t.isComposite():
					v.report(fmt.Sprintf("Fragment cannot condition on non composite type %q.", s.typeCond), s.pos)
					continue
				case !overlap(parent, t):
					v.report(
						fmt.Sprintf("Fragment cannot be spread here as objects of type %q can never be of type %q.",
							parent.name, t.name),
						s.pos,
					)
					continue
				}
			}

			v.selectionSet(s.sel, t, sc)
		}
	}

	v.mergeable(sel, parent)
}

// overlap reports whether some object type is possible for both a and b.
func overlap(a, b *namedType) bool {
	for _, o := range possibleObjects(a) {
		if slices.Contains(possibleObjects(b), o) {
			return true
		}
	}

	return false
}

func possibleObjects(t *namedType) []*namedType {
	if t.kind == kindObject {
		return []*namedType{t}
	}

	return t.possible
}

func (v *validator) field(f *field, parent *namedType, sc *scope) {
	fd := v.s.fieldFor(parent, f.name)
	if fd == nil {
		v.report(fmt.Sprintf("Cannot query field %q on type %q.", f.name, parent.name), f.pos)
		return
	}

	if !v.introspection && (fd == v.s.metaSchema || fd == v.s.metaType) {
		v.report("GraphQL introspection is not allowed, but the query contained __schema or __type.", f.pos)
		return
	}

	v.arguments(f.args, fd.args, fd.argMap, fmt.Sprintf("field %q", parent.name+"."+f.name), f.pos, sc)
	v.directives(f.directives, "FIELD", sc)

	base := fd.typ.base()

	switch {
	case base.isLeaf() && f.sel != nil:
		v.report(
			fmt.Sprintf("Field %q must not have a selection since type %q has no subfields.", f.name, fd.typ),
			f.pos,
		)
	case !base.isLeaf() && f.sel == nil:
		v.report(fmt.Sprintf("Field %q of type %q must have a selection of subfields. Did you mean \"%s { ... }\"?",
			f.name, fd.typ, f.name), f.pos)
	case f.sel != nil:
		v.selectionSet(f.sel, base, sc)
	}
}

func (v *validator) arguments(
	args []*argument, defs []*inputValue, defMap map[string]*inputValue, owner string, pos Location, sc *scope,
) {
	seen := map[string]bool{}

	for _, a := range args {
		if seen[a.name] {
			v.report(fmt.Sprintf("There can be only one argument named %q.", a.name), a.pos)
			continue
		}

		seen[a.name] = true

		def := defMap[a.name]
		if def == nil {
			v.report(fmt.Sprintf("Unknown argument %q on %s.", a.name, owner), a.pos)
			continue
		}

		v.value(a.value, def.typ, def.hasDefault, sc, fmt.Sprintf("Argument %q", a.name))
	}

	for _, d := range defs {
		if d.typ.kind == kindNonNull && !d.hasDefault && !seen[d.name] {
			v.report(fmt.Sprintf("%s argument %q of type %q is required, but it was not provided.",
				capitalize(owner), d.name, d.typ), pos)
		}
	}
}

func capitalize(s string) string {
	if s == "" {
		return s
	}

	return strings.ToUpper(s[:1]) + s[1:]
}

// value checks a literal against t (spec §5.6.1) and records variable
// usages with their expected type for §5.8.5.
func (v *validator) value(val *value, t *gtype, hasDefault bool, sc *scope, what string) {
	if val.kind == valVariable {
		sc.usages = append(sc.usages, varUsage{name: val.raw, typ: t, hasDefault: hasDefault, pos: val.pos})
		return
	}

	if t.kind == kindNonNull {
		if val.kind == valNull {
			v.report(fmt.Sprintf("%s: expected value of type %q, found null.", what, t), val.pos)
			return
		}

		t = t.ofType
	}

	if val.kind == valNull {
		return
	}

	if t.kind == kindList {
		if val.kind == valList {
			for _, it := range val.list {
				v.value(it, t.ofType, false, sc, what)
			}

			return
		}

		v.value(val, t.ofType, false, sc, what)

		return
	}

	if t.named.kind == kindInputObject {
		v.objectValue(val, t.named, sc, what)
		return
	}

	if containsVariable(val) {
		return // custom scalar literal with variables inside: checked at execution
	}

	if _, err := coerceLiteral(val, t, nil); err != nil {
		v.report(fmt.Sprintf("%s has invalid value %s: %s.", what, describeLiteral(val), err), val.pos)
	}
}

func (v *validator) objectValue(val *value, n *namedType, sc *scope, what string) {
	if val.kind != valObject {
		v.report(fmt.Sprintf("%s: expected value of type %q, found %s.", what, n.name, describeLiteral(val)), val.pos)
		return
	}

	seen := map[string]bool{}

	for _, f := range val.fields {
		if seen[f.name] {
			v.report(fmt.Sprintf("There can be only one input field named %q.", f.name), f.pos)
			continue
		}

		seen[f.name] = true

		iv := n.inputMap[f.name]
		if iv == nil {
			v.report(fmt.Sprintf("Field %q is not defined by type %q.", f.name, n.name), f.pos)
			continue
		}

		v.value(f.value, iv.typ, iv.hasDefault, sc, what)
	}

	for _, iv := range n.inputs {
		if iv.typ.kind == kindNonNull && !iv.hasDefault && !seen[iv.name] {
			v.report(fmt.Sprintf("Field %q of required type %q was not provided.", n.name+"."+iv.name, iv.typ), val.pos)
		}
	}
}

func containsVariable(val *value) bool {
	switch val.kind { //nolint:exhaustive // the remaining kinds take the common path below
	case valVariable:
		return true
	case valList:
		return slices.ContainsFunc(val.list, containsVariable)
	case valObject:
		return slices.ContainsFunc(val.fields, func(f *objectField) bool { return containsVariable(f.value) })
	}

	return false
}

func (v *validator) directives(ds []*directive, location string, sc *scope) {
	seen := map[string]bool{}

	for _, d := range ds {
		def := v.s.directives[d.name]
		if def == nil {
			v.report(fmt.Sprintf("Unknown directive \"@%s\".", d.name), d.pos)
			continue
		}

		if !slices.Contains(def.locations, location) {
			v.report(fmt.Sprintf("Directive \"@%s\" may not be used on %s.", d.name, location), d.pos)
			continue
		}

		if seen[d.name] && !def.repeatable {
			v.report(fmt.Sprintf("The directive \"@%s\" can only be used once at this location.", d.name), d.pos)
		}

		seen[d.name] = true

		v.arguments(d.args, def.args, def.argMap, fmt.Sprintf("directive \"@%s\"", d.name), d.pos, sc)
	}
}

func (v *validator) fragmentCycles() {
	const (
		unvisited = iota
		visiting
		done
	)

	state := map[string]int{}

	var visit func(f *fragmentDef, path []string)

	visit = func(f *fragmentDef, path []string) {
		state[f.name] = visiting

		for _, name := range v.scopes[f].spreads {
			next := v.frags[name]
			if next == nil {
				continue
			}

			switch state[name] {
			case visiting:
				cycle := append(path[slices.Index(path, name):], name) //nolint:gocritic // fresh slice per report
				via := ""

				if len(cycle) > 2 {
					via = " via " + strings.Join(quoteAll(cycle[1:len(cycle)-1]), ", ")
				}

				v.report(fmt.Sprintf("Cannot spread fragment %q within itself%s.", name, via), next.pos)
			case unvisited:
				visit(next, append(slices.Clone(path), name))
			}
		}

		state[f.name] = done
	}

	for _, f := range v.doc.fragments {
		if state[f.name] == unvisited && v.scopes[f] != nil {
			visit(f, []string{f.name})
		}
	}
}

func quoteAll(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = fmt.Sprintf("%q", n)
	}

	return out
}

// reachable returns the fragments transitively spread from sc.
func (v *validator) reachable(sc *scope) []*fragmentDef {
	var out []*fragmentDef

	seen := map[string]bool{}
	queue := slices.Clone(sc.spreads)

	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]

		if seen[name] || v.frags[name] == nil {
			continue
		}

		seen[name] = true
		f := v.frags[name]
		out = append(out, f)
		queue = append(queue, v.scopes[f].spreads...)
	}

	return out
}

func (v *validator) unusedFragments() {
	used := map[*fragmentDef]bool{}

	for _, op := range v.doc.operations {
		for _, f := range v.reachable(v.scopes[op]) {
			used[f] = true
		}
	}

	for _, f := range v.doc.fragments {
		if !used[f] && v.frags[f.name] == f {
			v.report(fmt.Sprintf("Fragment %q is never used.", f.name), f.pos)
		}
	}
}

func (v *validator) variableUsages(op *operation) {
	sc := v.scopes[op]
	usages := slices.Clone(sc.usages)

	for _, f := range v.reachable(sc) {
		usages = append(usages, v.scopes[f].usages...)
	}

	defs := map[string]*varDef{}
	for _, vd := range op.vars {
		defs[vd.name] = vd
	}

	used := map[string]bool{}

	for _, u := range usages {
		used[u.name] = true

		vd := defs[u.name]
		if vd == nil {
			if op.name != "" {
				v.report(
					fmt.Sprintf("Variable \"$%s\" is not defined by operation %q.", u.name, op.name),
					u.pos,
					op.pos,
				)
			} else {
				v.report(fmt.Sprintf("Variable \"$%s\" is not defined.", u.name), u.pos, op.pos)
			}

			continue
		}

		vt := v.typeOf(vd.typ)
		if vt == nil || !vt.base().isInput() {
			continue
		}

		if !usageAllowed(vt, vd.def, u) {
			v.report(fmt.Sprintf("Variable \"$%s\" of type %q used in position expecting type %q.", u.name, vt, u.typ),
				vd.pos, u.pos)
		}
	}

	for _, vd := range op.vars {
		if !used[vd.name] {
			if op.name != "" {
				v.report(fmt.Sprintf("Variable \"$%s\" is never used in operation %q.", vd.name, op.name), vd.pos)
			} else {
				v.report(fmt.Sprintf("Variable \"$%s\" is never used.", vd.name), vd.pos)
			}
		}
	}
}

// usageAllowed implements IsVariableUsageAllowed (spec §5.8.5).
func usageAllowed(varType *gtype, def *value, u varUsage) bool {
	loc := u.typ

	if loc.kind == kindNonNull && varType.kind != kindNonNull {
		hasNonNullDefault := def != nil && def.kind != valNull
		if !hasNonNullDefault && !u.hasDefault {
			return false
		}

		return compatible(varType, loc.ofType)
	}

	return compatible(varType, loc)
}

func compatible(varType, loc *gtype) bool {
	if loc.kind == kindNonNull {
		if varType.kind != kindNonNull {
			return false
		}

		return compatible(varType.ofType, loc.ofType)
	}

	if varType.kind == kindNonNull {
		return compatible(varType.ofType, loc)
	}

	if loc.kind == kindList {
		return varType.kind == kindList && compatible(varType.ofType, loc.ofType)
	}

	if varType.kind == kindList {
		return false
	}

	return varType.named == loc.named
}

// Field selection merging (spec §5.3.2), simplified: fields sharing a
// response key in one selection set (fragments included) must select the
// same field with the same arguments when their parents may be the same
// object, and must always have compatible response shapes.

type keyedField struct {
	f      *field
	parent *namedType
	def    *fieldType
}

func (v *validator) mergeable(sel []selection, parent *namedType) {
	groups := map[string][]keyedField{}

	var order []string

	v.gather(sel, parent, groups, &order, map[string]bool{})

	for _, key := range order {
		fields := groups[key]
		for i := 1; i < len(fields); i++ {
			if msg := v.conflict(key, fields[0], fields[i]); msg != "" {
				v.report(msg, fields[0].f.pos, fields[i].f.pos)
				break
			}
		}
	}
}

func (v *validator) gather(
	sel []selection,
	parent *namedType,
	groups map[string][]keyedField,
	order *[]string,
	seen map[string]bool,
) {
	for _, s := range sel {
		switch s := s.(type) {
		case *field:
			key := s.name
			if s.alias != "" {
				key = s.alias
			}

			if _, ok := groups[key]; !ok {
				*order = append(*order, key)
			}

			groups[key] = append(groups[key], keyedField{f: s, parent: parent, def: v.s.fieldFor(parent, s.name)})
		case *inlineFragment:
			t := parent
			if s.typeCond != "" {
				if t = v.s.types[s.typeCond]; t == nil {
					continue
				}
			}

			v.gather(s.sel, t, groups, order, seen)
		case *fragmentSpread:
			f := v.frags[s.name]
			if f == nil || seen[s.name] {
				continue
			}

			seen[s.name] = true

			if t := v.s.types[f.typeCond]; t != nil {
				v.gather(f.sel, t, groups, order, seen)
			}
		}
	}
}

func (v *validator) conflict(key string, a, b keyedField) string {
	sameParent := a.parent == b.parent || a.parent.kind != kindObject || b.parent.kind != kindObject

	if sameParent {
		if a.f.name != b.f.name {
			return fmt.Sprintf("Fields %q conflict because %q and %q are different fields. "+
				"Use different aliases on the fields to fetch both if this was intentional.", key, a.f.name, b.f.name)
		}

		if printArguments(a.f.args) != printArguments(b.f.args) {
			return fmt.Sprintf("Fields %q conflict because they have differing arguments. "+
				"Use different aliases on the fields to fetch both if this was intentional.", key)
		}
	}

	if a.def != nil && b.def != nil && !sameShape(a.def.typ, b.def.typ) {
		return fmt.Sprintf("Fields %q conflict because they return conflicting types %q and %q. "+
			"Use different aliases on the fields to fetch both if this was intentional.", key, a.def.typ, b.def.typ)
	}

	return ""
}

func sameShape(a, b *gtype) bool {
	switch {
	case a.kind == kindNonNull || b.kind == kindNonNull:
		return a.kind == b.kind && sameShape(a.ofType, b.ofType)
	case a.kind == kindList || b.kind == kindList:
		return a.kind == b.kind && sameShape(a.ofType, b.ofType)
	case a.named.isLeaf() || b.named.isLeaf():
		return a.named == b.named
	}

	return true
}

func printArguments(args []*argument) string {
	sorted := slices.Clone(args)
	slices.SortFunc(sorted, func(x, y *argument) int { return strings.Compare(x.name, y.name) })

	var b strings.Builder
	for _, a := range sorted {
		b.WriteString(a.name + ":" + printLiteral(a.value) + ",")
	}

	return b.String()
}

func printLiteral(v *value) string {
	switch v.kind { //nolint:exhaustive // the remaining kinds take the common path below
	case valVariable:
		return "$" + v.raw
	case valString:
		return fmt.Sprintf("%q", v.raw)
	case valList:
		parts := make([]string, 0, len(v.list))
		for _, it := range v.list {
			parts = append(parts, printLiteral(it))
		}

		return "[" + strings.Join(parts, ",") + "]"
	case valObject:
		parts := make([]string, 0, len(v.fields))
		for _, f := range v.fields {
			parts = append(parts, f.name+":"+printLiteral(f.value))
		}

		slices.Sort(parts)

		return "{" + strings.Join(parts, ",") + "}"
	}

	return v.raw
}

// collectFieldsStatic lists the response keys of a selection set without
// evaluating directives (for the subscription rule).
func collectFieldsStatic(sel []selection, frags map[string]*fragmentDef) []string {
	var keys []string

	seen := map[string]bool{}

	var walk func([]selection, map[string]bool)

	walk = func(sel []selection, visited map[string]bool) {
		for _, s := range sel {
			switch s := s.(type) {
			case *field:
				key := s.name
				if s.alias != "" {
					key = s.alias
				}

				if !seen[key] {
					seen[key] = true
					keys = append(keys, key)
				}
			case *inlineFragment:
				walk(s.sel, visited)
			case *fragmentSpread:
				if f := frags[s.name]; f != nil && !visited[s.name] {
					visited[s.name] = true
					walk(f.sel, visited)
				}
			}
		}
	}

	walk(sel, map[string]bool{})

	return keys
}
