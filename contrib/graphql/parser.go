package graphql

import (
	"fmt"
	"slices"
)

// maxNesting bounds recursion (selections, lists, objects, types) so a
// hostile document cannot exhaust the stack.
const maxNesting = 256

type parser struct {
	lex   *lexer
	tok   token
	depth int
}

func newParser(src string, maxTokens int) (*parser, error) {
	p := &parser{lex: newLexer(src, maxTokens)}
	if err := p.advance(); err != nil {
		return nil, err
	}

	return p, nil
}

func (p *parser) advance() error {
	t, err := p.lex.next()
	if err != nil {
		return err
	}

	p.tok = t

	return nil
}

func (p *parser) unexpected() *Error {
	return syntaxError(p.tok.pos, "Unexpected "+p.tok.describe()+".")
}

func (p *parser) expect(k tokenKind) (token, error) {
	t := p.tok
	if t.kind != k {
		want := k.String()
		if k < tokName {
			want = fmt.Sprintf("%q", want)
		}

		return t, syntaxError(t.pos, fmt.Sprintf("Expected %s, found %s.", want, t.describe()))
	}

	return t, p.advance()
}

// skip consumes k when it is the current token.
func (p *parser) skip(k tokenKind) (bool, error) {
	if p.tok.kind != k {
		return false, nil
	}

	return true, p.advance()
}

func (p *parser) peekKeyword(kw string) bool { return p.tok.kind == tokName && p.tok.value == kw }

func (p *parser) expectKeyword(kw string) error {
	if !p.peekKeyword(kw) {
		return syntaxError(p.tok.pos, fmt.Sprintf("Expected %q, found %s.", kw, p.tok.describe()))
	}

	return p.advance()
}

func (p *parser) name() (string, error) {
	t, err := p.expect(tokName)
	return t.value, err
}

func (p *parser) enter() error {
	p.depth++
	if p.depth > maxNesting {
		return syntaxError(p.tok.pos, fmt.Sprintf("Document nesting exceeds %d levels.", maxNesting))
	}

	return nil
}

func (p *parser) leave() { p.depth-- }

// parseQuery parses an executable document.
func parseQuery(src string, maxTokens int) (*document, error) {
	p, err := newParser(src, maxTokens)
	if err != nil {
		return nil, err
	}

	doc := &document{}

	if p.tok.kind == tokEOF {
		return nil, p.unexpected()
	}

	for p.tok.kind != tokEOF {
		switch {
		case p.tok.kind == tokBraceL:
			op := &operation{kind: opQuery, pos: p.tok.pos}
			if op.sel, err = p.selectionSet(); err != nil {
				return nil, err
			}

			doc.operations = append(doc.operations, op)
		case p.peekKeyword("query") || p.peekKeyword("mutation") || p.peekKeyword("subscription"):
			op, err := p.operation()
			if err != nil {
				return nil, err
			}

			doc.operations = append(doc.operations, op)
		case p.peekKeyword("fragment"):
			f, err := p.fragmentDefinition()
			if err != nil {
				return nil, err
			}

			doc.fragments = append(doc.fragments, f)
		default:
			return nil, p.unexpected()
		}
	}

	return doc, nil
}

func (p *parser) operation() (*operation, error) {
	op := &operation{kind: opKind(p.tok.value), pos: p.tok.pos}
	if err := p.advance(); err != nil {
		return nil, err
	}

	var err error

	if p.tok.kind == tokName {
		op.name = p.tok.value
		if err := p.advance(); err != nil {
			return nil, err
		}
	}

	if op.vars, err = p.variableDefinitions(); err != nil {
		return nil, err
	}

	if op.directives, err = p.directives(false); err != nil {
		return nil, err
	}

	if op.sel, err = p.selectionSet(); err != nil {
		return nil, err
	}

	return op, nil
}

func (p *parser) variableDefinitions() ([]*varDef, error) {
	if ok, err := p.skip(tokParenL); !ok || err != nil {
		return nil, err
	}

	var defs []*varDef

	for {
		v := &varDef{pos: p.tok.pos}
		if _, err := p.expect(tokDollar); err != nil {
			return nil, err
		}

		var err error
		if v.name, err = p.name(); err != nil {
			return nil, err
		}

		if _, err := p.expect(tokColon); err != nil {
			return nil, err
		}

		if v.typ, err = p.typeRef(); err != nil {
			return nil, err
		}

		if ok, err := p.skip(tokEquals); err != nil {
			return nil, err
		} else if ok {
			if v.def, err = p.value(true); err != nil {
				return nil, err
			}
		}

		if v.directives, err = p.directives(true); err != nil {
			return nil, err
		}

		defs = append(defs, v)

		if ok, err := p.skip(tokParenR); err != nil || ok {
			return defs, err
		}
	}
}

