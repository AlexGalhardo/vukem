package graphql

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"
)

// Schema is an executable GraphQL schema: SDL types with resolvers bound.
// It is immutable after New and safe for concurrent use.
type Schema struct {
	types      map[string]*namedType
	typeNames  []string // sorted, for introspection
	directives map[string]*directiveType
	dirNames   []string
	query      *namedType
	mutation   *namedType
	subscr     *namedType
	desc       string

	limits Limits
	// maxMetaFields bounds the introspection fields one operation
	// resolves (maxIntrospectionFields; lowered in tests).
	maxMetaFields int64
	debug         bool
	log           *slog.Logger
	concurrent    bool

	metaTypename *fieldType
	metaSchema   *fieldType
	metaType     *fieldType

	goTypes map[reflect.Type]string // Object[T] bindings for abstract types
	props   sync.Map                // reflect.Type → map[string][]int (default resolver)
}

// namedType is a schema type (scalar, object, interface, union, enum,
// input object).
type namedType struct {
	kind        typeKind
	name        string
	desc        string
	specifiedBy string

	fields     []*fieldType
	fieldMap   map[string]*fieldType
	interfaces []*namedType
	possible   []*namedType // objects implementing an interface / union members
	enumValues []*enumValue
	enumMap    map[string]*enumValue
	inputs     []*inputValue
	inputMap   map[string]*inputValue

	scalar      *ScalarFuncs
	resolveType func(v any) string
}

func (t *namedType) isLeaf() bool      { return t.kind == kindScalar || t.kind == kindEnum }
func (t *namedType) isAbstract() bool  { return t.kind == kindInterface || t.kind == kindUnion }
func (t *namedType) isComposite() bool { return t.kind == kindObject || t.isAbstract() }
func (t *namedType) isInput() bool {
	return t.kind == kindScalar || t.kind == kindEnum || t.kind == kindInputObject
}

func (t *namedType) isOutput() bool { return t.kind != kindInputObject }

// possibleType reports whether object o can be the runtime type of t.
func (t *namedType) possibleType(o *namedType) bool {
	if t == o {
		return true
	}

	return slices.Contains(t.possible, o)
}

// gtype is a type reference: a named type, a list or a non-null wrapper.
type gtype struct {
	kind   typeKind // kindList, kindNonNull, or the named type's kind
	ofType *gtype
	named  *namedType
}

func (t *gtype) String() string {
	switch t.kind { //nolint:exhaustive // the remaining kinds take the common path below
	case kindList:
		return "[" + t.ofType.String() + "]"
	case kindNonNull:
		return t.ofType.String() + "!"
	}

	return t.named.name
}

// base returns the innermost named type.
func (t *gtype) base() *namedType {
	for t.named == nil {
		t = t.ofType
	}

	return t.named
}

type fieldType struct {
	name       string
	desc       string
	args       []*inputValue
	argMap     map[string]*inputValue
	typ        *gtype
	deprecated *string
	parent     *namedType

	resolve   resolveFn
	subscribe subscribeFn // source stream of a subscription root field
	async     bool        // has a user resolver: may run concurrently with siblings
	cost      int
}

type resolveFn func(ctx context.Context, parent any, args map[string]any) (any, error)

// subscribeFn creates a source event stream (spec §6.2.3.1); next blocks
// for the next event and reports false when the stream or ctx ends.
type subscribeFn func(ctx context.Context, args map[string]any) (next func(context.Context) (any, bool), err error)

type inputValue struct {
	name       string
	desc       string
	typ        *gtype
	defLiteral *value
	defValue   any
	hasDefault bool
	deprecated *string
}

type enumValue struct {
	name       string
	desc       string
	deprecated *string
}

type directiveType struct {
	name       string
	desc       string
	args       []*inputValue
	argMap     map[string]*inputValue
	locations  []string
	repeatable bool
}

// ScalarFuncs converts a custom scalar between Go and the wire. Nil
// functions pass values through unchanged.
type ScalarFuncs struct {
	// Serialize turns a resolver value into a JSON-encodable value.
	Serialize func(v any) (any, error)
	// Parse turns an input value (from variables or literals, already
	// decoded to nil, bool, int64, float64, string, []any or
	// map[string]any) into the value resolvers receive.
	Parse func(v any) (any, error)
}

