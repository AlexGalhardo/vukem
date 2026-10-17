package middleware_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/AlexGalhardo/vukem/middleware"
)

func Example() {
	hello := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "hello")
	})

	// Middleware are plain func(http.Handler) http.Handler values, so they
	// compose by hand or through router.Use.
	h := middleware.Recover()(
		middleware.RequestID()(
			middleware.SecureHeaders(middleware.SecureHeadersConfig{})(hello),
		),
	)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", http.NoBody))
	fmt.Println(rec.Code, rec.Body.String())
	fmt.Println(rec.Header().Get("X-Frame-Options"), rec.Header().Get("X-Content-Type-Options"))
	fmt.Println(rec.Header().Get("X-Request-Id") != "")
	// Output:
	// 200 hello
	// DENY nosniff
	// true
}

func ExampleBodyLimit() {
	h := middleware.BodyLimit(16)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	// A declared Content-Length over the limit is refused before the
	// handler runs; undeclared bodies are cut off while reading.
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("x", 64)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	fmt.Println(rec.Code)
	// Output:
	// 413
}