func (p *parser) typeRef() (*typeRef, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()

	t := &typeRef{pos: p.tok.pos}

	if ok, err := p.skip(tokBracketL); err != nil {
		return nil, err
	} else if ok {
		if t.elem, err = p.typeRef(); err != nil {
			return nil, err
		}

		if _, err := p.expect(tokBracketR); err != nil {
			return nil, err
		}
	} else {
		var err error
		if t.name, err = p.name(); err != nil {
			return nil, err
		}
	}

	ok, err := p.skip(tokBang)
	t.nonNull = ok

	return t, err
}

func (p *parser) selectionSet() ([]selection, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()

	if _, err := p.expect(tokBraceL); err != nil {
		return nil, err
	}

	var sel []selection

	for {
		s, err := p.selection()
		if err != nil {
			return nil, err
		}

		sel = append(sel, s)

		if ok, err := p.skip(tokBraceR); err != nil || ok {
			return sel, err
		}
	}
}

func (p *parser) selection() (selection, error) {
	if p.tok.kind == tokSpread {
		pos := p.tok.pos
		if err := p.advance(); err != nil {
			return nil, err
		}

		if p.tok.kind == tokName && p.tok.value != "on" {
			fs := &fragmentSpread{name: p.tok.value, pos: pos}
			if err := p.advance(); err != nil {
				return nil, err
			}

			var err error
			fs.directives, err = p.directives(false)

			return fs, err
		}

		inl := &inlineFragment{pos: pos}

		if p.peekKeyword("on") {
			if err := p.advance(); err != nil {
				return nil, err
			}

			var err error
			if inl.typeCond, err = p.name(); err != nil {
				return nil, err
			}
		}

		var err error
		if inl.directives, err = p.directives(false); err != nil {
			return nil, err
		}

		inl.sel, err = p.selectionSet()

		return inl, err
	}

	return p.field()
}

func (p *parser) field() (*field, error) {
	f := &field{pos: p.tok.pos}

	var err error
	if f.name, err = p.name(); err != nil {
		return nil, err
	}

	if ok, err := p.skip(tokColon); err != nil {
		return nil, err
	} else if ok {
		f.alias = f.name
		if f.name, err = p.name(); err != nil {
			return nil, err
		}
	}

	if f.args, err = p.arguments(false); err != nil {
		return nil, err
	}

	if f.directives, err = p.directives(false); err != nil {
		return nil, err
	}

	if p.tok.kind == tokBraceL {
		if f.sel, err = p.selectionSet(); err != nil {
			return nil, err
		}
	}

	return f, nil
}

func (p *parser) arguments(constant bool) ([]*argument, error) {
	if ok, err := p.skip(tokParenL); !ok || err != nil {
		return nil, err
	}

	var args []*argument

	for {
		a := &argument{pos: p.tok.pos}

		var err error
		if a.name, err = p.name(); err != nil {
			return nil, err
		}

		if _, err := p.expect(tokColon); err != nil {
			return nil, err
		}

		if a.value, err = p.value(constant); err != nil {
			return nil, err
		}

		args = append(args, a)

		if ok, err := p.skip(tokParenR); err != nil || ok {
			return args, err
		}
	}
}

func (p *parser) directives(constant bool) ([]*directive, error) {
	var ds []*directive

	for p.tok.kind == tokAt {
		d := &directive{pos: p.tok.pos}
		if err := p.advance(); err != nil {
			return nil, err
		}

		var err error
		if d.name, err = p.name(); err != nil {
			return nil, err
		}

		if d.args, err = p.arguments(constant); err != nil {
			return nil, err
		}

		ds = append(ds, d)
	}

	return ds, nil
}

