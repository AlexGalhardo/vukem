package graphql

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

const nodeSDL = `
type Node { id: Int child: Node children(first: Int, limit: Int): [Node!]! expensive: Int }
type Query { node: Node nodes(first: Int): [Node!]! }
`

func nodeSchema(t *testing.T, opts ...Option) *Schema {
	t.Helper()

	type node struct {
		ID int `json:"id"`
	}

	base := []Option{
		Query("node", func(context.Context, NoArgs) (*node, error) { return &node{ID: 1}, nil }),
		Query("nodes", func(context.Context, map[string]any) ([]node, error) { return []node{{ID: 1}}, nil }),
		Field(
			"Node",
			"child",
			func(_ context.Context, n *node, _ NoArgs) (*node, error) { return &node{ID: n.ID + 1}, nil },
		),
		Field("Node", "children", func(context.Context, *node, map[string]any) ([]node, error) { return nil, nil }),
		Field("Node", "expensive", func(context.Context, *node, NoArgs) (int, error) { return 0, nil }, Cost(50)),
	}

	s, err := New(nodeSDL, append(base, opts...)...)
	if err != nil {
		t.Fatalf("New(node) error = %v", err)
	}

	return s
}

func nested(depth int) string {
	return "{ node " + strings.Repeat("{ child ", depth-1) + "{ id }" + strings.Repeat(" }", depth-1) + " }"
}

func firstCode(r *Response) string {
	if len(r.Errors) == 0 {
		return ""
	}

	return r.Errors[0].Code()
}

func TestSecurity_A06_GraphQLDepthAndComplexityLimits(t *testing.T) {
	t.Parallel()

	s := nodeSchema(t)

	// node + 11 child levels + id = depth 13 > default 12.
	if r := s.Execute(t.Context(), Request{Query: nested(12)}); firstCode(r) != CodeLimitExceeded ||
		r.Errors[0].Message != "Query depth 13 exceeds the maximum of 12." || r.Data != nil {
		t.Errorf("depth 13 = %+v, want QUERY_LIMIT_EXCEEDED without data", r.Errors)
	}

	if r := s.Execute(t.Context(), Request{Query: nested(11)}); len(r.Errors) > 0 {
		t.Errorf("depth 12 errors = %v, want none", r.Errors)
	}

	// 1 + 100 × (1 + 50 × 1 + 1) complexity through a list multiplier.
	q := `{ nodes(first: 100) { id expensive child { id } } }`
	if r := s.Execute(t.Context(), Request{Query: q}); firstCode(r) != CodeLimitExceeded ||
		!strings.Contains(r.Errors[0].Message, "complexity 5301 exceeds the maximum of 1000") {
		t.Errorf("complexity = %v, want limit error", r.Errors)
	}

	// The multiplier also comes from variables.
	qv := `query ($n: Int) { nodes(first: $n) { expensive } }`
	if r := s.Execute(
		t.Context(),
		Request{Query: qv, Variables: map[string]any{"n": 30}},
	); firstCode(
		r,
	) != CodeLimitExceeded {
		t.Errorf("complexity via variable = %v, want limit error", r.Errors)
	}

	if r := s.Execute(t.Context(), Request{Query: qv, Variables: map[string]any{"n": 10}}); len(r.Errors) > 0 {
		t.Errorf("complexity 501 errors = %v", r.Errors)
	}

	var aliases strings.Builder
	for i := range 31 {
		aliases.WriteString(" a" + strings.Repeat("x", i) + ": node { id }")
	}

	if r := s.Execute(t.Context(), Request{Query: "{" + aliases.String() + " }"}); firstCode(r) != CodeLimitExceeded ||
		!strings.Contains(r.Errors[0].Message, "aliases 31") {
		t.Errorf("aliases = %v, want limit error", r.Errors)
	}

	long := "{ node { " + strings.Repeat("id ", DefaultMaxTokens) + "} }"
	if r := s.Execute(t.Context(), Request{Query: long}); firstCode(r) != CodeParseFailed ||
		!strings.Contains(r.Errors[0].Message, "exceeds the limit of 10000 tokens") {
		t.Errorf("tokens = %v, want token limit error", r.Errors)
	}

	// Fragments spread many times are measured without blowing up.
	frags := `{ node { ...A } }
		fragment A on Node { child { ...B } child2: child { ...B } }
		fragment B on Node { child { ...C } child2: child { ...C } }
		fragment C on Node { id }`
	if r := s.Execute(t.Context(), Request{Query: frags}); len(r.Errors) > 0 {
		t.Errorf("fragments errors = %v", r.Errors)
	}
}

