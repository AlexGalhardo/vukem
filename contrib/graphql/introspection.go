package graphql

import (
	"context"
	"strconv"
	"strings"
)

// introspectionSDL declares the introspection types (spec §4.5,
// including the 2021 additions description, specifiedByURL, isRepeatable
// and deprecated arguments / input fields).
const introspectionSDL = `
type __Schema {
  description: String
  types: [__Type!]!
  queryType: __Type!
  mutationType: __Type
  subscriptionType: __Type
  directives: [__Directive!]!
}

type __Type {
  kind: __TypeKind!
  name: String
  description: String
  specifiedByURL: String
  fields(includeDeprecated: Boolean = false): [__Field!]
  interfaces: [__Type!]
  possibleTypes: [__Type!]
  enumValues(includeDeprecated: Boolean = false): [__EnumValue!]
  inputFields(includeDeprecated: Boolean = false): [__InputValue!]
  ofType: __Type
}

enum __TypeKind { SCALAR OBJECT INTERFACE UNION ENUM INPUT_OBJECT LIST NON_NULL }

type __Field {
  name: String!
  description: String
  args(includeDeprecated: Boolean = false): [__InputValue!]!
  type: __Type!
  isDeprecated: Boolean!
  deprecationReason: String
}

type __InputValue {
  name: String!
  description: String
  type: __Type!
  defaultValue: String
  isDeprecated: Boolean!
  deprecationReason: String
}

type __EnumValue {
  name: String!
  description: String
  isDeprecated: Boolean!
  deprecationReason: String
}

type __Directive {
  name: String!
  description: String
  locations: [__DirectiveLocation!]!
  args(includeDeprecated: Boolean = false): [__InputValue!]!
  isRepeatable: Boolean!
}

enum __DirectiveLocation {
  QUERY MUTATION SUBSCRIPTION FIELD FRAGMENT_DEFINITION FRAGMENT_SPREAD INLINE_FRAGMENT VARIABLE_DEFINITION
  SCHEMA SCALAR OBJECT FIELD_DEFINITION ARGUMENT_DEFINITION INTERFACE UNION ENUM ENUM_VALUE INPUT_OBJECT
  INPUT_FIELD_DEFINITION
}
`

// fieldFor returns the definition of field name on parent, including the
// meta fields __typename, __schema and __type.
func (s *Schema) fieldFor(parent *namedType, name string) *fieldType {
	if strings.HasPrefix(name, "__") {
		switch {
		case name == "__typename":
			return s.metaTypename
		case parent == s.query && name == "__schema":
			return s.metaSchema
		case parent == s.query && name == "__type":
			return s.metaType
		}
	}

	if parent.fieldMap == nil {
		return nil
	}

	return parent.fieldMap[name]
}

func named(n *namedType) *gtype { return &gtype{kind: n.kind, named: n} }

func nonNull(t *gtype) *gtype { return &gtype{kind: kindNonNull, ofType: t} }

func includeDeprecated(args map[string]any) bool {
	b, _ := args["includeDeprecated"].(bool)
	return b
}

func deprecated(d *string) (bool, any) {
	if d == nil {
		return false, nil
	}

	return true, *d
}

// bindIntrospection wires the introspection types to the schema model.
func (b *builder) bindIntrospection() {
	s := b.s
	str := named(s.types["String"])

	s.metaTypename = &fieldType{name: "__typename", typ: nonNull(str), cost: 0}
	s.metaSchema = &fieldType{
		name: "__schema", typ: nonNull(named(s.types["__Schema"])),
		resolve: func(context.Context, any, map[string]any) (any, error) { return s, nil },
	}

	nameArg := &inputValue{name: "name", typ: nonNull(str)}
	s.metaType = &fieldType{
		name: "__type", typ: named(s.types["__Type"]),
		args: []*inputValue{nameArg}, argMap: map[string]*inputValue{"name": nameArg},
		resolve: func(_ context.Context, _ any, args map[string]any) (any, error) {
			name, _ := args["name"].(string)
			if t := s.types[name]; t != nil {
				return named(t), nil
			}

			return nil, nil
		},
	}

	set := func(fieldName string, fn resolveFn) {
		s.types["__Schema"].fieldMap[fieldName].resolve = fn
	}

	set("description", func(context.Context, any, map[string]any) (any, error) {
		if s.desc == "" {
			return nil, nil
		}

		return s.desc, nil
	})
	set("types", func(context.Context, any, map[string]any) (any, error) {
		out := make([]*gtype, 0, len(s.typeNames))
		for _, n := range s.typeNames {
			out = append(out, named(s.types[n]))
		}

		return out, nil
	})
	root := func(t *namedType) resolveFn {
		return func(context.Context, any, map[string]any) (any, error) {
			if t == nil {
				return nil, nil
			}

			return named(t), nil
		}
	}
	set("queryType", root(s.query))
	set("mutationType", root(s.mutation))
	set("subscriptionType", root(s.subscr))
	set("directives", func(context.Context, any, map[string]any) (any, error) {
		out := make([]*directiveType, 0, len(s.dirNames))
		for _, n := range s.dirNames {
			out = append(out, s.directives[n])
		}

		return out, nil
	})

	b.bindTypeIntrospection()
	b.bindMemberIntrospection()
}