func (p *parser) fragmentDefinition() (*fragmentDef, error) {
	f := &fragmentDef{pos: p.tok.pos}
	if err := p.advance(); err != nil {
		return nil, err
	}

	if p.peekKeyword("on") {
		return nil, p.unexpected()
	}

	var err error
	if f.name, err = p.name(); err != nil {
		return nil, err
	}

	if err := p.expectKeyword("on"); err != nil {
		return nil, err
	}

	if f.typeCond, err = p.name(); err != nil {
		return nil, err
	}

	if f.directives, err = p.directives(false); err != nil {
		return nil, err
	}

	f.sel, err = p.selectionSet()

	return f, err
}

// value parses a Value; constant forbids variables (defaults, SDL).
func (p *parser) value(constant bool) (*value, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()

	t := p.tok
	v := &value{pos: t.pos}

	switch t.kind {
	case tokDollar:
		if constant {
			return nil, p.unexpected()
		}

		if err := p.advance(); err != nil {
			return nil, err
		}

		var err error
		v.kind = valVariable
		v.raw, err = p.name()

		return v, err
	case tokInt:
		v.kind, v.raw = valInt, t.value
	case tokFloat:
		v.kind, v.raw = valFloat, t.value
	case tokString, tokBlockString:
		v.kind, v.raw = valString, t.value
	case tokName:
		switch t.value {
		case "true", "false":
			v.kind = valBoolean
		case "null":
			v.kind = valNull
		default:
			v.kind = valEnum
		}

		v.raw = t.value
	case tokBracketL:
		return p.listValue(v, constant)
	case tokBraceL:
		return p.objectValue(v, constant)
	default:
		return nil, p.unexpected()
	}

	return v, p.advance()
}

func (p *parser) listValue(v *value, constant bool) (*value, error) {
	v.kind = valList
	v.list = []*value{}

	if err := p.advance(); err != nil {
		return nil, err
	}

	for p.tok.kind != tokBracketR {
		item, err := p.value(constant)
		if err != nil {
			return nil, err
		}

		v.list = append(v.list, item)
	}

	return v, p.advance()
}

func (p *parser) objectValue(v *value, constant bool) (*value, error) {
	v.kind = valObject
	v.fields = []*objectField{}

	if err := p.advance(); err != nil {
		return nil, err
	}

	for p.tok.kind != tokBraceR {
		f := &objectField{pos: p.tok.pos}

		var err error
		if f.name, err = p.name(); err != nil {
			return nil, err
		}

		if _, err := p.expect(tokColon); err != nil {
			return nil, err
		}

		if f.value, err = p.value(constant); err != nil {
			return nil, err
		}

		v.fields = append(v.fields, f)
	}

	return v, p.advance()
}

// SDL

// parseSchema parses a type system document.
func parseSchema(src string) (*schemaDocument, error) {
	p, err := newParser(src, 0)
	if err != nil {
		return nil, err
	}

	doc := &schemaDocument{}

	for p.tok.kind != tokEOF {
		desc, err := p.description()
		if err != nil {
			return nil, err
		}

		if p.tok.kind != tokName {
			return nil, p.unexpected()
		}

		if err := p.definition(doc, desc); err != nil {
			return nil, err
		}
	}

	return doc, nil
}

func (p *parser) description() (string, error) {
	if p.tok.kind != tokString && p.tok.kind != tokBlockString {
		return "", nil
	}

	d := p.tok.value

	return d, p.advance()
}

func (p *parser) definition(doc *schemaDocument, desc string) error {
	extend := p.peekKeyword("extend")
	if extend {
		if desc != "" {
			return p.unexpected()
		}

		if err := p.advance(); err != nil {
			return err
		}
	}

	kw := p.tok.value

	switch kw {
	case "schema":
		s, err := p.schemaDefinition(desc, extend)
		if err != nil {
			return err
		}

		if extend {
			doc.schemaExt = append(doc.schemaExt, s)
			return nil
		}

		if doc.schema != nil {
			return syntaxError(s.pos, "Must provide only one schema definition.")
		}

		doc.schema = s
	case "scalar", "type", "interface", "union", "enum", "input":
		t, err := p.typeDefinition(desc)
		if err != nil {
			return err
		}

		if extend {
			doc.extensions = append(doc.extensions, t)
		} else {
			doc.types = append(doc.types, t)
		}
	case "directive":
		if extend {
			return p.unexpected()
		}

		d, err := p.directiveDefinition(desc)
		if err != nil {
			return err
		}

		doc.directives = append(doc.directives, d)
	default:
		return p.unexpected()
	}

	return nil
}

