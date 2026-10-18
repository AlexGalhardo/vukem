package graphql

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/z"
)

var update = flag.Bool("update", false, "rewrite golden files")

const nullSDL = `
type Query {
  nullable: Inner
  nonNull: Inner!
  list: [Inner!]
  items: [Inner]!
  empty: [Inner!]!
}
type Inner {
  ok: String
  fail: String
  failNonNull: String!
  nested: Inner
  nestedNonNull: Inner!
}
`

type inner struct{}

func nullSchema(t *testing.T) *Schema {
	t.Helper()

	one := func(context.Context, NoArgs) (*inner, error) { return &inner{}, nil }
	two := func(context.Context, NoArgs) ([]*inner, error) { return []*inner{{}, {}}, nil }

	s, err := New(nullSDL,
		Query("nullable", one), Query("nonNull", one), Query("list", two), Query("items", two),
		Query("empty", func(context.Context, NoArgs) ([]*inner, error) { return nil, nil }),
		Field("Inner", "ok", func(context.Context, *inner, NoArgs) (string, error) { return "ok", nil }),
		Field("Inner", "fail", func(context.Context, *inner, NoArgs) (*string, error) {
			return nil, errs.New(http.StatusNotFound, "gone")
		}),
		Field("Inner", "failNonNull", func(context.Context, *inner, NoArgs) (string, error) {
			return "", errs.New(http.StatusForbidden, "")
		}),
		Field("Inner", "nested", func(context.Context, *inner, NoArgs) (*inner, error) { return &inner{}, nil }),
		Field("Inner", "nestedNonNull", func(context.Context, *inner, NoArgs) (*inner, error) { return nil, nil }),
	)
	if err != nil {
		t.Fatalf("New(null) error = %v", err)
	}

	return s
}

