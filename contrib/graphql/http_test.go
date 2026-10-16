package graphql_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/cache"
	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/contrib/graphql"
	"github.com/AlexGalhardo/vukem/router"
)

const counterSDL = `
type Query { count: Int! hello(name: String = "world"): String! secret: String }
type Mutation { increment(by: Int! = 1): Int! }
`

func counterSchema(t *testing.T) *graphql.Schema {
	t.Helper()

	n := 0

	type byArgs struct {
		By int `json:"by"`
	}

	s, err := graphql.New(counterSDL,
		graphql.Query("count", func(context.Context, graphql.NoArgs) (int, error) { return n, nil }),
		graphql.Query("hello", func(_ context.Context, a struct {
			Name string `json:"name"`
		},
		) (string, error) {
			return "hello " + a.Name, nil
		}),
		graphql.Query("secret", func(context.Context, graphql.NoArgs) (*string, error) {
			return nil, errors.New("pq: password authentication failed for user admin")
		}),
		graphql.Mutation("increment", func(_ context.Context, a byArgs) (int, error) {
			n += a.By
			return n, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	return s
}

type reply struct {
	status int
	header http.Header
	body   string
}

func do(t *testing.T, h http.Handler, method, target, contentType, accept, body string) reply {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), method, target, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	if accept != "" {
		req.Header.Set("Accept", accept)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return reply{status: rec.Code, header: rec.Header(), body: rec.Body.String()}
}

func post(t *testing.T, h http.Handler, body string) reply {
	t.Helper()
	return do(t, h, http.MethodPost, "/graphql", "application/json", "", body)
}

func TestHandler(t *testing.T) {
	t.Parallel()

	const (
		jsonCT = "application/json"
		gqlCT  = "application/graphql-response+json"
	)

	tests := []struct {
		name                       string
		method, target, ct, accept string
		body                       string
		wantStatus                 int
		wantBody                   string // substring
		wantCT                     string
	}{
		{
			"post query",
			"POST",
			"/graphql",
			jsonCT,
			"",
			`{"query":"{ hello }"}`,
			200,
			`{"data":{"hello":"hello world"}}`,
			jsonCT,
		},
		{
			"post variables",
			"POST",
			"/graphql",
			"application/json; charset=utf-8",
			"",
			`{"query":"query($n: String){ hello(name: $n) }","variables":{"n":"go"}}`,
			200,
			`"hello go"`,
			jsonCT,
		},
		{
			"post operation name",
			"POST",
			"/graphql",
			jsonCT,
			"",
			`{"query":"query A { count } query B { hello }","operationName":"B"}`,
			200,
			`{"data":{"hello":"hello world"}}`,
			jsonCT,
		},
		{
			"post mutation",
			"POST",
			"/graphql",
			jsonCT,
			"",
			`{"query":"mutation { increment(by: 2) }"}`,
			200,
			`{"data":{"increment":2}}`,
			jsonCT,
		},
		{
			"get query",
			"GET",
			"/graphql?query=" + url.QueryEscape(
				`query($n: String){ hello(name: $n) }`,
			) + "&variables=" + url.QueryEscape(
				`{"n":"get"}`,
			),
			"",
			"",
			"",
			200,
			`"hello get"`,
			jsonCT,
		},
		{
			"get bad variables",
			"GET",
			"/graphql?query=%7Bcount%7D&variables=nope",
			"",
			"",
			"",
			400,
			`The variables parameter must be a JSON object.`,
			jsonCT,
		},
		{
			"graphql response media type",
			"POST",
			"/graphql",
			jsonCT,
			gqlCT,
			`{"query":"{ hello }"}`,
			200,
			`"hello world"`,
			gqlCT,
		},
		{
			"validation error legacy json is 200",
			"POST",
			"/graphql",
			jsonCT,
			jsonCT,
			`{"query":"{ nope }"}`,
			200,
			`GRAPHQL_VALIDATION_FAILED`,
			jsonCT,
		},
		{
			"validation error graphql media type is 400",
			"POST",
			"/graphql",
			jsonCT,
			gqlCT,
			`{"query":"{ nope }"}`,
			400,
			`Cannot query field \"nope\" on type \"Query\".`,
			gqlCT,
		},
		{"parse error is 400", "POST", "/graphql", jsonCT, gqlCT, `{"query":"{"}`, 400, `GRAPHQL_PARSE_FAILED`, gqlCT},
		{"accept wildcard", "POST", "/graphql", jsonCT, "*/*", `{"query":"{ count }"}`, 200, `"count"`, jsonCT},
		{
			"not acceptable",
			"POST",
			"/graphql",
			jsonCT,
			"text/html",
			`{"query":"{ count }"}`,
			406,
			`Not Acceptable`,
			"text/plain",
		},
		{
			"wrong content type",
			"POST",
			"/graphql",
			"text/plain",
			"",
			`{"query":"{ count }"}`,
			415,
			`Content-Type must be application/json.`,
			jsonCT,
		},
		{"bad json", "POST", "/graphql", jsonCT, "", `{"query":`, 400, `Body must be a JSON object`, jsonCT},
		{
			"batch array",
			"POST",
			"/graphql",
			jsonCT,
			"",
			`[{"query":"{ count }"}]`,
			400,
			`Batched requests are not supported.`,
			jsonCT,
		},
		{"missing query", "POST", "/graphql", jsonCT, "", `{}`, 400, `Must provide query string.`, jsonCT},
		{"put not allowed", "PUT", "/graphql", jsonCT, "", `{}`, 405, `Method Not Allowed`, "text/plain"},
		{
			"persisted query disabled",
			"POST",
			"/graphql",
			jsonCT,
			"",
			`{"extensions":{"persistedQuery":{"version":1,"sha256Hash":"` + strings.Repeat("a", 64) + `"}}}`,
			200,
			`PERSISTED_QUERY_NOT_SUPPORTED`,
			jsonCT,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := counterSchema(t).Handler(graphql.HandlerConfig{})
			r := do(t, h, tt.method, tt.target, tt.ct, tt.accept, tt.body)

			if r.status != tt.wantStatus || !strings.Contains(r.body, tt.wantBody) ||
				!strings.HasPrefix(r.header.Get("Content-Type"), tt.wantCT) {
				t.Errorf("%s %s = %d %q (%s), want %d containing %q (%s)", tt.method, tt.target, r.status, r.body,
					r.header.Get("Content-Type"), tt.wantStatus, tt.wantBody, tt.wantCT)
			}
		})
	}
}

func TestHandlerBodyLimit(t *testing.T) {
	t.Parallel()

	h := counterSchema(t).Handler(graphql.HandlerConfig{MaxBodyBytes: 64})
	r := post(t, h, `{"query":"{ hello(name: \"`+strings.Repeat("x", 100)+`\") }"}`)

	if r.status != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized body = %d %s, want 413", r.status, r.body)
	}
}

func TestSecurity_A01_GraphQLMutationsRequirePOST(t *testing.T) {
	t.Parallel()

	h := counterSchema(t).Handler(graphql.HandlerConfig{})

	r := do(t, h, http.MethodGet, "/graphql?query="+url.QueryEscape("mutation { increment }"), "", "", "")
	if r.status != http.StatusMethodNotAllowed || r.header.Get("Allow") != "POST" {
		t.Errorf("GET mutation = %d (Allow %q), want 405 Allow: POST", r.status, r.header.Get("Allow"))
	}

	if r := post(t, h, `{"query":"{ count }"}`); !strings.Contains(r.body, `"count":0`) {
		t.Errorf("count after GET mutation = %s, want 0", r.body)
	}

	h = counterSchema(t).Handler(graphql.HandlerConfig{DisableGET: true})
	if r := do(
		t,
		h,
		http.MethodGet,
		"/graphql?query=%7Bcount%7D",
		"",
		"",
		"",
	); r.status != http.StatusMethodNotAllowed {
		t.Errorf("GET with DisableGET = %d, want 405", r.status)
	}
}

func TestSecurity_A10_GraphQLInternalErrorsMasked(t *testing.T) {
	t.Parallel()

	r := post(t, counterSchema(t).Handler(graphql.HandlerConfig{}), `{"query":"{ secret }"}`)

	if strings.Contains(r.body, "password") || !strings.Contains(r.body, `"message":"Internal server error"`) ||
		!strings.Contains(r.body, `"code":"INTERNAL_SERVER_ERROR"`) {
		t.Errorf("internal error response = %s, want masked message", r.body)
	}
}

func apq(hash string) string {
	return `"extensions":{"persistedQuery":{"version":1,"sha256Hash":"` + hash + `"}}`
}

func TestPersistedQueries(t *testing.T) {
	t.Parallel()

	const q = "{ hello }"

	hash := graphql.Hash(q)

	stores := map[string]func() graphql.QueryStore{
		"memory": func() graphql.QueryStore { return graphql.NewMemoryStore(0) },
		"cache": func() graphql.QueryStore {
			return graphql.NewCacheStore(cache.New(cache.NewMemory(100), "test:"), 0)
		},
	}

	for name, store := range stores {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h := counterSchema(t).Handler(graphql.HandlerConfig{Persisted: &graphql.PersistedQueries{Store: store()}})

			if r := post(t, h, `{`+apq(hash)+`}`); !strings.Contains(r.body, `"message":"PersistedQueryNotFound"`) ||
				!strings.Contains(r.body, graphql.CodePersistedNotFound) {
				t.Fatalf("unknown hash = %s, want PersistedQueryNotFound", r.body)
			}

			if r := post(
				t,
				h,
				`{"query":"{ count }",`+apq(hash)+`}`,
			); !strings.Contains(
				r.body,
				"provided sha does not match query",
			) {
				t.Errorf("mismatched hash = %s", r.body)
			}

			if r := post(t, h, `{"query":"`+q+`",`+apq(hash)+`}`); !strings.Contains(r.body, `"hello world"`) {
				t.Fatalf("register = %s", r.body)
			}

			if r := post(t, h, `{`+apq(strings.ToUpper(hash))+`}`); !strings.Contains(r.body, `"hello world"`) {
				t.Errorf("hash only after register = %s", r.body)
			}

			get := "/graphql?extensions=" + url.QueryEscape(`{"persistedQuery":{"version":1,"sha256Hash":"`+hash+`"}}`)
			if r := do(t, h, http.MethodGet, get, "", "", ""); !strings.Contains(r.body, `"hello world"`) {
				t.Errorf("GET hash only = %s", r.body)
			}

			for _, bad := range []string{
				`{"extensions":{"persistedQuery":{"version":2,"sha256Hash":"` + hash + `"}}}`,
				`{"extensions":{"persistedQuery":{"version":1,"sha256Hash":"zz"}}}`,
				`{"extensions":{"persistedQuery":"x"}}`,
			} {
				if r := post(t, h, bad); !strings.Contains(r.body, graphql.CodeBadRequest) {
					t.Errorf("POST %s = %s, want BAD_REQUEST", bad, r.body)
				}
			}
		})
	}
}