//nolint:gocognit // one binding per introspection field; splitting scatters the schema
func (b *builder) bindTypeIntrospection() {
	s := b.s
	set := func(fieldName string, fn func(t *gtype, args map[string]any) any) {
		s.types["__Type"].fieldMap[fieldName].resolve = func(_ context.Context, p any, args map[string]any) (any, error) {
			t, _ := p.(*gtype)
			return fn(t, args), nil
		}
	}

	set("kind", func(t *gtype, _ map[string]any) any { return string(t.kind) })
	set("name", func(t *gtype, _ map[string]any) any {
		if t.named == nil {
			return nil
		}

		return t.named.name
	})
	set("description", func(t *gtype, _ map[string]any) any {
		if t.named == nil || t.named.desc == "" {
			return nil
		}

		return t.named.desc
	})
	set("specifiedByURL", func(t *gtype, _ map[string]any) any {
		if t.named == nil || t.named.specifiedBy == "" {
			return nil
		}

		return t.named.specifiedBy
	})
	set("fields", func(t *gtype, args map[string]any) any {
		if t.named == nil || (t.kind != kindObject && t.kind != kindInterface) {
			return nil
		}

		out := []*fieldType{}

		for _, f := range t.named.fields {
			if f.deprecated == nil || includeDeprecated(args) {
				out = append(out, f)
			}
		}

		return out
	})
	set("interfaces", func(t *gtype, _ map[string]any) any {
		if t.named == nil || (t.kind != kindObject && t.kind != kindInterface) {
			return nil
		}

		out := make([]*gtype, 0, len(t.named.interfaces))
		for _, it := range t.named.interfaces {
			out = append(out, named(it))
		}

		return out
	})
	set("possibleTypes", func(t *gtype, _ map[string]any) any {
		if t.named == nil || !t.named.isAbstract() {
			return nil
		}

		out := make([]*gtype, 0, len(t.named.possible))
		for _, it := range t.named.possible {
			out = append(out, named(it))
		}

		return out
	})
	set("enumValues", func(t *gtype, args map[string]any) any {
		if t.named == nil || t.kind != kindEnum {
			return nil
		}

		out := []*enumValue{}

		for _, v := range t.named.enumValues {
			if v.deprecated == nil || includeDeprecated(args) {
				out = append(out, v)
			}
		}

		return out
	})
	set("inputFields", func(t *gtype, args map[string]any) any {
		if t.named == nil || t.kind != kindInputObject {
			return nil
		}

		return filterInputs(t.named.inputs, args)
	})
	set("ofType", func(t *gtype, _ map[string]any) any {
		if t.ofType == nil {
			return nil
		}

		return t.ofType
	})
}

func filterInputs(ivs []*inputValue, args map[string]any) []*inputValue {
	out := []*inputValue{}

	for _, iv := range ivs {
		if iv.deprecated == nil || includeDeprecated(args) {
			out = append(out, iv)
		}
	}

	return out
}

