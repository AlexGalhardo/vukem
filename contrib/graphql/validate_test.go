package graphql

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

const validationSDL = `
interface Pet { name(surname: Boolean): String }
type Dog implements Pet {
  name(surname: Boolean): String
  nickname: String
  barkVolume: Int
  doesKnowCommand(dogCommand: DogCommand!): Boolean!
  isHouseTrained(atOtherHomes: Boolean): Boolean!
  owner: Human
}
type Cat implements Pet { name(surname: Boolean): String meowVolume: Int }
type Human { name: String pets: [Pet!]! }
union CatOrDog = Cat | Dog
enum DogCommand { SIT DOWN HEEL }
input FindDogInput { name: String owner: String }
input Required { id: ID! tags: [String!] }
type Query {
  dog: Dog
  pet: Pet
  catOrDog: CatOrDog
  findDog(searchBy: FindDogInput): Dog
  booleanList(booleanListArg: [Boolean!]): Boolean
  required(arg: Required!, count: Int! = 1, opt: Int): Boolean
  int(i: Int, n: Int!): Int
}
type Mutation { mutate: Boolean }
`

func validationSchema(t *testing.T) *Schema {
	t.Helper()

	opts := []Option{}
	for _, f := range []string{"dog", "pet", "catOrDog", "findDog", "booleanList", "required", "int"} {
		opts = append(opts, Query(f, func(_ context.Context, _ map[string]any) (any, error) { return nil, nil }))
	}

	opts = append(opts, Mutation("mutate", func(_ context.Context, _ NoArgs) (bool, error) { return true, nil }))

	s, err := New(validationSDL, opts...)
	if err != nil {
		t.Fatalf("New(validation) error = %v", err)
	}

	return s
}