// Limits bound the cost of a single operation. Zero values take the
// defaults; negative values disable a limit.
type Limits struct {
	// MaxDepth is the deepest field nesting (default 12). Introspection
	// selections have their own depth bound (16) and a cap on resolved
	// fields (200000); their aliases and complexity count here.
	MaxDepth int
	// MaxComplexity is the summed field cost (default 1000). Each field
	// costs 1 (or its Cost option); a field with a first/last/limit
	// argument multiplies the cost of its selection by that value.
	MaxComplexity int
	// MaxAliases is the number of aliased fields (default 30).
	MaxAliases int
	// MaxTokens is the lexer token budget of a document (default 10000).
	MaxTokens int
}

// Default limits.
const (
	DefaultMaxDepth      = 12
	DefaultMaxComplexity = 1000
	DefaultMaxAliases    = 30
	DefaultMaxTokens     = 10000
)

func (l Limits) withDefaults() Limits {
	def := func(v, d int) int {
		switch {
		case v == 0:
			return d
		case v < 0:
			return 0
		}

		return v
	}

	return Limits{
		MaxDepth:      def(l.MaxDepth, DefaultMaxDepth),
		MaxComplexity: def(l.MaxComplexity, DefaultMaxComplexity),
		MaxAliases:    def(l.MaxAliases, DefaultMaxAliases),
		MaxTokens:     def(l.MaxTokens, DefaultMaxTokens),
	}
}

// Option configures New: resolver bindings, scalars, limits and more.
type Option interface{ apply(b *builder) error }

type optionFunc func(b *builder) error

func (f optionFunc) apply(b *builder) error { return f(b) }

type builder struct {
	s        *Schema
	bindings []binding
	limits   Limits
}

type binding struct {
	typeName, field string
	resolve         resolveFn
	subscribe       subscribeFn
	argNames        []string // json names of the Go args struct, checked against the SDL
	cost            int
	// parent and out are the Go types of the parent and the result, so New
	// can check that default-resolved fields exist on them.
	parent, out reflect.Type
}

// WithLimits sets the operation limits (see Limits for defaults).
func WithLimits(l Limits) Option {
	return optionFunc(func(b *builder) error { b.limits = l; return nil })
}

// WithDebug exposes the text of internal (5xx) resolver errors in
// responses. Never enable it in production.
func WithDebug(debug bool) Option {
	return optionFunc(func(b *builder) error { b.s.debug = debug; return nil })
}

// WithLogger logs internal resolver errors and panics (default: discard).
func WithLogger(l *slog.Logger) Option {
	return optionFunc(func(b *builder) error { b.s.log = l; return nil })
}

// WithoutConcurrency resolves every field serially, for resolvers that
// share state that is not safe for concurrent use (one *sql.Tx, say).
// Loaders then batch only keys requested before the first load waits.
func WithoutConcurrency() Option {
	return optionFunc(func(b *builder) error { b.s.concurrent = false; return nil })
}

// Scalar binds a custom scalar declared in the SDL ("scalar DateTime").
// Unbound custom scalars pass values through unchanged.
func Scalar(name string, funcs ScalarFuncs) Option {
	return optionFunc(func(b *builder) error {
		t := b.s.types[name]
		if t == nil || t.kind != kindScalar {
			return fmt.Errorf("graphql: Scalar(%q): no such scalar in the schema", name)
		}

		if builtinScalar(name) {
			return fmt.Errorf("graphql: Scalar(%q): built-in scalars cannot be replaced", name)
		}

		t.scalar = &funcs

		return nil
	})
}

// Object maps Go type T (and *T) to an object type, so values of T
// returned from interface or union fields resolve to it.
func Object[T any](typeName string) Option {
	return optionFunc(func(b *builder) error {
		t := b.s.types[typeName]
		if t == nil || t.kind != kindObject {
			return fmt.Errorf("graphql: Object(%q): no such object type in the schema", typeName)
		}

		rt := reflect.TypeFor[T]()
		for rt.Kind() == reflect.Pointer {
			rt = rt.Elem()
		}

		b.s.goTypes[rt] = typeName

		return nil
	})
}