func TestMemoryStoreEvictsOldest(t *testing.T) {
	t.Parallel()

	s := graphql.NewMemoryStore(2)
	ctx := t.Context()

	for _, q := range []string{"a", "b", "c"} {
		if err := s.Put(ctx, graphql.Hash(q), q); err != nil {
			t.Fatal(err)
		}
	}

	if _, ok, _ := s.Get(ctx, graphql.Hash("a")); ok {
		t.Error("oldest entry kept, want evicted")
	}

	if q, ok, _ := s.Get(ctx, graphql.Hash("c")); !ok || q != "c" {
		t.Errorf("Get(c) = %q, %v", q, ok)
	}
}

func TestSecurity_A06_GraphQLAllowlistOnly(t *testing.T) {
	t.Parallel()

	allowed := "query Count { count }"
	h := counterSchema(t).Handler(graphql.HandlerConfig{Persisted: &graphql.PersistedQueries{
		Store: graphql.NewAllowlist(allowed), AllowlistOnly: true,
	}})

	tests := []struct{ name, body, want string }{
		{"listed hash", `{` + apq(graphql.Hash(allowed)) + `}`, `{"data":{"count":0}}`},
		{"listed document", `{"query":"` + allowed + `"}`, `{"data":{"count":0}}`},
		{"unlisted document", `{"query":"{ hello }"}`, graphql.CodePersistedRequired},
		{"unlisted hash", `{` + apq(graphql.Hash("{ hello }")) + `}`, graphql.CodePersistedRequired},
		{
			"unlisted document with its hash is not registered",
			`{"query":"{ hello }",` + apq(graphql.Hash("{ hello }")) + `}`,
			graphql.CodePersistedRequired,
		},
	}

	for _, tt := range tests {
		if r := post(t, h, tt.body); !strings.Contains(r.body, tt.want) {
			t.Errorf("%s = %s, want %s", tt.name, r.body, tt.want)
		}
	}

	if r := post(
		t,
		h,
		`{`+apq(graphql.Hash("{ hello }"))+`}`,
	); !strings.Contains(
		r.body,
		graphql.CodePersistedRequired,
	) {
		t.Errorf("hash after attempted registration = %s, want still rejected", r.body)
	}

	// An allowlist without a store fails closed.
	h = counterSchema(t).Handler(graphql.HandlerConfig{Persisted: &graphql.PersistedQueries{AllowlistOnly: true}})
	if r := post(t, h, `{"query":"{ count }"}`); !strings.Contains(r.body, graphql.CodePersistedRequired) {
		t.Errorf("empty allowlist = %s, want rejected", r.body)
	}
}

