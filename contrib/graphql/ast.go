package graphql

// The AST is internal: resolvers see decoded Go values, never syntax.

type opKind string

const (
	opQuery        opKind = "query"
	opMutation     opKind = "mutation"
	opSubscription opKind = "subscription"
)

// document is an executable document (spec §2.2).
type document struct {
	operations []*operation
	fragments  []*fragmentDef
}

type operation struct {
	kind       opKind
	name       string
	vars       []*varDef
	directives []*directive
	sel        []selection
	pos        Location
}

type varDef struct {
	name       string
	typ        *typeRef
	def        *value
	directives []*directive
	pos        Location
}

// typeRef is a type in source form: a named type, a list or a non-null
// wrapper.
type typeRef struct {
	name    string
	elem    *typeRef
	nonNull bool
	pos     Location
}

func (t *typeRef) String() string {
	s := t.name
	if t.elem != nil {
		s = "[" + t.elem.String() + "]"
	}

	if t.nonNull {
		s += "!"
	}

	return s
}

type selection interface{ location() Location }

type field struct {
	alias      string
	name       string
	args       []*argument
	directives []*directive
	sel        []selection
	pos        Location
}

func (f *field) location() Location { return f.pos }

type fragmentSpread struct {
	name       string
	directives []*directive
	pos        Location
}

func (f *fragmentSpread) location() Location { return f.pos }

type inlineFragment struct {
	typeCond   string
	directives []*directive
	sel        []selection
	pos        Location
}

func (f *inlineFragment) location() Location { return f.pos }

type fragmentDef struct {
	name       string
	typeCond   string
	directives []*directive
	sel        []selection
	pos        Location
}

type argument struct {
	name  string
	value *value
	pos   Location
}

type directive struct {
	name string
	args []*argument
	pos  Location
}

type valueKind uint8

const (
	valVariable valueKind = iota
	valInt
	valFloat
	valString
	valBoolean
	valNull
	valEnum
	valList
	valObject
)

type value struct {
	kind   valueKind
	raw    string // variable name, number text, decoded string, enum name, "true"/"false"
	list   []*value
	fields []*objectField
	pos    Location
}

type objectField struct {
	name  string
	value *value
	pos   Location
}

// Type system (SDL) definitions (spec §3).

type typeKind string

const (
	kindScalar      typeKind = "SCALAR"
	kindObject      typeKind = "OBJECT"
	kindInterface   typeKind = "INTERFACE"
	kindUnion       typeKind = "UNION"
	kindEnum        typeKind = "ENUM"
	kindInputObject typeKind = "INPUT_OBJECT"
	kindList        typeKind = "LIST"
	kindNonNull     typeKind = "NON_NULL"
)

type schemaDocument struct {
	schema     *schemaDef
	types      []*typeDef
	directives []*directiveDef
	extensions []*typeDef // "extend type …"; schemaExt merges into schema
	schemaExt  []*schemaDef
}

type schemaDef struct {
	desc       string
	ops        map[opKind]string
	directives []*directive
	pos        Location
}

type typeDef struct {
	kind       typeKind
	name       string
	desc       string
	interfaces []string
	fields     []*fieldDef
	members    []string
	values     []*enumValueDef
	inputs     []*inputValueDef
	directives []*directive
	pos        Location
}

type fieldDef struct {
	name       string
	desc       string
	args       []*inputValueDef
	typ        *typeRef
	directives []*directive
	pos        Location
}

type inputValueDef struct {
	name       string
	desc       string
	typ        *typeRef
	def        *value
	directives []*directive
	pos        Location
}

type enumValueDef struct {
	name       string
	desc       string
	directives []*directive
	pos        Location
}

type directiveDef struct {
	name       string
	desc       string
	args       []*inputValueDef
	repeatable bool
	locations  []string
	pos        Location
}
