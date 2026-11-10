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

func TestSecurity_A06_PersistedQuerySizeCapped(t *testing.T) {
	t.Parallel()

	t.Run("oversized document runs but is not stored", func(t *testing.T) {
		t.Parallel()

		store := graphql.NewMemoryStore(0)
		h := counterSchema(t).Handler(graphql.HandlerConfig{Persisted: &graphql.PersistedQueries{Store: store}})

		query := "{ hello }" + strings.Repeat(" ", graphql.DefaultMaxPersistedBytes)
		r := post(t, h, `{"query":"`+query+`",`+apq(graphql.Hash(query))+`}`)
		if !strings.Contains(r.body, `"hello world"`) {
			t.Errorf("response = %s, want executed", r.body)
		}

		if _, ok, _ := store.Get(context.Background(), graphql.Hash(query)); ok {
			t.Error("oversized document stored")
		}
	})

	t.Run("configured limit", func(t *testing.T) {
		t.Parallel()

		store := graphql.NewMemoryStore(0)
		h := counterSchema(t).Handler(graphql.HandlerConfig{
			Persisted: &graphql.PersistedQueries{Store: store, MaxDocumentBytes: 8},
		})

		const query = "{ hello }"
		post(t, h, `{"query":"`+query+`",`+apq(graphql.Hash(query))+`}`)

		if _, ok, _ := store.Get(context.Background(), graphql.Hash(query)); ok {
			t.Error("document over MaxDocumentBytes stored")
		}
	})

	t.Run("memory store byte budget evicts the oldest", func(t *testing.T) {
		t.Parallel()

		store := graphql.NewMemoryStore(0)
		chunk := strings.Repeat("a", graphql.MaxMemoryStoreBytes/4)

		for _, k := range []string{"1", "2", "3", "4", "5"} {
			if err := store.Put(context.Background(), k, chunk); err != nil {
				t.Fatal(err)
			}
		}

		if _, ok, _ := store.Get(context.Background(), "1"); ok {
			t.Error("oldest entry kept past the byte budget")
		}

		if _, ok, _ := store.Get(context.Background(), "5"); !ok {
			t.Error("newest entry evicted")
		}
	})
}

func TestMemoryStoreSkipsDocumentOverBudget(t *testing.T) {
	t.Parallel()

	store := graphql.NewMemoryStore(0)
	if err := store.Put(t.Context(), "small", "{ hello }"); err != nil {
		t.Fatal(err)
	}

	if err := store.Put(t.Context(), "huge", strings.Repeat(" ", graphql.MaxMemoryStoreBytes+1)); err != nil {
		t.Fatal(err)
	}

	if _, ok, _ := store.Get(t.Context(), "huge"); ok {
		t.Error("a document over the whole byte budget was stored")
	}

	if _, ok, _ := store.Get(t.Context(), "small"); !ok {
		t.Error("a document over the byte budget evicted the other entries")
	}
}