func TestValidation(t *testing.T) {
	t.Parallel()

	s := validationSchema(t)

	tests := []struct {
		name, query string
		want        []string // nil when the document is valid
	}{
		{"valid", `{ dog { name nickname barkVolume } }`, nil},
		{
			"valid fragments",
			`{ dog { ...F } pet { ... on Dog { barkVolume } ...P } } fragment F on Dog { name } fragment P on Pet { name }`,
			nil,
		},
		{"unknown field", `{ dog { meowVolume } }`, []string{`Cannot query field "meowVolume" on type "Dog". (1:9)`}},
		{"field on union", `{ catOrDog { name } }`, []string{`Cannot query field "name" on type "CatOrDog". (1:14)`}},
		{"typename on union", `{ catOrDog { __typename ... on Cat { meowVolume } } }`, nil},
		{
			"leaf with selection",
			`{ dog { barkVolume { x } } }`,
			[]string{`Field "barkVolume" must not have a selection since type "Int" has no subfields. (1:9)`},
		},
		{
			"composite without selection",
			`{ dog }`,
			[]string{`Field "dog" of type "Dog" must have a selection of subfields. Did you mean "dog { ... }"? (1:3)`},
		},
		{
			"duplicate operation",
			`query A { dog { name } } query A { dog { name } }`,
			[]string{`There can be only one operation named "A". (1:26)`},
		},
		{
			"anonymous not alone",
			`{ dog { name } } query B { dog { name } }`,
			[]string{`This anonymous operation must be the only defined operation. (1:1)`},
		},
		{
			"unknown argument",
			`{ dog { name(nope: true) } }`,
			[]string{`Unknown argument "nope" on field "Dog.name". (1:14)`},
		},
		{
			"duplicate argument",
			`{ dog { name(surname: true, surname: false) } }`,
			[]string{`There can be only one argument named "surname". (1:29)`},
		},
		{
			"missing required argument",
			`{ dog { doesKnowCommand } }`,
			[]string{
				`Field "Dog.doesKnowCommand" argument "dogCommand" of type "DogCommand!" is required, but it was not provided. (1:9)`,
			},
		},
		{"required arg with default", `{ required(arg: {id: 1}) }`, nil},
		{
			"missing input field",
			`{ required(arg: {tags: []}) }`,
			[]string{`Field "Required.id" of required type "ID!" was not provided. (1:17)`},
		},
		{
			"unknown input field",
			`{ findDog(searchBy: {name: "x", color: "y"}) { name } }`,
			[]string{`Field "color" is not defined by type "FindDogInput". (1:33)`},
		},
		{
			"bad enum literal",
			`{ dog { doesKnowCommand(dogCommand: JUMP) } }`,
			[]string{
				`Argument "dogCommand" has invalid value JUMP: value "JUMP" does not exist in "DogCommand" enum. (1:37)`,
			},
		},
		{
			"string for enum",
			`{ dog { doesKnowCommand(dogCommand: "SIT") } }`,
			[]string{
				`Argument "dogCommand" has invalid value "SIT": enum "DogCommand" cannot represent non-enum value: "SIT". (1:37)`,
			},
		},
		{
			"int out of range",
			`{ int(n: 2147483648) }`,
			[]string{
				`Argument "n" has invalid value 2147483648: int cannot represent non 32-bit signed integer value: 2147483648. (1:10)`,
			},
		},
		{
			"float for int",
			`{ int(n: 1.5) }`,
			[]string{`Argument "n" has invalid value 1.5: int cannot represent non-integer value: 1.5. (1:10)`},
		},
		{
			"null for non-null",
			`{ int(n: null) }`,
			[]string{`Argument "n": expected value of type "Int!", found null. (1:10)`},
		},
		{"list coercion", `{ booleanList(booleanListArg: true) }`, nil},
		{"unknown fragment", `{ dog { ...Nope } }`, []string{`Unknown fragment "Nope". (1:9)`}},
		{
			"unused fragment",
			`{ dog { name } } fragment F on Dog { name }`,
			[]string{`Fragment "F" is never used. (1:18)`},
		},
		{
			"fragment unknown type",
			`{ dog { ...F } } fragment F on Nope { name }`,
			[]string{`Unknown type "Nope". (1:18)`},
		},
		{
			"fragment on scalar",
			`{ dog { ...F } } fragment F on Int { name }`,
			[]string{`Fragment "F" cannot condition on non composite type "Int". (1:18)`},
		},
		{
			"impossible spread",
			`{ dog { ... on Cat { meowVolume } } }`,
			[]string{`Fragment cannot be spread here as objects of type "Dog" can never be of type "Cat". (1:9)`},
		},
		{
			"impossible named spread",
			`{ dog { ...C } } fragment C on Cat { meowVolume }`,
			[]string{`Fragment "C" cannot be spread here as objects of type "Dog" can never be of type "Cat". (1:9)`},
		},
		{
			"fragment cycle",
			`{ dog { ...A } } fragment A on Dog { ...B } fragment B on Dog { ...A }`,
			[]string{`Cannot spread fragment "A" within itself via "B". (1:18)`},
		},
		{
			"self cycle",
			`{ dog { ...A } } fragment A on Dog { name ...A }`,
			[]string{`Cannot spread fragment "A" within itself. (1:18)`},
		},
		{
			"undefined variable",
			`query Q { int(n: $x) }`,
			[]string{`Variable "$x" is not defined by operation "Q". (1:18) (1:1)`},
		},
		{
			"unused variable",
			`query Q($x: Int) { int(n: 1) }`,
			[]string{`Variable "$x" is never used in operation "Q". (1:9)`},
		},
		{
			"duplicate variable",
			`query Q($x: Int, $x: Int) { int(i: $x, n: 1) }`,
			[]string{`There can be only one variable named "$x". (1:18)`},
		},
		{
			"non-input variable",
			`query Q($x: Dog) { int(n: 1) }`,
			[]string{
				`Variable "$x" cannot be non-input type "Dog". (1:13)`,
				`Variable "$x" is never used in operation "Q". (1:9)`,
			},
		},
		{
			"nullable variable for non-null",
			`query Q($x: Int) { int(n: $x) }`,
			[]string{`Variable "$x" of type "Int" used in position expecting type "Int!". (1:9) (1:27)`},
		},
		{"nullable variable with default", `query Q($x: Int = 3) { int(n: $x) }`, nil},
		{"nullable variable into defaulted arg", `query Q($x: Int) { required(arg: {id: 1}, count: $x) }`, nil},
		{
			"variable in list",
			`query Q($b: Boolean) { booleanList(booleanListArg: [$b]) }`,
			[]string{`Variable "$b" of type "Boolean" used in position expecting type "Boolean!". (1:9) (1:53)`},
		},
		{
			"variable used in fragment",
			`query Q($c: DogCommand!) { dog { ...F } } fragment F on Dog { doesKnowCommand(dogCommand: $c) }`,
			nil,
		},
		{
			"bad variable default",
			`query Q($x: Int = "a") { int(i: $x, n: 1) }`,
			[]string{`Variable "$x" has invalid default value: int cannot represent non-integer value: "a". (1:19)`},
		},
		{"unknown directive", `{ dog { name @nope } }`, []string{`Unknown directive "@nope". (1:14)`}},
		{
			"misplaced directive",
			`query @skip(if: true) { dog { name } }`,
			[]string{`Directive "@skip" may not be used on QUERY. (1:7)`},
		},
		{
			"repeated directive",
			`{ dog { name @skip(if: true) @skip(if: false) } }`,
			[]string{`The directive "@skip" can only be used once at this location. (1:30)`},
		},
		{
			"directive missing arg",
			`{ dog { name @include } }`,
			[]string{
				`Directive "@include" argument "if" of type "Boolean!" is required, but it was not provided. (1:14)`,
			},
		},
		{
			"conflicting aliases",
			`{ dog { name: nickname name } }`,
			[]string{
				`Fields "name" conflict because "nickname" and "name" are different fields. Use different aliases on the fields to fetch both if this was intentional. (1:9) (1:24)`,
			},
		},
		{
			"conflicting args",
			`{ dog { name(surname: true) name(surname: false) } }`,
			[]string{
				`Fields "name" conflict because they have differing arguments. Use different aliases on the fields to fetch both if this was intentional. (1:9) (1:29)`,
			},
		},
		{
			"different types allowed on distinct objects",
			`{ pet { ... on Dog { x: barkVolume } ... on Cat { x: meowVolume } } }`,
			nil,
		},
		{
			"conflicting shapes",
			`{ catOrDog { ... on Dog { x: name } ... on Cat { x: meowVolume } } }`,
			[]string{
				`Fields "x" conflict because they return conflicting types "String" and "Int". Use different aliases on the fields to fetch both if this was intentional. (1:27) (1:50)`,
			},
		},
		{
			"no subscription root",
			`subscription { dog { name } }`,
			[]string{`Schema is not configured to execute subscription operation. (1:1)`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			doc, err := parseQuery(tt.query, 0)
			if err != nil {
				t.Fatalf("parseQuery(%q) error = %v", tt.query, err)
			}

			got := []string{}
			for _, e := range s.validate(doc, true) {
				var msg strings.Builder

				msg.WriteString(e.Message)

				for _, l := range e.Locations {
					msg.WriteString(" (" + strconv.Itoa(l.Line) + ":" + strconv.Itoa(l.Column) + ")")
				}

				got = append(got, msg.String())

				if e.Code() != CodeValidationFailed {
					t.Errorf("code = %q", e.Code())
				}
			}

			want := tt.want
			if want == nil {
				want = []string{}
			}

			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("validate(%q) (-want +got):\n%s", tt.query, diff)
			}
		})
	}
}

func TestValidationIntrospectionDisabled(t *testing.T) {
	t.Parallel()

	s := validationSchema(t)

	doc, _ := parseQuery(`{ __schema { types { name } } dog { __typename } }`, 0)
	errs := s.validate(doc, false)

	if len(errs) != 1 || !strings.Contains(errs[0].Message, "introspection is not allowed") {
		t.Fatalf("validate(introspection off) = %v, want one introspection error", errs)
	}
}