// ResolveType sets how values of an interface or union map to an object
// type name. Without it, values resolve through Typenamer, Object[T]
// bindings or a "__typename" map key.
func ResolveType(abstractType string, fn func(v any) string) Option {
	return optionFunc(func(b *builder) error {
		t := b.s.types[abstractType]
		if t == nil || !t.isAbstract() {
			return fmt.Errorf("graphql: ResolveType(%q): no such interface or union in the schema", abstractType)
		}

		t.resolveType = fn

		return nil
	})
}

// Typenamer is implemented by values that know their GraphQL object type.
type Typenamer interface {
	GraphQLTypename() string
}

// FieldOption tunes a bound field.
type FieldOption func(*binding)

// Cost sets the complexity cost of a field (default 1).
func Cost(n int) FieldOption { return func(b *binding) { b.cost = n } }

// New builds a schema from SDL and options. It fails on SDL errors,
// unknown types, invalid implementations, bindings that do not match the
// SDL and root fields without a resolver.
func New(sdl string, opts ...Option) (*Schema, error) {
	b := &builder{s: &Schema{
		types: map[string]*namedType{}, directives: map[string]*directiveType{},
		goTypes: map[reflect.Type]string{}, concurrent: true, log: slog.New(slog.DiscardHandler),
	}}

	if err := b.load(sdl); err != nil {
		return nil, err
	}

	for _, o := range opts {
		if err := o.apply(b); err != nil {
			return nil, err
		}
	}

	if err := b.bind(); err != nil {
		return nil, err
	}

	b.s.limits = b.limits.withDefaults()
	b.s.maxMetaFields = maxIntrospectionFields

	return b.s, nil
}

func builtinScalar(name string) bool {
	switch name {
	case "Int", "Float", "String", "Boolean", "ID":
		return true
	}

	return false
}

const builtinSDL = `
"The ` + "`Int`" + ` scalar type represents non-fractional signed whole numeric values. Int can represent values between -(2^31) and 2^31 - 1."
scalar Int
"The ` + "`Float`" + ` scalar type represents signed double-precision fractional values as specified by [IEEE 754](https://en.wikipedia.org/wiki/IEEE_floating_point)."
scalar Float
"The ` + "`String`" + ` scalar type represents textual data, represented as UTF-8 character sequences."
scalar String
"The ` + "`Boolean`" + ` scalar type represents ` + "`true` or `false`" + `."
scalar Boolean
"The ` + "`ID`" + ` scalar type represents a unique identifier, often used to refetch an object or as key for a cache."
scalar ID

"Directs the executor to include this field or fragment only when the ` + "`if`" + ` argument is true."
directive @include("Included when true." if: Boolean!) on FIELD | FRAGMENT_SPREAD | INLINE_FRAGMENT
"Directs the executor to skip this field or fragment when the ` + "`if`" + ` argument is true."
directive @skip("Skipped when true." if: Boolean!) on FIELD | FRAGMENT_SPREAD | INLINE_FRAGMENT
"Marks an element of a GraphQL schema as no longer supported."
directive @deprecated(
  "Explains why this element was deprecated."
  reason: String = "No longer supported"
) on FIELD_DEFINITION | ARGUMENT_DEFINITION | INPUT_FIELD_DEFINITION | ENUM_VALUE
"Exposes a URL that specifies the behavior of this scalar."
directive @specifiedBy("The URL that specifies the behavior of this scalar." url: String!) on SCALAR
`

func (b *builder) load(sdl string) error {
	builtins, err := parseSchema(builtinSDL + introspectionSDL)
	if err != nil {
		return fmt.Errorf("graphql: built-in SDL: %w", err)
	}

	user, err := parseSchema(sdl)
	if err != nil {
		return fmt.Errorf("graphql: parse SDL: %w", err)
	}

	for _, d := range user.types {
		if strings.HasPrefix(d.name, "__") {
			return fmt.Errorf(
				"graphql: type %q: names starting with \"__\" are reserved (%d:%d)",
				d.name,
				d.pos.Line,
				d.pos.Column,
			)
		}

		if builtinScalar(d.name) {
			return fmt.Errorf("graphql: type %q is built in and cannot be redefined", d.name)
		}
	}

	defs := slices.Concat(builtins.types, user.types)

	for _, d := range defs {
		if b.s.types[d.name] != nil {
			return fmt.Errorf("graphql: type %q is defined more than once (%d:%d)", d.name, d.pos.Line, d.pos.Column)
		}

		b.s.types[d.name] = &namedType{kind: d.kind, name: d.name, desc: d.desc}
	}

	for _, ext := range user.extensions {
		if err := b.extend(defs, ext); err != nil {
			return err
		}
	}

	for _, d := range defs {
		if err := b.fill(d); err != nil {
			return err
		}
	}

	for _, d := range slices.Concat(builtins.directives, user.directives) {
		if err := b.directive(d); err != nil {
			return err
		}
	}

	if err := b.roots(user); err != nil {
		return err
	}

	b.s.typeNames = slices.Sorted(func(yield func(string) bool) {
		for n := range b.s.types {
			if !yield(n) {
				return
			}
		}
	})

	for _, t := range b.s.types {
		if err := b.check(t); err != nil {
			return err
		}
	}

	return nil
}