func (b *builder) bindMemberIntrospection() {
	s := b.s
	desc := func(d string) any {
		if d == "" {
			return nil
		}

		return d
	}

	field := func(name string, fn func(f *fieldType, args map[string]any) any) {
		s.types["__Field"].fieldMap[name].resolve = func(_ context.Context, p any, args map[string]any) (any, error) {
			f, _ := p.(*fieldType)
			return fn(f, args), nil
		}
	}
	field("name", func(f *fieldType, _ map[string]any) any { return f.name })
	field("description", func(f *fieldType, _ map[string]any) any { return desc(f.desc) })
	field("args", func(f *fieldType, args map[string]any) any { return filterInputs(f.args, args) })
	field("type", func(f *fieldType, _ map[string]any) any { return f.typ })
	field("isDeprecated", func(f *fieldType, _ map[string]any) any { d, _ := deprecated(f.deprecated); return d })
	field("deprecationReason", func(f *fieldType, _ map[string]any) any { _, r := deprecated(f.deprecated); return r })

	input := func(name string, fn func(iv *inputValue) any) {
		s.types["__InputValue"].fieldMap[name].resolve = func(_ context.Context, p any, _ map[string]any) (any, error) {
			iv, _ := p.(*inputValue)
			return fn(iv), nil
		}
	}
	input("name", func(iv *inputValue) any { return iv.name })
	input("description", func(iv *inputValue) any { return desc(iv.desc) })
	input("type", func(iv *inputValue) any { return iv.typ })
	input("defaultValue", func(iv *inputValue) any {
		if iv.defLiteral == nil {
			return nil
		}

		return printGraphQL(iv.defLiteral)
	})
	input("isDeprecated", func(iv *inputValue) any { d, _ := deprecated(iv.deprecated); return d })
	input("deprecationReason", func(iv *inputValue) any { _, r := deprecated(iv.deprecated); return r })

	enum := func(name string, fn func(v *enumValue) any) {
		s.types["__EnumValue"].fieldMap[name].resolve = func(_ context.Context, p any, _ map[string]any) (any, error) {
			v, _ := p.(*enumValue)
			return fn(v), nil
		}
	}
	enum("name", func(v *enumValue) any { return v.name })
	enum("description", func(v *enumValue) any { return desc(v.desc) })
	enum("isDeprecated", func(v *enumValue) any { d, _ := deprecated(v.deprecated); return d })
	enum("deprecationReason", func(v *enumValue) any { _, r := deprecated(v.deprecated); return r })

	dir := func(name string, fn func(d *directiveType, args map[string]any) any) {
		s.types["__Directive"].fieldMap[name].resolve = func(_ context.Context, p any, args map[string]any) (any, error) {
			d, _ := p.(*directiveType)
			return fn(d, args), nil
		}
	}
	dir("name", func(d *directiveType, _ map[string]any) any { return d.name })
	dir("description", func(d *directiveType, _ map[string]any) any { return desc(d.desc) })
	dir("locations", func(d *directiveType, _ map[string]any) any { return d.locations })
	dir("args", func(d *directiveType, args map[string]any) any { return filterInputs(d.args, args) })
	dir("isRepeatable", func(d *directiveType, _ map[string]any) any { return d.repeatable })
}

// printGraphQL prints a constant literal in GraphQL syntax (for
// __InputValue.defaultValue).
func printGraphQL(v *value) string {
	switch v.kind { //nolint:exhaustive // the remaining kinds take the common path below
	case valString:
		return strconv.Quote(v.raw)
	case valList:
		parts := make([]string, 0, len(v.list))
		for _, it := range v.list {
			parts = append(parts, printGraphQL(it))
		}

		return "[" + strings.Join(parts, ", ") + "]"
	case valObject:
		parts := make([]string, 0, len(v.fields))
		for _, f := range v.fields {
			parts = append(parts, f.name+": "+printGraphQL(f.value))
		}

		return "{" + strings.Join(parts, ", ") + "}"
	case valVariable:
		return "$" + v.raw
	}

	return v.raw
}

// IntrospectionQuery is the standard query GraphiQL and code generators
// send to read a schema.
const IntrospectionQuery = `query IntrospectionQuery {
  __schema {
    description
    queryType { name }
    mutationType { name }
    subscriptionType { name }
    types { ...FullType }
    directives {
      name
      description
      isRepeatable
      locations
      args(includeDeprecated: true) { ...InputValue }
    }
  }
}

fragment FullType on __Type {
  kind
  name
  description
  specifiedByURL
  fields(includeDeprecated: true) {
    name
    description
    args(includeDeprecated: true) { ...InputValue }
    type { ...TypeRef }
    isDeprecated
    deprecationReason
  }
  inputFields(includeDeprecated: true) { ...InputValue }
  interfaces { ...TypeRef }
  enumValues(includeDeprecated: true) {
    name
    description
    isDeprecated
    deprecationReason
  }
  possibleTypes { ...TypeRef }
}

fragment InputValue on __InputValue {
  name
  description
  type { ...TypeRef }
  defaultValue
  isDeprecated
  deprecationReason
}

fragment TypeRef on __Type {
  kind
  name
  ofType {
    kind
    name
    ofType {
      kind
      name
      ofType {
        kind
        name
        ofType {
          kind
          name
          ofType {
            kind
            name
            ofType {
              kind
              name
              ofType {
                kind
                name
                ofType {
                  kind
                  name
                }
              }
            }
          }
        }
      }
    }
  }
}`
