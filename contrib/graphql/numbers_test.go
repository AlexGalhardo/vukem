package graphql_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/AlexGalhardo/vukem/contrib/graphql"
)

func echoSchema(t *testing.T) *graphql.Schema {
	t.Helper()

	s, err := graphql.New(`type Query { echo(id: ID!): ID! }`,
		graphql.Query("echo", func(_ context.Context, a struct {
			ID string `json:"id"`
		},
		) (string, error) {
			return a.ID, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	return s
}

func TestServeWSKeepsLargeNumericIDs(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		c := dial(t, echoSchema(t), graphql.WSConfig{}, graphql.WSProtocol)
		c.init(nil)

		// 2^53 + 1: a float64 would round it to ...992.
		c.send(
			`{"id":"q","type":"subscribe","payload":{"query":"query($id: ID!) { echo(id: $id) }","variables":{"id":9007199254740993}}}`,
		)

		c.wantNext("q", `{"echo":"9007199254740993"}`)
		c.wantComplete("q")
	})
}

func TestSecurity_A03_GraphQLRefusesIDOutOfInt64Range(t *testing.T) {
	t.Parallel()

	const query = `"query":"query($id: ID!) { echo(id: $id) }","variables":{"id":9223372036854775808}`

	t.Run("http", func(t *testing.T) {
		t.Parallel()

		r := post(t, echoSchema(t).Handler(graphql.HandlerConfig{}), `{`+query+`}`)
		if strings.Contains(r.body, "-9223372036854775808") || !strings.Contains(r.body, graphql.CodeBadUserInput) {
			t.Errorf("2^63 as an ID over HTTP = %s, want a %s error", r.body, graphql.CodeBadUserInput)
		}
	})

	t.Run("websocket", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			c := dial(t, echoSchema(t), graphql.WSConfig{}, graphql.WSProtocol)
			c.init(nil)
			c.send(`{"id":"q","type":"subscribe","payload":{` + query + `}}`)

			if m := c.recv(); m.Type != "error" || strings.Contains(string(m.Payload), "-9223372036854775808") {
				t.Errorf("2^63 as an ID over WebSocket = %s %s, want an error", m.Type, m.Payload)
			}
		})
	})
}

func TestHandlerSubscriptionOverHTTPIsRequestError(t *testing.T) {
	t.Parallel()

	s, _ := subApp(t)
	h := s.Handler(graphql.HandlerConfig{})
	body := `{"query":"subscription { countdown(from: 1) }"}`

	tests := []struct {
		accept string
		want   int
	}{
		{"application/graphql-response+json", http.StatusBadRequest},
		{"application/json", http.StatusOK},
	}

	for _, tt := range tests {
		r := do(t, h, http.MethodPost, "/graphql", "application/json", tt.accept, body)
		if r.status != tt.want || !strings.Contains(r.body, "errors") {
			t.Errorf(
				"subscription over HTTP with Accept %s = %d %s, want %d with errors",
				tt.accept,
				r.status,
				r.body,
				tt.want,
			)
		}
	}
}
