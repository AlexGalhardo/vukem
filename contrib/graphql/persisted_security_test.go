package graphql_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/contrib/graphql"
)

func TestSecurity_A06_PersistedQueryStoredOnlyWhenValid(t *testing.T) {
	t.Parallel()

	register := func(query string) string {
		return `{"query":"` + query + `",` + apq(graphql.Hash(query)) + `}`
	}

	tests := []struct {
		name   string
		method string
		query  string
		want   string
		stored bool
	}{
		{"syntax error", http.MethodPost, "{ hello", "errors", false},
		{"validation error", http.MethodPost, "{ nope }", "errors", false},
		{"valid over GET", http.MethodGet, "{ hello }", `"hello world"`, false},
		{"valid over POST", http.MethodPost, "{ hello }", `"hello world"`, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := graphql.NewMemoryStore(0)
			h := counterSchema(t).Handler(graphql.HandlerConfig{Persisted: &graphql.PersistedQueries{Store: store}})

			var r reply
			if tt.method == http.MethodGet {
				target := "/graphql?query=" + url.QueryEscape(tt.query) + "&extensions=" +
					url.QueryEscape(`{"persistedQuery":{"version":1,"sha256Hash":"`+graphql.Hash(tt.query)+`"}}`)
				r = do(t, h, http.MethodGet, target, "", "", "")
			} else {
				r = post(t, h, register(tt.query))
			}

			if !strings.Contains(r.body, tt.want) {
				t.Errorf("response = %s, want %s", r.body, tt.want)
			}

			if _, ok, _ := store.Get(context.Background(), graphql.Hash(tt.query)); ok != tt.stored {
				t.Errorf("stored = %v, want %v", ok, tt.stored)
			}

			if tt.stored {
				if r := post(t, h, `{`+apq(graphql.Hash(tt.query))+`}`); !strings.Contains(r.body, `"hello world"`) {
					t.Errorf("hash-only request = %s", r.body)
				}
			}
		})
	}
}