func TestNullPropagation(t *testing.T) {
	t.Parallel()

	s := nullSchema(t)

	tests := []struct{ name, query, want string }{
		{
			"nullable field error",
			`{ nullable { ok fail } }`,
			`{"errors":[{"message":"gone","locations":[{"line":1,"column":17}],"path":["nullable","fail"],"extensions":{"code":"NOT_FOUND"}}],
			  "data":{"nullable":{"ok":"ok","fail":null}}}`,
		},
		{
			"non-null error bubbles to nullable parent",
			`{ nullable { ok failNonNull } }`,
			`{"errors":[{"message":"Forbidden","locations":[{"line":1,"column":17}],"path":["nullable","failNonNull"],"extensions":{"code":"FORBIDDEN"}}],
			  "data":{"nullable":null}}`,
		},
		{
			"non-null root makes data null",
			`{ nonNull { failNonNull } }`,
			`{"errors":[{"message":"Forbidden","locations":[{"line":1,"column":13}],"path":["nonNull","failNonNull"],"extensions":{"code":"FORBIDDEN"}}],
			  "data":null}`,
		},
		{
			"null returned for non-null",
			`{ nullable { nested { nestedNonNull { ok } } } }`,
			`{"errors":[{"message":"Cannot return null for non-nullable field Inner.nestedNonNull.","locations":[{"line":1,"column":23}],
			  "path":["nullable","nested","nestedNonNull"],"extensions":{"code":"INTERNAL_SERVER_ERROR"}}],
			  "data":{"nullable":{"nested":null}}}`,
		},
		{
			"non-null item nulls the list", `{ list { failNonNull } }`,
			`{"errors":[
			  {"message":"Forbidden","locations":[{"line":1,"column":10}],"path":["list",0,"failNonNull"],"extensions":{"code":"FORBIDDEN"}},
			  {"message":"Forbidden","locations":[{"line":1,"column":10}],"path":["list",1,"failNonNull"],"extensions":{"code":"FORBIDDEN"}}],
			  "data":{"list":null}}`,
		},
		{
			"nullable items null individually", `{ items { failNonNull } }`,
			`{"errors":[
			  {"message":"Forbidden","locations":[{"line":1,"column":11}],"path":["items",0,"failNonNull"],"extensions":{"code":"FORBIDDEN"}},
			  {"message":"Forbidden","locations":[{"line":1,"column":11}],"path":["items",1,"failNonNull"],"extensions":{"code":"FORBIDDEN"}}],
			  "data":{"items":[null,null]}}`,
		},
		{"nil slice is an empty list", `{ empty { ok } }`, `{"data":{"empty":[]}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			jsonEqual(t, tt.want, run(t, s, tt.query, nil))
		})
	}
}

func TestMutationsRunSerially(t *testing.T) {
	t.Parallel()

	var (
		mu     sync.Mutex
		order  []int
		active atomic.Int32
		peak   atomic.Int32
	)

	type setArgs struct {
		N int `json:"n"`
	}

	s, err := New(`type Query { n: Int } type Mutation { set(n: Int!): Int }`,
		Query("n", func(context.Context, NoArgs) (int, error) { return 0, nil }),
		Mutation("set", func(_ context.Context, a setArgs) (int, error) {
			peak.Store(max(peak.Load(), active.Add(1)))
			defer active.Add(-1)

			mu.Lock()
			order = append(order, a.N)
			mu.Unlock()

			return a.N, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	got := run(t, s, `mutation { a: set(n: 1) b: set(n: 2) c: set(n: 3) d: set(n: 4) }`, nil)
	jsonEqual(t, `{"data":{"a":1,"b":2,"c":3,"d":4}}`, got)

	if diff := cmp.Diff([]int{1, 2, 3, 4}, order); diff != "" || peak.Load() != 1 {
		t.Errorf("mutation order (-want +got):\n%s peak concurrency = %d, want 1", diff, peak.Load())
	}
}

func TestQueryFieldsResolveConcurrently(t *testing.T) {
	t.Parallel()

	// Both resolvers wait for each other: this only finishes when they
	// run at the same time.
	var wg sync.WaitGroup
	wg.Add(2)

	meet := func(context.Context, NoArgs) (int, error) {
		wg.Done()

		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()

		select {
		case <-done:
			return 1, nil
		case <-time.After(5 * time.Second):
			return 0, errors.New("siblings did not run concurrently")
		}
	}

	s, err := New(`type Query { a: Int b: Int }`, Query("a", meet), Query("b", meet))
	if err != nil {
		t.Fatal(err)
	}

	jsonEqual(t, `{"data":{"a":1,"b":1}}`, run(t, s, `{ a b }`, nil))
}

const scalarSDL = `
scalar DateTime @specifiedBy(url: "https://scalars.graphql.org/andimarek/date-time")
scalar JSON
enum Color { RED GREEN }
input PostInput { title: String! tags: [String!] = [] color: Color = RED when: DateTime meta: JSON }
type Post { title: String! tags: [String!]! color: Color! when: DateTime meta: JSON }
union Result = Post | Problem
type Problem { reason: String }
type Query {
  echo(in: PostInput!): Post
  at(when: DateTime!): DateTime
  big: Int
  results: [Result!]!
  validated(email: String, age: Int): Boolean
  status(code: Int!): String
}
`

type postInput struct {
	Title string         `json:"title"`
	Tags  []string       `json:"tags"`
	Color string         `json:"color"`
	When  *time.Time     `json:"when"`
	Meta  map[string]any `json:"meta"`
}

type problem struct {
	Reason string `json:"reason"`
}

type validatedArgs struct {
	Email string `json:"email" validate:"required,email"`
	Age   int    `json:"age"   validate:"min=18"`
}

type rateLimitError struct{}

func (rateLimitError) Error() string              { return "rate limited" }
func (rateLimitError) HTTPStatus() int            { return http.StatusTooManyRequests }
func (rateLimitError) Extensions() map[string]any { return map[string]any{"retryAfter": 30} }
func (p *postInput) GraphQLTypename() string      { return "Post" }

func scalarSchema(t *testing.T) *Schema {
	t.Helper()

	s, err := New(scalarSDL,
		Scalar("DateTime", ScalarFuncs{
			Serialize: func(v any) (any, error) {
				switch v := v.(type) {
				case time.Time:
					return v.UTC().Format(time.RFC3339), nil
				case *time.Time:
					return v.UTC().Format(time.RFC3339), nil
				}

				return nil, fmt.Errorf("not a time: %T", v)
			},
			Parse: func(v any) (any, error) {
				s, ok := v.(string)
				if !ok {
					return nil, errors.New("must be an RFC 3339 string")
				}

				return time.Parse(time.RFC3339, s)
			},
		}),
		Object[problem]("Problem"),
		Query("echo", func(_ context.Context, a struct {
			In postInput `json:"in"`
		},
		) (*postInput, error) {
			return &a.In, nil
		}),
		Query("at", func(_ context.Context, a struct {
			When time.Time `json:"when"`
		},
		) (time.Time, error) {
			return a.When.Add(time.Hour), nil
		}),
		Query("big", func(context.Context, NoArgs) (int64, error) { return 1 << 40, nil }),
		Query("results", func(context.Context, NoArgs) ([]any, error) {
			return []any{&postInput{Title: "a", Color: "GREEN"}, problem{Reason: "b"}}, nil
		}),
		Query("validated", func(context.Context, validatedArgs) (bool, error) { return true, nil }),
		Query("status", func(_ context.Context, a struct {
			Code int `json:"code"`
		},
		) (*string, error) {
			switch a.Code {
			case 401:
				return nil, errs.ErrUnauthorized
			case 422:
				return nil, errs.NewValidation().Add("code", "is odd")
			case 429:
				return nil, rateLimitError{}
			case 500:
				return nil, errs.Wrap(http.StatusInternalServerError, errors.New("db password=secret"))
			}

			panic("boom")
		}),
	)
	if err != nil {
		t.Fatalf("New(scalar) error = %v", err)
	}

	return s
}

func TestInputsScalarsAndErrors(t *testing.T) {
	t.Parallel()

	s := scalarSchema(t)

	tests := []struct {
		name, query string
		vars        map[string]any
		want        string
	}{
		{
			"input object literal with defaults", `{ echo(in: {title: "t"}) { title tags color when } }`, nil,
			`{"data":{"echo":{"title":"t","tags":[],"color":"RED","when":null}}}`,
		},
		{
			"input object variable",
			`query ($in: PostInput!) { echo(in: $in) { title tags color when meta } }`,
			map[string]any{
				"in": map[string]any{
					"title": "v",
					"tags":  "single",
					"color": "GREEN",
					"when":  "2026-10-16T10:00:00Z",
					"meta":  map[string]any{"k": []any{json.Number("1")}},
				},
			},
			`{"data":{"echo":{"title":"v","tags":["single"],"color":"GREEN","when":"2026-10-16T10:00:00Z","meta":{"k":[1]}}}}`,
		},
		{
			"custom scalar literal",
			`{ at(when: "2026-01-01T00:00:00Z") }`,
			nil,
			`{"data":{"at":"2026-01-01T01:00:00Z"}}`,
		},
		{
			"custom scalar invalid literal",
			`{ at(when: 5) }`,
			nil,
			`{"errors":[{"message":"Argument \"when\" has invalid value 5: DateTime: must be an RFC 3339 string.","locations":[{"line":1,"column":12}],"extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`,
		},
		{
			"int output overflow",
			`{ big }`,
			nil,
			`{"errors":[{"message":"Internal server error","locations":[{"line":1,"column":3}],"path":["big"],"extensions":{"code":"INTERNAL_SERVER_ERROR"}}],"data":{"big":null}}`,
		},
		{
			"union via Typenamer and Object",
			`{ results { __typename ... on Post { title } ... on Problem { reason } } }`,
			nil,
			`{"data":{"results":[{"__typename":"Post","title":"a"},{"__typename":"Problem","reason":"b"}]}}`,
		},
		{
			"missing required variable",
			`query ($in: PostInput!) { echo(in: $in) { title } }`,
			nil,
			`{"errors":[{"message":"Variable \"$in\" of required type \"PostInput!\" was not provided.","locations":[{"line":1,"column":8}],"extensions":{"code":"BAD_USER_INPUT"}}]}`,
		},
		{
			"variable unknown field",
			`query ($in: PostInput!) { echo(in: $in) { title } }`,
			map[string]any{"in": map[string]any{"title": "x", "nope": 1}},
			`{"errors":[{"message":"Variable \"$in\" got invalid value {\"nope\":1,\"title\":\"x\"}; field \"nope\" is not defined by type \"PostInput\".","locations":[{"line":1,"column":8}],"extensions":{"code":"BAD_USER_INPUT"}}]}`,
		},
		{
			"variable int range",
			`query ($c: Int!) { status(code: $c) }`,
			map[string]any{"c": json.Number("3000000000")},
			`{"errors":[{"message":"Variable \"$c\" got invalid value 3000000000; int cannot represent non 32-bit signed integer value: 3000000000.","locations":[{"line":1,"column":8}],"extensions":{"code":"BAD_USER_INPUT"}}]}`,
		},
		{
			"validate tags", `{ validated(email: "nope", age: 3) }`, nil,
			`{"errors":[{"message":"Invalid input","locations":[{"line":1,"column":3}],"path":["validated"],
			  "extensions":{"code":"BAD_USER_INPUT","fields":{"age":["The age field must be at least 18."],"email":["The email field must be a valid email address."]}}}],"data":{"validated":null}}`,
		},
		{
			"unauthenticated",
			`{ status(code: 401) }`,
			nil,
			`{"errors":[{"message":"Unauthorized","locations":[{"line":1,"column":3}],"path":["status"],"extensions":{"code":"UNAUTHENTICATED"}}],"data":{"status":null}}`,
		},
		{
			"validation error",
			`{ status(code: 422) }`,
			nil,
			`{"errors":[{"message":"Invalid input","locations":[{"line":1,"column":3}],"path":["status"],"extensions":{"code":"BAD_USER_INPUT","fields":{"code":["is odd"]}}}],"data":{"status":null}}`,
		},
		{
			"extensions from error",
			`{ status(code: 429) }`,
			nil,
			`{"errors":[{"message":"rate limited","locations":[{"line":1,"column":3}],"path":["status"],"extensions":{"code":"TOO_MANY_REQUESTS","retryAfter":30}}],"data":{"status":null}}`,
		},
		{
			"internal error is masked",
			`{ status(code: 500) }`,
			nil,
			`{"errors":[{"message":"Internal server error","locations":[{"line":1,"column":3}],"path":["status"],"extensions":{"code":"INTERNAL_SERVER_ERROR"}}],"data":{"status":null}}`,
		},
		{
			"panic is masked",
			`{ status(code: 1) }`,
			nil,
			`{"errors":[{"message":"Internal server error","locations":[{"line":1,"column":3}],"path":["status"],"extensions":{"code":"INTERNAL_SERVER_ERROR"}}],"data":{"status":null}}`,
		},
		{
			"syntax error",
			`{ status(code: 1 }`,
			nil,
			`{"errors":[{"message":"Syntax Error: Expected Name, found \"}\".","locations":[{"line":1,"column":18}],"extensions":{"code":"GRAPHQL_PARSE_FAILED"}}]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			jsonEqual(t, tt.want, run(t, s, tt.query, tt.vars))
		})
	}
}

func TestOperationSelection(t *testing.T) {
	t.Parallel()

	s := nullSchema(t)
	doc := `query A { nullable { ok } } query B { list { ok } }`

	tests := []struct{ op, want string }{
		{"A", `{"data":{"nullable":{"ok":"ok"}}}`},
		{"B", `{"data":{"list":[{"ok":"ok"},{"ok":"ok"}]}}`},
		{
			"",
			`{"errors":[{"message":"Must provide operation name if query contains multiple operations.","extensions":{"code":"OPERATION_RESOLUTION_FAILURE"}}]}`,
		},
		{
			"C",
			`{"errors":[{"message":"Unknown operation named \"C\".","extensions":{"code":"OPERATION_RESOLUTION_FAILURE"}}]}`,
		},
	}

	for _, tt := range tests {
		b, err := json.Marshal(s.Execute(t.Context(), Request{Query: doc, OperationName: tt.op}))
		if err != nil {
			t.Fatal(err)
		}

		jsonEqual(t, tt.want, string(b))
	}
}

func TestNewErrors(t *testing.T) {
	t.Parallel()

	q := Query("a", func(context.Context, NoArgs) (int, error) { return 1, nil })

	tests := []struct {
		name, sdl string
		opts      []Option
		want      string
	}{
		{"syntax", `type Query { a: }`, nil, "graphql: parse SDL: Syntax Error: Expected Name, found \"}\". (1:17)"},
		{"no query", `type Foo { a: Int }`, nil, "graphql: the schema has no query root type"},
		{"unknown type", `type Query { a: Nope }`, nil, `graphql: Query.a: graphql: unknown type "Nope" (1:17)`},
		{
			"duplicate type",
			`type Query { a: Int } type Query { b: Int }`,
			nil,
			`graphql: type "Query" is defined more than once (1:23)`,
		},
		{
			"reserved name",
			`type __Foo { a: Int } type Query { a: Int }`,
			nil,
			`graphql: type "__Foo": names starting with "__" are reserved (1:1)`,
		},
		{
			"input as output",
			`input I { a: Int } type Query { a: I }`,
			[]Option{q},
			`graphql: Query.a: I is not an output type`,
		},
		{
			"output as input",
			`type O { a: Int } type Query { a(o: O): Int }`,
			[]Option{q},
			`graphql: Query.a.o: O is not an input type`,
		},
		{
			"missing interface field",
			`interface I { id: ID } type T implements I { a: Int } type Query { a: T }`,
			[]Option{q},
			`graphql: T.id required by interface I is missing`,
		},
		{
			"wrong interface type",
			`interface I { id: ID } type T implements I { id: Int } type Query { a: T }`,
			[]Option{q},
			`graphql: T.id has type Int, interface I requires ID (or a subtype)`,
		},
		{
			"union of scalar",
			`union U = Int type Query { a: U }`,
			[]Option{q},
			`graphql: union U member "Int" is not an object type`,
		},
		{
			"bad default",
			`type Query { a(n: Int = "x"): Int }`,
			[]Option{q},
			`graphql: Query.a.n: invalid default value: int cannot represent non-integer value: "x"`,
		},
		{
			"unbound root field",
			`type Query { a: Int b: Int }`,
			[]Option{q},
			`graphql: Query.b has no resolver (bind it with graphql.Query/Mutation/Field)`,
		},
		{
			"binding unknown field",
			`type Query { a: Int }`,
			[]Option{q, Field("Query", "zzz", func(context.Context, any, NoArgs) (int, error) { return 0, nil })},
			`graphql: binding Query.zzz: no such field in the schema`,
		},
		{"binding unknown arg", `type Query { a: Int }`, []Option{Query("a", func(context.Context, struct {
			X int `json:"x"`
		},
		) (int, error) {
			return 0, nil
		})}, `graphql: binding Query.a: Go argument "x" is not an argument of the field`},
		{"double binding", `type Query { a: Int }`, []Option{q, q}, `graphql: Query.a is bound more than once`},
		{
			"unknown scalar",
			`type Query { a: Int }`,
			[]Option{q, Scalar("Nope", ScalarFuncs{})},
			`graphql: Scalar("Nope"): no such scalar in the schema`,
		},
		{
			"replace builtin",
			`type Query { a: Int }`,
			[]Option{q, Scalar("Int", ScalarFuncs{})},
			`graphql: Scalar("Int"): built-in scalars cannot be replaced`,
		},
		{
			"extend unknown",
			`type Query { a: Int } extend type Nope { b: Int }`,
			[]Option{q},
			`graphql: cannot extend unknown type "Nope" (1:30)`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := New(tt.sdl, tt.opts...)
			if err == nil || err.Error() != tt.want {
				t.Errorf("New(%q) = %v, want %q", tt.sdl, err, tt.want)
			}
		})
	}
}

func TestSchemaExtensions(t *testing.T) {
	t.Parallel()

	s, err := New(`type Query { a: Int } extend type Query { b: Int }`,
		Query("a", func(context.Context, NoArgs) (int, error) { return 1, nil }),
		Query("b", func(context.Context, NoArgs) (int, error) { return 2, nil }),
	)
	if err != nil {
		t.Fatal(err)
	}

	jsonEqual(t, `{"data":{"a":1,"b":2}}`, run(t, s, `{ a b }`, nil))
}

func TestIntrospectionGolden(t *testing.T) {
	t.Parallel()

	s := starWarsSchema(t)
	resp := s.Execute(t.Context(), Request{Query: IntrospectionQuery})

	if len(resp.Errors) > 0 {
		t.Fatalf("introspection errors = %v", resp.Errors)
	}

	got, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		t.Fatal(err)
	}

	golden := filepath.Join("testdata", "introspection.golden.json")
	if *update {
		if err := os.WriteFile(golden, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(strings.ReplaceAll(string(want), "\r\n", "\n"), string(got)); diff != "" {
		t.Errorf("introspection (-want +got):\n%s", diff)
	}
}

func TestIntrospectionTypeLookup(t *testing.T) {
	t.Parallel()

	s := starWarsSchema(t)
	got := run(
		t,
		s,
		`{ __type(name: "Droid") { name kind interfaces { name } fields { name type { kind ofType { name } } } }
		missing: __type(name: "Nope") { name } }`,
		nil,
	)

	jsonEqual(t, `{"data":{"__type":{"name":"Droid","kind":"OBJECT","interfaces":[{"name":"Character"}],"fields":[
		{"name":"id","type":{"kind":"NON_NULL","ofType":{"name":"ID"}}},
		{"name":"name","type":{"kind":"SCALAR","ofType":null}},
		{"name":"friends","type":{"kind":"LIST","ofType":{"name":"Character"}}},
		{"name":"appearsIn","type":{"kind":"LIST","ofType":{"name":"Episode"}}},
		{"name":"secretBackstory","type":{"kind":"SCALAR","ofType":null}},
		{"name":"primaryFunction","type":{"kind":"SCALAR","ofType":null}}]},"missing":null}}`, got)
}

// conflictError is a 4xx StatusCoder wrapping a driver error, like
// orm.ErrConflict.
type conflictError struct{ cause error }

func (e *conflictError) Error() string   { return "unique constraint violated: " + e.cause.Error() }
func (e *conflictError) Unwrap() error   { return e.cause }
func (e *conflictError) HTTPStatus() int { return http.StatusConflict }

func TestSecurity_A10_GraphQLClientErrorsMasked(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("create user: %w", &conflictError{cause: errors.New(`pq: duplicate key "users_email_key"`)})

	ge := fromResolverError(t.Context(), err, false)
	if strings.Contains(ge.Message, "users_email_key") || ge.Message != "Conflict" || ge.Code() != CodeConflict {
		t.Errorf(
			"fromResolverError(conflict) = %q (%s), want %q without driver text",
			ge.Message,
			ge.Code(),
			"Conflict",
		)
	}

	he := fromResolverError(
		t.Context(),
		fmt.Errorf("wrap: %w", errs.New(http.StatusNotFound, "Post not found.")),
		false,
	)
	if he.Message != "Post not found." {
		t.Errorf("fromResolverError(HTTPError) = %q, want its client message", he.Message)
	}
}

func TestExecute_LocalizesKeyedErrors(t *testing.T) {
	t.Parallel()

	s, err := New(`type Query { post: String }`,
		Query("post", func(context.Context, NoArgs) (*string, error) {
			return nil, fmt.Errorf("load: %w",
				errs.NewKey(http.StatusNotFound, "post.missing", "Post not found.", map[string]any{"id": 7}))
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	tr := func(key string, params map[string]any) (string, bool) {
		if key == "post.missing" {
			return fmt.Sprintf("Post %v não encontrado.", params["id"]), true
		}

		return "", false
	}

	tests := map[string]struct {
		ctx  context.Context
		want string
	}{
		"translator in ctx": {errs.WithTranslator(t.Context(), tr), "Post 7 não encontrado."},
		"no translator":     {t.Context(), "Post not found."},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			resp := s.Execute(tt.ctx, Request{Query: `{ post }`})
			if len(resp.Errors) != 1 || resp.Errors[0].Message != tt.want || resp.Errors[0].Code() != CodeNotFound {
				t.Errorf("Execute errors = %v, want one %s %q", resp.Errors, CodeNotFound, tt.want)
			}
		})
	}
}

func TestSecurity_A10_GraphQLServerHTTPErrorsMasked(t *testing.T) {
	t.Parallel()

	leaky := func(string, map[string]any) (string, bool) { return "dsn postgres://app:hunter2@db", true }
	ctx := errs.WithTranslator(t.Context(), leaky)

	secret := errors.New("db: password=hunter2")
	_, issues := z.Struct[struct {
		Name string `json:"name"`
	}](z.Shape{"name": z.String().Refine(func(string) error { return secret })}).Parse(map[string]any{"name": "x"})

	tests := map[string]error{
		"500 message":      errs.New(http.StatusInternalServerError, "query failed: password=hunter2"),
		"503 keyed":        errs.NewKey(http.StatusServiceUnavailable, "db.down", "redis hunter2 down", nil),
		"500 wraps issues": errs.Wrap(http.StatusInternalServerError, issues),
		"500 wraps fields": errs.Wrap(http.StatusInternalServerError, errs.NewValidation().Add("name", "hunter2")),
	}

	for name, err := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ge := fromResolverError(ctx, err, false)
			if ge.Message != internalMessage || ge.Code() != CodeInternal || ge.Extensions["fields"] != nil {
				t.Errorf("fromResolverError(%v) = %q %s %v, want %q %s without fields",
					err, ge.Message, ge.Code(), ge.Extensions, internalMessage, CodeInternal)
			}
		})
	}
}