func (p *parser) schemaDefinition(desc string, extend bool) (*schemaDef, error) {
	s := &schemaDef{desc: desc, ops: map[opKind]string{}, pos: p.tok.pos}
	if err := p.advance(); err != nil {
		return nil, err
	}

	var err error
	if s.directives, err = p.directives(true); err != nil {
		return nil, err
	}

	if extend && p.tok.kind != tokBraceL {
		return s, nil
	}

	if _, err := p.expect(tokBraceL); err != nil {
		return nil, err
	}

	for {
		kindTok := p.tok

		kind, err := p.name()
		if err != nil {
			return nil, err
		}

		k := opKind(kind)
		if k != opQuery && k != opMutation && k != opSubscription {
			return nil, syntaxError(kindTok.pos, "Unexpected "+kindTok.describe()+".")
		}

		if _, err := p.expect(tokColon); err != nil {
			return nil, err
		}

		if s.ops[k], err = p.name(); err != nil {
			return nil, err
		}

		if ok, err := p.skip(tokBraceR); err != nil || ok {
			return s, err
		}
	}
}

func (p *parser) typeDefinition(desc string) (*typeDef, error) {
	kw := p.tok.value
	t := &typeDef{desc: desc, pos: p.tok.pos}

	if err := p.advance(); err != nil {
		return nil, err
	}

	var err error
	if t.name, err = p.name(); err != nil {
		return nil, err
	}

	switch kw {
	case "scalar":
		t.kind = kindScalar
		t.directives, err = p.directives(true)

		return t, err
	case "type", "interface":
		t.kind = kindObject
		if kw == "interface" {
			t.kind = kindInterface
		}

		if t.interfaces, err = p.implements(); err != nil {
			return nil, err
		}

		if t.directives, err = p.directives(true); err != nil {
			return nil, err
		}

		t.fields, err = p.fieldsDefinition()

		return t, err
	case "union":
		t.kind = kindUnion
		if t.directives, err = p.directives(true); err != nil {
			return nil, err
		}

		t.members, err = p.unionMembers()

		return t, err
	case "enum":
		t.kind = kindEnum
		if t.directives, err = p.directives(true); err != nil {
			return nil, err
		}

		t.values, err = p.enumValues()

		return t, err
	default: // input
		t.kind = kindInputObject
		if t.directives, err = p.directives(true); err != nil {
			return nil, err
		}

		t.inputs, err = p.inputValueDefs(tokBraceL, tokBraceR, true)

		return t, err
	}
}

func (p *parser) implements() ([]string, error) {
	if !p.peekKeyword("implements") {
		return nil, nil
	}

	if err := p.advance(); err != nil {
		return nil, err
	}

	if _, err := p.skip(tokAmp); err != nil {
		return nil, err
	}

	var names []string

	for {
		n, err := p.name()
		if err != nil {
			return nil, err
		}

		names = append(names, n)

		if ok, err := p.skip(tokAmp); err != nil {
			return nil, err
		} else if !ok {
			return names, nil
		}
	}
}

func (p *parser) fieldsDefinition() ([]*fieldDef, error) {
	// The body is optional ("type Foo", or an extension adding only
	// interfaces or directives).
	if p.tok.kind != tokBraceL {
		return nil, nil
	}

	if err := p.advance(); err != nil {
		return nil, err
	}

	var fields []*fieldDef

	for p.tok.kind != tokBraceR {
		f := &fieldDef{}

		var err error
		if f.desc, err = p.description(); err != nil {
			return nil, err
		}

		f.pos = p.tok.pos
		if f.name, err = p.name(); err != nil {
			return nil, err
		}

		if p.tok.kind == tokParenL {
			if f.args, err = p.inputValueDefs(tokParenL, tokParenR, false); err != nil {
				return nil, err
			}
		}

		if _, err := p.expect(tokColon); err != nil {
			return nil, err
		}

		if f.typ, err = p.typeRef(); err != nil {
			return nil, err
		}

		if f.directives, err = p.directives(true); err != nil {
			return nil, err
		}

		fields = append(fields, f)
	}

	if len(fields) == 0 {
		return nil, p.unexpected()
	}

	return fields, p.advance()
}

