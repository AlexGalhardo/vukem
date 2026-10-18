package middleware_test

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/AlexGalhardo/vukem/middleware"
	"github.com/AlexGalhardo/vukem/web"
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
	h := middleware.BodyLimit(16)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := web.NewCtx(w, r, &web.Services{Log: slog.New(slog.DiscardHandler)})

		var v map[string]any
		if err := c.BindJSON(&v); err != nil {
			c.HandleError(err)
			return
		}

		_ = c.NoContent()
	}))

	// Reading a body over the limit fails with 413. A declared
	// Content-Length over it fails on the first read, before any byte is
	// consumed; undeclared bodies are cut off while reading.
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"a":"`+strings.Repeat("x", 64)+`"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	fmt.Println(rec.Code)
	// Output:
	// 413
}
