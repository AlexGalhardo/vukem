package graphql

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// printDoc renders an executable document in a canonical one-line form so
// parse results can be compared as strings.
func printDoc(d *document) string {
	var parts []string

	for _, op := range d.operations {
		var b strings.Builder

		b.WriteString(string(op.kind))

		if op.name != "" {
			b.WriteString(" " + op.name)
		}

		if len(op.vars) > 0 {
			vs := make([]string, 0, len(op.vars))
			for _, v := range op.vars {
				s := "$" + v.name + ": " + v.typ.String()
				if v.def != nil {
					s += " = " + printValue(v.def)
				}

				vs = append(vs, s+printDirectives(v.directives))
			}

			b.WriteString("(" + strings.Join(vs, ", ") + ")")
		}

		b.WriteString(printDirectives(op.directives) + " " + printSel(op.sel))
		parts = append(parts, b.String())
	}

	for _, f := range d.fragments {
		parts = append(parts, "fragment "+f.name+" on "+f.typeCond+printDirectives(f.directives)+" "+printSel(f.sel))
	}

	return strings.Join(parts, "\n")
}

func printSel(sel []selection) string {
	items := make([]string, 0, len(sel))

	for _, s := range sel {
		switch s := s.(type) {
		case *field:
			out := s.name
			if s.alias != "" {
				out = s.alias + ": " + s.name
			}

			out += printArgs(s.args) + printDirectives(s.directives)
			if s.sel != nil {
				out += " " + printSel(s.sel)
			}

			items = append(items, out)
		case *fragmentSpread:
			items = append(items, "..."+s.name+printDirectives(s.directives))
		case *inlineFragment:
			out := "..."
			if s.typeCond != "" {
				out += " on " + s.typeCond
			}

			items = append(items, out+printDirectives(s.directives)+" "+printSel(s.sel))
		}
	}

	return "{ " + strings.Join(items, " ") + " }"
}

func printArgs(args []*argument) string {
	if len(args) == 0 {
		return ""
	}

	out := make([]string, 0, len(args))
	for _, a := range args {
		out = append(out, a.name+": "+printValue(a.value))
	}

	return "(" + strings.Join(out, ", ") + ")"
}

func printDirectives(ds []*directive) string {
	var b strings.Builder
	for _, d := range ds {
		b.WriteString(" @" + d.name + printArgs(d.args))
	}

	return b.String()
}

func printValue(v *value) string {
	switch v.kind {
	case valVariable:
		return "$" + v.raw
	case valString:
		return fmt.Sprintf("%q", v.raw)
	case valList:
		items := make([]string, 0, len(v.list))
		for _, it := range v.list {
			items = append(items, printValue(it))
		}

		return "[" + strings.Join(items, ", ") + "]"
	case valObject:
		items := make([]string, 0, len(v.fields))
		for _, f := range v.fields {
			items = append(items, f.name+": "+printValue(f.value))
		}

		return "{" + strings.Join(items, ", ") + "}"
	default:
		return v.raw
	}
}

