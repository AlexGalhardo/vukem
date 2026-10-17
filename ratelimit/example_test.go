package ratelimit_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/AlexGalhardo/vukem/ratelimit"
)

func Example() {
	limiter := ratelimit.NewMemory(2, time.Minute)
	h := ratelimit.Middleware(limiter, ratelimit.ByIP)(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}),
	)

	for range 3 {
		req := httptest.NewRequest(http.MethodGet, "/api/posts", http.NoBody)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		fmt.Println(rec.Code, rec.Header().Get("RateLimit-Remaining"))
	}
	// Output:
	// 204 1
	// 204 0
	// 429 0
}
