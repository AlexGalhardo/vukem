package graphql_test

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/AlexGalhardo/vukem/contrib/graphql"
	"github.com/AlexGalhardo/vukem/errs"
)

const exampleSDL = `
type Query {
	account(id: ID!): Account
}

type Account {
	id: ID!
	name: String!
	greeting(punctuation: String = "!"): String!
}
`

type Account struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

var accounts = map[string]*Account{"1": {ID: "1", Name: "Ada"}}

func exampleSchema() (*graphql.Schema, error) {
	return graphql.New(exampleSDL,
		graphql.Query("account", func(_ context.Context, args struct {
			ID string `json:"id"`
		},
		) (*Account, error) {
			u, ok := accounts[args.ID]
			if !ok {
				return nil, errs.New(http.StatusNotFound, "account not found")
			}

			return u, nil
		}),
		// Fields without a resolver read the struct's json-tagged fields.
		graphql.Field("Account", "greeting", func(_ context.Context, u *Account, args struct {
			Punctuation string `json:"punctuation"`
		},
		) (string, error) {
			return "Hello, " + u.Name + args.Punctuation, nil
		}),
	)
}

func Example() {
	schema, err := exampleSchema()
	if err != nil {
		log.Fatal(err)
	}

	resp := schema.Execute(context.Background(), graphql.Request{
		Query:     `query ($id: ID!) { account(id: $id) { name greeting(punctuation: "?") } }`,
		Variables: map[string]any{"id": "1"},
	})

	fmt.Println(string(resp.Data), len(resp.Errors))
	// Output: {"account":{"name":"Ada","greeting":"Hello, Ada?"}} 0
}

// Handler serves the schema over HTTP; graphql.Serve mounts it on a Vukem
// router together with the explorer page.
func ExampleSchema_Handler() {
	schema, err := exampleSchema()
	if err != nil {
		log.Fatal(err)
	}

	h := schema.Handler(graphql.HandlerConfig{DisableIntrospection: true})

	req := httptest.NewRequest(
		http.MethodPost,
		"/graphql",
		strings.NewReader(`{"query":"{ account(id: \"1\") { id } }"}`),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	fmt.Println(rec.Code, strings.TrimSpace(rec.Body.String()))
	// Output: 200 {"data":{"account":{"id":"1"}}}
}
