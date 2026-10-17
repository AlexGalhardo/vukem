package graphql_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/broadcast"
	"github.com/AlexGalhardo/vukem/contrib/graphql"
)

func TestNewRequiresSubscriptionSourceStreams(t *testing.T) {
	t.Parallel()

	const sdl = `type Query { a: Int } type Subscription { tick: Int! }`

	tests := []struct {
		name string
		opts []graphql.Option
		want string
	}{
		{"unbound", nil, "Subscription.tick has no source stream"},
		{"bound as a plain field", []graphql.Option{
			graphql.Field("Subscription", "tick", func(context.Context, any, graphql.NoArgs) (int, error) {
				return 1, nil
			}),
		}, "Subscription.tick has no source stream"},
		{"unknown argument", []graphql.Option{
			graphql.Subscription("tick", func(context.Context, struct {
				N int `json:"n"`
			},
			) (<-chan int, error) {
				return nil, nil
			}),
		}, `Go argument "n" is not an argument`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			opts := append([]graphql.Option{
				graphql.Query("a", func(context.Context, graphql.NoArgs) (int, error) { return 1, nil }),
			}, tt.opts...)

			_, err := graphql.New(sdl, opts...)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("New() error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestSchemaSubscribe(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		s, _ := subApp(t)

		stream, resp := s.Subscribe(t.Context(), graphql.Request{Query: `subscription { n: countdown(from: 2) }`})
		if resp != nil {
			t.Fatalf("Subscribe() response = %+v, want a stream", resp)
		}

		var got []string
		for r := range stream {
			got = append(got, string(r.Data))
		}

		if diff := cmp.Diff([]string{`{"n":2}`, `{"n":1}`}, got); diff != "" {
			t.Errorf("Subscribe() events (-want +got):\n%s", diff)
		}

		stream, resp = s.Subscribe(t.Context(), graphql.Request{Query: `{ hello }`})
		if resp != nil {
			t.Fatalf("Subscribe(query) response = %+v, want a stream", resp)
		}

		for r := range stream {
			if string(r.Data) != `{"hello":"world"}` {
				t.Errorf("Subscribe(query) data = %s, want hello", r.Data)
			}
		}

		if _, resp := s.Subscribe(
			t.Context(),
			graphql.Request{Query: `subscription { adminFeed { id } }`},
		); resp == nil ||
			resp.Errors[0].Code() != graphql.CodeForbidden {
			t.Errorf("Subscribe(adminFeed) = %+v, want FORBIDDEN", resp)
		}
	})
}

func TestSchemaSubscribeStopsWithContext(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		s, b := subApp(t)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		stream, resp := s.Subscribe(ctx, graphql.Request{Query: `subscription { postCreated { title } }`})
		if resp != nil {
			t.Fatalf("Subscribe() response = %+v", resp)
		}

		synctest.Wait()

		if err := b.Publish(t.Context(), "posts", "post.created", wsPost{Title: "a"}); err != nil {
			t.Fatal(err)
		}

		// Other events on the channel are filtered out by name.
		if err := b.Publish(t.Context(), "posts", "post.deleted", wsPost{Title: "b"}); err != nil {
			t.Fatal(err)
		}

		n := 0
		for r := range stream {
			n++

			if string(r.Data) != `{"postCreated":{"title":"a"}}` {
				t.Errorf("event data = %s", r.Data)
			}

			cancel()
		}

		if n != 1 {
			t.Errorf("got %d events, want 1", n)
		}
		// synctest.Test fails if the FromBroadcast goroutine is still alive.
	})
}

func TestFromBroadcastRawMessages(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		b := broadcast.New(broadcast.Config{})
		t.Cleanup(b.Close)

		if err := b.Public("room"); err != nil {
			t.Fatal(err)
		}

		src := graphql.FromBroadcast[graphql.NoArgs, broadcast.Message](b,
			func(context.Context, graphql.NoArgs) ([]string, error) { return []string{"room"}, nil })

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		ch, err := src(ctx, graphql.NoArgs{})
		if err != nil {
			t.Fatal(err)
		}

		synctest.Wait()

		if err := b.Publish(t.Context(), "room", "said", map[string]string{"text": "hi"}); err != nil {
			t.Fatal(err)
		}

		m := <-ch
		want := broadcast.Message{Channel: "room", Event: "said", Data: json.RawMessage(`{"text":"hi"}`)}

		if diff := cmp.Diff(want, m); diff != "" {
			t.Errorf("message (-want +got):\n%s", diff)
		}

		if _, err := graphql.FromBroadcast[graphql.NoArgs, int](nil, nil)(ctx, graphql.NoArgs{}); err == nil {
			t.Error("FromBroadcast(nil) error = nil, want an error")
		}
	})
}

func TestHandlerServesSubscriptionsOverSSE(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		query      string
		wantStatus int
		want       string
	}{
		{
			"subscription", `subscription { countdown(from: 2) }`, http.StatusOK,
			"event: next\ndata: {\"data\":{\"countdown\":2}}\n\n" +
				"event: next\ndata: {\"data\":{\"countdown\":1}}\n\nevent: complete\ndata: \n\n",
		},
		{
			"query", `{ hello }`, http.StatusOK,
			"event: next\ndata: {\"data\":{\"hello\":\"world\"}}\n\nevent: complete\ndata: \n\n",
		},
		{"forbidden channel", `subscription { adminFeed { id } }`, http.StatusOK, `"code":"FORBIDDEN"`},
		{"validation error", `subscription { nope }`, http.StatusBadRequest, "GRAPHQL_VALIDATION_FAILED"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				s, _ := subApp(t)
				body, _ := json.Marshal(map[string]string{"query": tt.query})
				req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/graphql", bytes.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Accept", "text/event-stream")

				rec := httptest.NewRecorder()
				s.Handler(graphql.HandlerConfig{}).ServeHTTP(rec, req)

				if rec.Code != tt.wantStatus {
					t.Fatalf("status = %d, want %d (%s)", rec.Code, tt.wantStatus, rec.Body)
				}

				if got := rec.Body.String(); !strings.Contains(got, tt.want) {
					t.Errorf("body = %q, want it to contain %q", got, tt.want)
				}
			})
		})
	}
}