func newApp(t *testing.T, env map[string]string, cfg graphql.Config) http.Handler {
	t.Helper()

	m := map[string]string{"APP_KEY": config.GenerateKey(), "APP_ENV": "development", "APP_NAME": "Shop"}
	maps.Copy(m, env)

	s := counterSchema(t)

	app, err := vukem.New(
		vukem.WithSource(config.MapSource(m)),
		vukem.WithLogger(slog.New(slog.DiscardHandler)),
		vukem.WithStdout(io.Discard),
		vukem.WithRoutes(func(r *router.Router, app *vukem.App) error {
			if err := vukem.DefaultMiddleware(app, r); err != nil {
				return err
			}

			graphql.Serve(app, r, "/graphql", s, cfg)

			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	h, err := app.Handler()
	if err != nil {
		t.Fatal(err)
	}

	return h
}

func introspect(t *testing.T, h http.Handler) reply {
	t.Helper()

	body, err := json.Marshal(map[string]string{"query": graphql.IntrospectionQuery})
	if err != nil {
		t.Fatal(err)
	}

	return post(t, h, string(body))
}

func TestServeDevelopment(t *testing.T) {
	t.Parallel()

	h := newApp(t, nil, graphql.Config{})

	if r := post(t, h, `{"query":"{ hello }"}`); r.status != 200 || !strings.Contains(r.body, "hello world") {
		t.Fatalf("POST /graphql = %d %s", r.status, r.body)
	}

	if r := introspect(t, h); !strings.Contains(r.body, `"queryType":{"name":"Query"}`) {
		t.Errorf("introspection in development = %.200s", r.body)
	}

	r := do(t, h, http.MethodGet, "/graphql/explorer", "", "text/html", "")
	if r.status != 200 || !strings.Contains(r.body, "<title>GraphQL Explorer</title>") {
		t.Fatalf("GET /graphql/explorer = %d %.200s", r.status, r.body)
	}

	m := regexp.MustCompile(`'nonce-([^']+)'`).FindStringSubmatch(r.header.Get("Content-Security-Policy"))
	if len(m) < 2 || strings.Count(r.body, `nonce="`+m[1]+`"`) != 2 {
		t.Errorf("explorer script and style do not carry the CSP nonce: %q", r.header.Get("Content-Security-Policy"))
	}

	if !strings.Contains(r.body, `const endpoint = "/graphql";`) {
		t.Errorf("explorer endpoint not embedded as a JS string")
	}
}

func TestSecurity_A02_GraphQLExplorerOffInProduction(t *testing.T) {
	t.Parallel()

	prod := map[string]string{"APP_ENV": "production"}
	h := newApp(t, prod, graphql.Config{})

	if r := do(t, h, http.MethodGet, "/graphql/explorer", "", "text/html", ""); r.status != http.StatusNotFound {
		t.Errorf("production GET /graphql/explorer = %d, want 404", r.status)
	}

	if r := introspect(t, h); !strings.Contains(r.body, "introspection is not allowed") {
		t.Errorf("production introspection = %.200s, want rejected", r.body)
	}

	if r := post(t, h, `{"query":"{ __typename hello }"}`); !strings.Contains(r.body, `"__typename":"Query"`) {
		t.Errorf("production __typename = %s, want allowed", r.body)
	}

	h = newApp(t, prod, graphql.Config{InsecureExplorerInProduction: true, InsecureIntrospectionInProduction: true})
	if r := do(t, h, http.MethodGet, "/graphql/explorer", "", "text/html", ""); r.status != http.StatusOK {
		t.Errorf("opted-in production explorer = %d, want 200", r.status)
	}

	if r := introspect(t, h); !strings.Contains(r.body, `"queryType":{"name":"Query"}`) {
		t.Errorf("opted-in production introspection = %.200s", r.body)
	}

	h = newApp(t, nil, graphql.Config{DisableExplorer: true})
	if r := do(t, h, http.MethodGet, "/graphql/explorer", "", "text/html", ""); r.status != http.StatusNotFound {
		t.Errorf("DisableExplorer = %d, want 404", r.status)
	}
}