func (p *parser) inputValueDefs(open, closing tokenKind, allowMissing bool) ([]*inputValueDef, error) {
	if allowMissing && p.tok.kind != open {
		return nil, nil
	}

	if _, err := p.expect(open); err != nil {
		return nil, err
	}

	var defs []*inputValueDef

	for p.tok.kind != closing {
		d := &inputValueDef{}

		var err error
		if d.desc, err = p.description(); err != nil {
			return nil, err
		}

		d.pos = p.tok.pos
		if d.name, err = p.name(); err != nil {
			return nil, err
		}

		if _, err := p.expect(tokColon); err != nil {
			return nil, err
		}

		if d.typ, err = p.typeRef(); err != nil {
			return nil, err
		}

		if ok, err := p.skip(tokEquals); err != nil {
			return nil, err
		} else if ok {
			if d.def, err = p.value(true); err != nil {
				return nil, err
			}
		}

		if d.directives, err = p.directives(true); err != nil {
			return nil, err
		}

		defs = append(defs, d)
	}

	if len(defs) == 0 {
		return nil, p.unexpected()
	}

	return defs, p.advance()
}

func (p *parser) unionMembers() ([]string, error) {
	if ok, err := p.skip(tokEquals); err != nil {
		return nil, err
	} else if !ok {
		return nil, nil
	}

	if _, err := p.skip(tokPipe); err != nil {
		return nil, err
	}

	var members []string

	for {
		n, err := p.name()
		if err != nil {
			return nil, err
		}

		members = append(members, n)

		if ok, err := p.skip(tokPipe); err != nil {
			return nil, err
		} else if !ok {
			return members, nil
		}
	}
}

func (p *parser) enumValues() ([]*enumValueDef, error) {
	if p.tok.kind != tokBraceL {
		return nil, nil
	}

	if err := p.advance(); err != nil {
		return nil, err
	}

	var values []*enumValueDef

	for p.tok.kind != tokBraceR {
		v := &enumValueDef{}

		var err error
		if v.desc, err = p.description(); err != nil {
			return nil, err
		}

		v.pos = p.tok.pos
		if v.name, err = p.name(); err != nil {
			return nil, err
		}

		if v.name == "true" || v.name == "false" || v.name == "null" {
			return nil, syntaxError(
				v.pos,
				fmt.Sprintf("Name %q is reserved and cannot be used for an enum value.", v.name),
			)
		}

		if v.directives, err = p.directives(true); err != nil {
			return nil, err
		}

		values = append(values, v)
	}

	if len(values) == 0 {
		return nil, p.unexpected()
	}

	return values, p.advance()
}

var directiveLocations = []string{
	"QUERY", "MUTATION", "SUBSCRIPTION", "FIELD", "FRAGMENT_DEFINITION", "FRAGMENT_SPREAD",
	"INLINE_FRAGMENT", "VARIABLE_DEFINITION", "SCHEMA", "SCALAR", "OBJECT", "FIELD_DEFINITION",
	"ARGUMENT_DEFINITION", "INTERFACE", "UNION", "ENUM", "ENUM_VALUE", "INPUT_OBJECT",
	"INPUT_FIELD_DEFINITION",
}

func (p *parser) directiveDefinition(desc string) (*directiveDef, error) {
	d := &directiveDef{desc: desc, pos: p.tok.pos}
	if err := p.advance(); err != nil {
		return nil, err
	}

	if _, err := p.expect(tokAt); err != nil {
		return nil, err
	}

	var err error
	if d.name, err = p.name(); err != nil {
		return nil, err
	}

	if d.args, err = p.inputValueDefs(tokParenL, tokParenR, true); err != nil {
		return nil, err
	}

	if p.peekKeyword("repeatable") {
		d.repeatable = true
		if err := p.advance(); err != nil {
			return nil, err
		}
	}

	if err := p.expectKeyword("on"); err != nil {
		return nil, err
	}

	if _, err := p.skip(tokPipe); err != nil {
		return nil, err
	}

	for {
		t := p.tok

		loc, err := p.name()
		if err != nil {
			return nil, err
		}

		if !slices.Contains(directiveLocations, loc) {
			return nil, syntaxError(t.pos, "Unexpected "+t.describe()+".")
		}

		d.locations = append(d.locations, loc)

		if ok, err := p.skip(tokPipe); err != nil {
			return nil, err
		} else if !ok {
			return d, nil
		}
	}
}