func TestParseQuery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, src, want string
	}{
		{"shorthand", "{ hero { name } }", "query { hero { name } }"},
		{
			"named with commas and comments",
			"query Hero, { # comment\n hero,,{ name } }",
			"query Hero { hero { name } }",
		},
		{
			"alias and args",
			`{ empireHero: hero(episode: EMPIRE) { name } }`,
			`query { empireHero: hero(episode: EMPIRE) { name } }`,
		},
		{
			"variables with defaults",
			`query Q($ep: Episode = JEDI, $ids: [ID!]! = ["1"], $n: Int = -3, $f: Float = 1.5e3) { a }`,
			`query Q($ep: Episode = JEDI, $ids: [ID!]! = ["1"], $n: Int = -3, $f: Float = 1.5e3) { a }`,
		},
		{
			"fragments and inline fragments",
			`query { ...F ... on Droid { primaryFunction } ... @include(if: $x) { id } } fragment F on Character @dir { name }`,
			"query { ...F ... on Droid { primaryFunction } ... @include(if: $x) { id } }\nfragment F on Character @dir { name }",
		},
		{
			"directives",
			`query ($skip: Boolean!) @op { a @skip(if: $skip) b @include(if: true) @other }`,
			`query($skip: Boolean!) @op { a @skip(if: $skip) b @include(if: true) @other }`,
		},
		{
			"values",
			"{ f(a: null, b: true, c: [1, [2]], d: {x: \"y\", z: {w: ENUM}}, e: \"\"\"  block\n    text\n  \"\"\") }",
			`query { f(a: null, b: true, c: [1, [2]], d: {x: "y", z: {w: ENUM}}, e: "  block\ntext") }`,
		},
		{
			"mutation and subscription",
			"mutation M { like } subscription S { tick }",
			"mutation M { like }\nsubscription S { tick }",
		},
		{"field named like keywords", "{ query fragment on type }", "query { query fragment on type }"},
		{"escapes", `{ f(s: "a\"b\\c\/\n\u00e9\u{1F600}\uD83D\uDE00") }`, `query { f(s: "a\"b\\c/\né😀😀") }`},
		{"crlf and bom", "\uFEFFquery Q {\r\n a\r\n}", "query Q { a }"},
		{"empty list and object", "{ f(a: [], b: {}) }", "query { f(a: [], b: {}) }"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			doc, err := parseQuery(tt.src, 0)
			if err != nil {
				t.Fatalf("parseQuery(%q) error = %v", tt.src, err)
			}

			if diff := cmp.Diff(tt.want, printDoc(doc)); diff != "" {
				t.Errorf("parseQuery(%q) (-want +got):\n%s", tt.src, diff)
			}
		})
	}
}

func TestParseQueryErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, src, want string
	}{
		{"empty", "", `Syntax Error: Unexpected "<EOF>". (1:1)`},
		{"unclosed", "{ a", `Syntax Error: Expected Name, found "<EOF>". (1:4)`},
		{"bad char", "{ a ? }", `Syntax Error: Unexpected character '?'. (1:5)`},
		{"single dot", "{ a. }", `Syntax Error: Unexpected ".". (1:4)`},
		{"leading zero", "{ f(a: 01) }", `Syntax Error: Invalid number, unexpected digit after 0: '1'. (1:9)`},
		{"number then name", "{ f(a: 1x) }", `Syntax Error: Invalid number, expected digit but got: 'x'. (1:9)`},
		{"dangling exponent", "{ f(a: 1e) }", `Syntax Error: Invalid number, expected digit but got: ')'. (1:10)`},
		{"unterminated string", "{ f(a: \"abc\n) }", `Syntax Error: Unterminated string. (1:12)`},
		{"bad escape", `{ f(a: "\x") }`, `Syntax Error: Invalid character escape sequence: "\x". (1:9)`},
		{
			"lone surrogate",
			`{ f(a: "\uD83D") }`,
			`Syntax Error: Invalid Unicode escape sequence: unpaired surrogate. (1:9)`,
		},
		{"variable in default", "query ($a: Int = $b) { a }", `Syntax Error: Unexpected "$". (1:18)`},
		{"type definition", "type Foo { a: Int }", `Syntax Error: Unexpected Name "type". (1:1)`},
		{"fragment named on", "fragment on on X { a }", `Syntax Error: Unexpected Name "on". (1:10)`},
		{"position on line 3", "{\n  a\n  b(: 1)\n}", `Syntax Error: Expected Name, found ":". (3:5)`},
		{"empty selection", "{ }", `Syntax Error: Expected Name, found "}". (1:3)`},
		{"missing var type", "query ($a) { a }", `Syntax Error: Expected ":", found ")". (1:10)`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := parseQuery(tt.src, 0)
			if err == nil {
				t.Fatalf("parseQuery(%q) = nil error, want %q", tt.src, tt.want)
			}

			if diff := cmp.Diff(tt.want, err.Error()); diff != "" {
				t.Errorf("parseQuery(%q) error (-want +got):\n%s", tt.src, diff)
			}

			if ge, ok := errors.AsType[*Error](err); !ok || ge.Code() != CodeParseFailed {
				t.Errorf("parseQuery(%q) code = %v, want %s", tt.src, err, CodeParseFailed)
			}
		})
	}
}