// extend merges an "extend …" definition into the matching definition.
func (b *builder) extend(defs []*typeDef, ext *typeDef) error {
	i := slices.IndexFunc(defs, func(d *typeDef) bool { return d.name == ext.name })
	if i < 0 {
		return fmt.Errorf("graphql: cannot extend unknown type %q (%d:%d)", ext.name, ext.pos.Line, ext.pos.Column)
	}

	d := defs[i]
	if d.kind != ext.kind {
		return fmt.Errorf("graphql: cannot extend %s %q as %s", d.kind, d.name, ext.kind)
	}

	d.fields = append(d.fields, ext.fields...)
	d.interfaces = append(d.interfaces, ext.interfaces...)
	d.members = append(d.members, ext.members...)
	d.values = append(d.values, ext.values...)
	d.inputs = append(d.inputs, ext.inputs...)
	d.directives = append(d.directives, ext.directives...)

	return nil
}

func (b *builder) ref(r *typeRef) (*gtype, error) {
	switch {
	case r.elem != nil:
		of, err := b.ref(r.elem)
		if err != nil {
			return nil, err
		}

		t := &gtype{kind: kindList, ofType: of}
		if r.nonNull {
			t = &gtype{kind: kindNonNull, ofType: t}
		}

		return t, nil
	default:
		n := b.s.types[r.name]
		if n == nil {
			return nil, fmt.Errorf("graphql: unknown type %q (%d:%d)", r.name, r.pos.Line, r.pos.Column)
		}

		t := &gtype{kind: n.kind, named: n}
		if r.nonNull {
			t = &gtype{kind: kindNonNull, ofType: t}
		}

		return t, nil
	}
}

func deprecation(ds []*directive) *string {
	for _, d := range ds {
		if d.name != "deprecated" {
			continue
		}

		reason := "No longer supported"

		for _, a := range d.args {
			if a.name == "reason" && a.value.kind == valString {
				reason = a.value.raw
			}
		}

		return &reason
	}

	return nil
}

func (b *builder) inputValues(owner string, defs []*inputValueDef) ([]*inputValue, map[string]*inputValue, error) {
	out := make([]*inputValue, 0, len(defs))
	m := make(map[string]*inputValue, len(defs))

	for _, d := range defs {
		if m[d.name] != nil {
			return nil, nil, fmt.Errorf("graphql: %s: duplicate argument or field %q", owner, d.name)
		}

		t, err := b.ref(d.typ)
		if err != nil {
			return nil, nil, fmt.Errorf("graphql: %s.%s: %w", owner, d.name, err)
		}

		iv := &inputValue{
			name: d.name, desc: d.desc, typ: t, defLiteral: d.def, hasDefault: d.def != nil,
			deprecated: deprecation(d.directives),
		}
		out = append(out, iv)
		m[d.name] = iv
	}

	return out, m, nil
}

