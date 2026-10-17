package apidocs_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/AlexGalhardo/vukem/apidocs"
	"github.com/AlexGalhardo/vukem/router"
)

func ExampleRegister() {
	r := router.New(nil)
	// Apps call apidocs.Serve(app, r, "/docs", cfg), which skips production.
	apidocs.Register(r, "/docs", apidocs.Config{Title: "Shop API", SpecURL: "/openapi.json"})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/docs", http.NoBody))
	fmt.Println(rec.Code, rec.Header().Get("Content-Type"))
	// Output: 200 text/html; charset=utf-8
}