func TestParseTokenLimit(t *testing.T) {
	t.Parallel()

	src := "{ " + strings.Repeat("a ", 50) + "}"
	if _, err := parseQuery(src, 52); err != nil {
		t.Fatalf("parseQuery within limit = %v", err)
	}

	_, err := parseQuery(src, 51)
	if err == nil || !strings.Contains(err.Error(), "exceeds the limit of 51 tokens") {
		t.Fatalf("parseQuery over limit = %v, want token limit error", err)
	}
}

func TestParseNestingLimit(t *testing.T) {
	t.Parallel()

	src := "{ f(a: " + strings.Repeat("[", maxNesting+1) + strings.Repeat("]", maxNesting+1) + ") }"
	if _, err := parseQuery(src, 0); err == nil || !strings.Contains(err.Error(), "nesting exceeds") {
		t.Fatalf("parseQuery deep list = %v, want nesting error", err)
	}
}

func TestBlockStringValue(t *testing.T) {
	t.Parallel()

	tests := []struct{ raw, want string }{
		{"", ""},
		{"  hello  ", "  hello  "},
		{"\n    Hello,\n      World!\n\n    Yours,\n      GraphQL.\n  ", "Hello,\n  World!\n\nYours,\n  GraphQL."},
		{"first\n  second\n  third", "first\nsecond\nthird"},
		{"  \n  \n  x\n  \n", "x"},
	}

	for _, tt := range tests {
		if got := blockStringValue(tt.raw); got != tt.want {
			t.Errorf("blockStringValue(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

func TestParseSchema(t *testing.T) {
	t.Parallel()

	src := `
"""The schema"""
schema @dir { query: Query mutation: Mutation }

"A scalar" scalar DateTime @specifiedBy(url: "https://example.com")

interface Node { id: ID! }
interface Named implements Node & Entity { id: ID! name: String }

"""
A user.
"""
type User implements & Node & Named @key(fields: "id") {
  id: ID!
  "Display name"
  name(upper: Boolean = false, "how" format: Format = SHORT @deprecated): String @deprecated(reason: "use title")
  friends(first: Int = 10): [User!]!
}

union Result = | User | Post
enum Format { SHORT "long one" LONG @deprecated }
input Filter { "q" q: String = "x", tags: [String!] = [], nested: Filter }
directive @key(fields: String!) repeatable on OBJECT | INTERFACE
directive @auth on | FIELD_DEFINITION
extend type User { age: Int }
extend schema { subscription: Sub }
type Empty
`

	doc, err := parseSchema(src)
	if err != nil {
		t.Fatalf("parseSchema error = %v", err)
	}

	got := []string{}
	for _, td := range doc.types {
		got = append(got, fmt.Sprintf("%s %s fields=%d ifaces=%v members=%v values=%d inputs=%d desc=%q",
			td.kind, td.name, len(td.fields), td.interfaces, td.members, len(td.values), len(td.inputs), td.desc))
	}

	want := []string{
		`SCALAR DateTime fields=0 ifaces=[] members=[] values=0 inputs=0 desc="A scalar"`,
		`INTERFACE Node fields=1 ifaces=[] members=[] values=0 inputs=0 desc=""`,
		`INTERFACE Named fields=2 ifaces=[Node Entity] members=[] values=0 inputs=0 desc=""`,
		`OBJECT User fields=3 ifaces=[Node Named] members=[] values=0 inputs=0 desc="A user."`,
		`UNION Result fields=0 ifaces=[] members=[User Post] values=0 inputs=0 desc=""`,
		`ENUM Format fields=0 ifaces=[] members=[] values=2 inputs=0 desc=""`,
		`INPUT_OBJECT Filter fields=0 ifaces=[] members=[] values=0 inputs=3 desc=""`,
		`OBJECT Empty fields=0 ifaces=[] members=[] values=0 inputs=0 desc=""`,
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("parseSchema types (-want +got):\n%s", diff)
	}

	if doc.schema.desc != "The schema" || doc.schema.ops[opMutation] != "Mutation" {
		t.Errorf("schema = %+v", doc.schema)
	}

	name := doc.types[3].fields[1]
	if len(name.args) != 2 || name.args[1].desc != "how" || name.args[1].def.raw != "SHORT" ||
		name.desc != "Display name" {
		t.Errorf("User.name = %+v", name)
	}

	if len(doc.directives) != 2 || !doc.directives[0].repeatable || len(doc.directives[0].locations) != 2 {
		t.Errorf("directives = %+v", doc.directives)
	}

	if len(doc.extensions) != 1 || len(doc.schemaExt) != 1 {
		t.Errorf("extensions = %d, schema extensions = %d", len(doc.extensions), len(doc.schemaExt))
	}
}

func TestParseSchemaErrors(t *testing.T) {
	t.Parallel()

	tests := []struct{ name, src, want string }{
		{"query in sdl", "query { a }", `Syntax Error: Unexpected Name "query". (1:1)`},
		{
			"two schemas",
			"schema { query: Q } schema { query: Q }",
			`Syntax Error: Must provide only one schema definition. (1:21)`,
		},
		{"bad op type", "schema { fetch: Q }", `Syntax Error: Unexpected Name "fetch". (1:10)`},
		{"empty fields", "type A {}", `Syntax Error: Unexpected "}". (1:9)`},
		{
			"reserved enum value",
			"enum E { true }",
			`Syntax Error: Name "true" is reserved and cannot be used for an enum value. (1:10)`,
		},
		{"bad directive location", "directive @a on NOWHERE", `Syntax Error: Unexpected Name "NOWHERE". (1:17)`},
		{"variable in sdl default", "type A { f(a: Int = $x): Int }", `Syntax Error: Unexpected "$". (1:21)`},
		{"described extension", `"d" extend type A { f: Int }`, `Syntax Error: Unexpected Name "extend". (1:5)`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := parseSchema(tt.src)
			if err == nil {
				t.Fatalf("parseSchema(%q) = nil error", tt.src)
			}

			if diff := cmp.Diff(tt.want, err.Error()); diff != "" {
				t.Errorf("parseSchema(%q) error (-want +got):\n%s", tt.src, diff)
			}
		})
	}
}

func FuzzParseQuery(f *testing.F) {
	for _, s := range []string{
		"{ a }", `query Q($a: [Int!]! = [1]) @d { a: b(c: {d: "e"}) ...F ... on T { x } }`,
		"fragment F on T { a }", `{ f(s: """block""") }`, "{ a(b: 1.5e-3, c: -0) }", "\uFEFF#c\r\n{a}",
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, src string) {
		doc, err := parseQuery(src, 2000)
		if err != nil {
			if _, ok := errors.AsType[*Error](err); !ok {
				t.Fatalf("parseQuery(%q) error type %T", src, err)
			}

			return
		}

		if len(doc.operations)+len(doc.fragments) == 0 {
			t.Fatalf("parseQuery(%q) returned an empty document without error", src)
		}
	})
}

func FuzzParseSchema(f *testing.F) {
	f.Add("type Query { a(b: Int = 1): [String!]! @deprecated }")
	f.Add(`"d" interface I implements J & K { a: Int } union U = A | B enum E { A B } input In { a: Int = 1 }`)
	f.Add("directive @d(a: Int) repeatable on FIELD | OBJECT extend type A { b: Int }")

	f.Fuzz(func(t *testing.T, src string) {
		if len(src) > 4096 {
			return
		}

		if _, err := parseSchema(src); err != nil {
			if _, ok := errors.AsType[*Error](err); !ok {
				t.Fatalf("parseSchema(%q) error type %T", src, err)
			}
		}
	})
}