//nolint:gocognit // one pass over a type definition kind by kind; checks share state
func (b *builder) fill(d *typeDef) error {
	t := b.s.types[d.name]

	for _, dir := range d.directives {
		if dir.name == "specifiedBy" {
			for _, a := range dir.args {
				if a.name == "url" {
					t.specifiedBy = a.value.raw
				}
			}
		}
	}

	switch d.kind { //nolint:exhaustive // the remaining kinds take the common path below
	case kindObject, kindInterface:
		t.fieldMap = map[string]*fieldType{}

		for _, f := range d.fields {
			if t.fieldMap[f.name] != nil {
				return fmt.Errorf("graphql: %s.%s is defined more than once", d.name, f.name)
			}

			typ, err := b.ref(f.typ)
			if err != nil {
				return fmt.Errorf("graphql: %s.%s: %w", d.name, f.name, err)
			}

			args, argMap, err := b.inputValues(d.name+"."+f.name, f.args)
			if err != nil {
				return err
			}

			ft := &fieldType{
				name: f.name, desc: f.desc, typ: typ, args: args, argMap: argMap,
				deprecated: deprecation(f.directives), parent: t, cost: 1,
			}
			t.fields = append(t.fields, ft)
			t.fieldMap[f.name] = ft
		}

		for _, name := range d.interfaces {
			it := b.s.types[name]
			if it == nil || it.kind != kindInterface {
				return fmt.Errorf("graphql: %s implements %q, which is not an interface", d.name, name)
			}

			if slices.Contains(t.interfaces, it) {
				return fmt.Errorf("graphql: %s implements %q more than once", d.name, name)
			}

			t.interfaces = append(t.interfaces, it)

			if d.kind == kindObject {
				it.possible = append(it.possible, t)
			}
		}
	case kindUnion:
		for _, name := range d.members {
			mt := b.s.types[name]
			if mt == nil || mt.kind != kindObject {
				return fmt.Errorf("graphql: union %s member %q is not an object type", d.name, name)
			}

			if slices.Contains(t.possible, mt) {
				return fmt.Errorf("graphql: union %s lists %q more than once", d.name, name)
			}

			t.possible = append(t.possible, mt)
		}
	case kindEnum:
		t.enumMap = map[string]*enumValue{}

		for _, v := range d.values {
			if t.enumMap[v.name] != nil {
				return fmt.Errorf("graphql: enum %s value %q is defined more than once", d.name, v.name)
			}

			ev := &enumValue{name: v.name, desc: v.desc, deprecated: deprecation(v.directives)}
			t.enumValues = append(t.enumValues, ev)
			t.enumMap[v.name] = ev
		}
	case kindInputObject:
		var err error
		if t.inputs, t.inputMap, err = b.inputValues(d.name, d.inputs); err != nil {
			return err
		}
	}

	return nil
}

func (b *builder) directive(d *directiveDef) error {
	if b.s.directives[d.name] != nil {
		return fmt.Errorf("graphql: directive @%s is defined more than once", d.name)
	}

	args, argMap, err := b.inputValues("@"+d.name, d.args)
	if err != nil {
		return err
	}

	b.s.directives[d.name] = &directiveType{
		name: d.name, desc: d.desc, args: args, argMap: argMap, locations: d.locations, repeatable: d.repeatable,
	}
	b.s.dirNames = append(b.s.dirNames, d.name)

	return nil
}

func (b *builder) roots(doc *schemaDocument) error {
	ops := map[opKind]string{}
	if doc.schema != nil {
		ops = doc.schema.ops
		b.s.desc = doc.schema.desc
	} else {
		for _, k := range []opKind{opQuery, opMutation, opSubscription} {
			name := strings.ToUpper(string(k[:1])) + string(k[1:])
			if b.s.types[name] != nil {
				ops[k] = name
			}
		}
	}

	for _, ext := range doc.schemaExt {
		for k, v := range ext.ops {
			if _, dup := ops[k]; dup {
				return fmt.Errorf("graphql: schema extension redefines the %s type", k)
			}

			ops[k] = v
		}
	}

	root := func(k opKind) (*namedType, error) {
		name, ok := ops[k]
		if !ok {
			return nil, nil //nolint:nilnil // an absent root is not an error
		}

		t := b.s.types[name]
		if t == nil || t.kind != kindObject {
			return nil, fmt.Errorf("graphql: %s root type %q must be an object type", k, name)
		}

		return t, nil
	}

	var err error
	if b.s.query, err = root(opQuery); err != nil {
		return err
	}

	if b.s.query == nil {
		return errors.New("graphql: the schema has no query root type")
	}

	if b.s.mutation, err = root(opMutation); err != nil {
		return err
	}

	b.s.subscr, err = root(opSubscription)

	return err
}