func TestLimitsConfigurable(t *testing.T) {
	t.Parallel()

	s := nodeSchema(t, WithLimits(Limits{MaxDepth: 2, MaxComplexity: -1, MaxAliases: -1}))

	if r := s.Execute(t.Context(), Request{Query: nested(3)}); firstCode(r) != CodeLimitExceeded {
		t.Errorf("MaxDepth 2 = %v", r.Errors)
	}

	if r := s.Execute(t.Context(), Request{Query: `{ nodes(first: 100000) { expensive } }`}); len(r.Errors) > 0 {
		t.Errorf("disabled complexity errors = %v", r.Errors)
	}
}

// introspectionBomb nests a fragment n levels deep, each level selecting
// the previous one twice through aliases and once through a list.
func introspectionBomb(n int) string {
	var b strings.Builder

	b.WriteString(`{ __type(name: "__Type") { ...F` + strconv.Itoa(n) + ` } }`)
	b.WriteString("\nfragment F0 on __Type { name }")

	for i := 1; i <= n; i++ {
		prev := "F" + strconv.Itoa(i-1)
		b.WriteString("\nfragment F" + strconv.Itoa(i) + " on __Type { name ofType { ..." + prev +
			" } a: fields { type { ..." + prev + " } } b: fields { type { ..." + prev + " } } }")
	}

	return b.String()
}

func TestSecurity_A06_GraphQLIntrospectionBounded(t *testing.T) {
	t.Parallel()

	s := nodeSchema(t)

	r := s.Execute(t.Context(), Request{Query: introspectionBomb(10)})
	if firstCode(r) != CodeLimitExceeded {
		t.Fatalf(
			"introspection bomb: code = %q (%d bytes of data), want %s",
			firstCode(r),
			len(r.Data),
			CodeLimitExceeded,
		)
	}

	// List fan-out without aliases: every level returns all fields again.
	deep := "{ __schema { types { " + strings.Repeat(
		"fields { type { ",
		10,
	) + "name" + strings.Repeat(
		" } }",
		10,
	) + " } } }"
	if r := s.Execute(t.Context(), Request{Query: deep}); firstCode(r) != CodeLimitExceeded {
		t.Fatalf(
			"deep introspection: code = %q (%d bytes of data), want %s",
			firstCode(r),
			len(r.Data),
			CodeLimitExceeded,
		)
	}
}

func TestSecurity_A06_GraphQLIntrospectionFieldCap(t *testing.T) {
	t.Parallel()

	s := nodeSchema(t)
	s.maxMetaFields = 50

	r := s.Execute(t.Context(), Request{Query: IntrospectionQuery})
	if firstCode(r) != CodeLimitExceeded || len(r.Errors) != 1 {
		t.Fatalf("capped introspection: errors = %v, want one %s", r.Errors, CodeLimitExceeded)
	}
}

func TestIntrospectionQueryWithinDefaultLimits(t *testing.T) {
	t.Parallel()

	if r := nodeSchema(t).Execute(t.Context(), Request{Query: IntrospectionQuery}); len(r.Errors) > 0 {
		t.Errorf("IntrospectionQuery errors = %v", r.Errors)
	}
}
