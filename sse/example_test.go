package sse_test

import (
	"context"
	"net/http"

	"github.com/AlexGalhardo/vukem/sse"
)

func Example() {
	hub := sse.NewHub(nil) // or sse.NewHub(redisClient) across instances

	http.HandleFunc("GET /events", func(w http.ResponseWriter, r *http.Request) {
		_ = sse.Serve(w, r, func(s *sse.Stream) error { return hub.Stream(s, "posts") })
	})

	// Elsewhere, e.g. after creating a post:
	_ = hub.Publish(context.Background(), "posts", sse.Event{Event: "created", Data: "<li>New post</li>"})
	// Output:
}