// check enforces the type validation rules of spec §3 that the parser
// cannot: field and argument kinds, interface implementations, defaults.
func (b *builder) check(t *namedType) error {
	switch t.kind { //nolint:exhaustive // the remaining kinds take the common path below
	case kindObject, kindInterface:
		if len(t.fields) == 0 {
			return fmt.Errorf("graphql: %s %s must define at least one field", strings.ToLower(string(t.kind)), t.name)
		}

		for _, f := range t.fields {
			if strings.HasPrefix(f.name, "__") && !strings.HasPrefix(t.name, "__") {
				return fmt.Errorf("graphql: %s.%s: names starting with \"__\" are reserved", t.name, f.name)
			}

			if !f.typ.base().isOutput() {
				return fmt.Errorf("graphql: %s.%s: %s is not an output type", t.name, f.name, f.typ)
			}

			if err := b.checkInputs(t.name+"."+f.name, f.args); err != nil {
				return err
			}
		}

		for _, it := range t.interfaces {
			if err := implements(t, it); err != nil {
				return err
			}
		}
	case kindUnion:
		if len(t.possible) == 0 {
			return fmt.Errorf("graphql: union %s must have at least one member", t.name)
		}
	case kindEnum:
		if len(t.enumValues) == 0 {
			return fmt.Errorf("graphql: enum %s must define at least one value", t.name)
		}
	case kindInputObject:
		if len(t.inputs) == 0 {
			return fmt.Errorf("graphql: input %s must define at least one field", t.name)
		}

		if err := b.checkInputs(t.name, t.inputs); err != nil {
			return err
		}
	}

	return nil
}

func (b *builder) checkInputs(owner string, ivs []*inputValue) error {
	for _, iv := range ivs {
		if !iv.typ.base().isInput() {
			return fmt.Errorf("graphql: %s.%s: %s is not an input type", owner, iv.name, iv.typ)
		}

		if iv.defLiteral != nil {
			v, err := coerceLiteral(iv.defLiteral, iv.typ, nil)
			if err != nil {
				return fmt.Errorf("graphql: %s.%s: invalid default value: %w", owner, iv.name, err)
			}

			iv.defValue = v
		}
	}

	return nil
}

// implements checks spec §3.6.1 "IsValidImplementation".
func implements(t, it *namedType) error {
	for _, want := range it.interfaces {
		if !slices.Contains(t.interfaces, want) {
			return fmt.Errorf("graphql: %s must implement %s because %s does", t.name, want.name, it.name)
		}
	}

	for _, iff := range it.fields {
		f := t.fieldMap[iff.name]
		if f == nil {
			return fmt.Errorf("graphql: %s.%s required by interface %s is missing", t.name, iff.name, it.name)
		}

		if !isSubType(f.typ, iff.typ) {
			return fmt.Errorf("graphql: %s.%s has type %s, interface %s requires %s (or a subtype)",
				t.name, f.name, f.typ, it.name, iff.typ)
		}

		for _, ia := range iff.args {
			a := f.argMap[ia.name]
			if a == nil || a.typ.String() != ia.typ.String() {
				return fmt.Errorf("graphql: %s.%s must accept argument %s: %s from interface %s",
					t.name, f.name, ia.name, ia.typ, it.name)
			}
		}

		for _, a := range f.args {
			if iff.argMap[a.name] == nil && a.typ.kind == kindNonNull && !a.hasDefault {
				return fmt.Errorf("graphql: %s.%s argument %s must be optional: it is not in interface %s",
					t.name, f.name, a.name, it.name)
			}
		}
	}

	return nil
}

// isSubType reports whether sub can be used where super is expected
// (covariant output types, spec IsValidImplementationFieldType).
func isSubType(sub, super *gtype) bool {
	if sub.kind == kindNonNull {
		if super.kind == kindNonNull {
			return isSubType(sub.ofType, super.ofType)
		}

		return isSubType(sub.ofType, super)
	}

	if super.kind == kindNonNull {
		return false
	}

	if sub.kind == kindList || super.kind == kindList {
		return sub.kind == kindList && super.kind == kindList && isSubType(sub.ofType, super.ofType)
	}

	return sub.named == super.named || (super.named.isAbstract() && super.named.possibleType(sub.named)) ||
		(super.named.kind == kindInterface && slices.Contains(sub.named.interfaces, super.named))
}

