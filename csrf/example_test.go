package csrf_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/AlexGalhardo/vukem/csrf"
)

func Example() {
	protect, err := csrf.Middleware(csrf.Config{BypassPatterns: []string{"POST /webhooks/"}})
	if err != nil {
		fmt.Println(err)
		return
	}
	h := protect(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	send := func(path, fetchSite string) int {
		req := httptest.NewRequest(http.MethodPost, "http://example.com"+path, http.NoBody)
		req.Header.Set("Sec-Fetch-Site", fetchSite)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	fmt.Println("same-origin form:", send("/posts", "same-origin"))
	fmt.Println("cross-site form:", send("/posts", "cross-site"))
	fmt.Println("cross-site webhook:", send("/webhooks/stripe", "cross-site"))
	// Output:
	// same-origin form: 204
	// cross-site form: 403
	// cross-site webhook: 204
}