// bind attaches resolvers; root fields must all have one.
func (b *builder) bind() error {
	seen := map[string]bool{}

	for _, bd := range b.bindings {
		key := bd.typeName + "." + bd.field
		if seen[key] {
			return fmt.Errorf("graphql: %s is bound more than once", key)
		}

		seen[key] = true

		t := b.s.types[bd.typeName]
		if t == nil || t.kind != kindObject {
			return fmt.Errorf("graphql: binding %s: no such object type in the schema", key)
		}

		f := t.fieldMap[bd.field]
		if f == nil {
			return fmt.Errorf("graphql: binding %s: no such field in the schema", key)
		}

		for _, a := range bd.argNames {
			if f.argMap[a] == nil {
				return fmt.Errorf("graphql: binding %s: Go argument %q is not an argument of the field", key, a)
			}
		}

		f.resolve = bd.resolve
		f.subscribe = bd.subscribe
		f.async = true
		f.cost = cmp.Or(bd.cost, 1)
	}

	for _, root := range []*namedType{b.s.query, b.s.mutation} {
		if root == nil {
			continue
		}

		for _, f := range root.fields {
			if f.resolve == nil {
				return fmt.Errorf("graphql: %s.%s has no resolver (bind it with graphql.Query/Mutation/Field)",
					root.name, f.name)
			}
		}
	}

	if root := b.s.subscr; root != nil {
		for _, f := range root.fields {
			if f.subscribe == nil {
				return fmt.Errorf("graphql: %s.%s has no source stream (bind it with graphql.Subscription)",
					root.name, f.name)
			}
		}
	}

	if err := b.checkDefaultFields(); err != nil {
		return err
	}

	b.bindIntrospection()

	return nil
}

// checkDefaultFields walks from the Go types the bindings declare (parents,
// results, Object[T]) and fails when an object field without a resolver
// has no Go field the default resolver could read, so a typo or a
// json:"-" field fails at boot instead of on the first query. Maps,
// interfaces and other non-struct Go types are not checked.
//
//nolint:gocognit // default-value checks per argument and input field in one walk
func (b *builder) checkDefaultFields() error {
	seen := map[*namedType]map[reflect.Type]bool{}

	var walk func(t *namedType, gt reflect.Type) error

	walk = func(t *namedType, gt reflect.Type) error {
		for gt.Kind() == reflect.Pointer || gt.Kind() == reflect.Slice || gt.Kind() == reflect.Array {
			gt = gt.Elem()
		}

		if t == nil || t.kind != kindObject || gt.Kind() != reflect.Struct || seen[t][gt] {
			return nil
		}

		if seen[t] == nil {
			seen[t] = map[reflect.Type]bool{}
		}

		seen[t][gt] = true
		index := b.s.fieldIndex(gt)

		for _, f := range t.fields {
			if f.resolve != nil {
				continue
			}

			idx, ok := index[f.name]
			if !ok {
				idx, ok = index[foldPrefix+strings.ToLower(f.name)]
			}

			if !ok {
				return fmt.Errorf("graphql: %s.%s has no resolver and Go type %s has no field for it "+
					"(bind it with graphql.Field or add a json tag)", t.name, f.name, gt)
			}

			if err := walk(f.typ.base(), gt.FieldByIndex(idx).Type); err != nil {
				return err
			}
		}

		return nil
	}

	for _, bd := range b.bindings {
		t := b.s.types[bd.typeName]

		if bd.parent != nil {
			if err := walk(t, bd.parent); err != nil {
				return err
			}
		}

		if bd.out != nil {
			if err := walk(t.fieldMap[bd.field].typ.base(), bd.out); err != nil {
				return err
			}
		}
	}

	for _, rt := range slices.SortedFunc(maps.Keys(b.s.goTypes), func(a, c reflect.Type) int {
		return strings.Compare(a.String(), c.String())
	}) {
		if err := walk(b.s.types[b.s.goTypes[rt]], rt); err != nil {
			return err
		}
	}

	return nil
}

// rootName returns the configured root type name for kind, or the
// conventional one when the SDL does not define it yet (bindings may be
// applied before the check in bind reports the problem).
func (b *builder) rootName(k opKind) string {
	switch k {
	case opMutation:
		if b.s.mutation != nil {
			return b.s.mutation.name
		}

		return "Mutation"
	case opSubscription:
		if b.s.subscr != nil {
			return b.s.subscr.name
		}

		return "Subscription"
	default:
		return b.s.query.name
	}
}
