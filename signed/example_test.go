package signed_test

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/signed"
)

func Example() {
	// In an app, derive the key: app.Key.Derive("signed.urls", 32).
	s := signed.New(bytes.Repeat([]byte{3}, 32))

	link, err := s.Sign("https://example.com/email/verify/42", time.Hour)
	if err != nil {
		fmt.Println(err)
		return
	}

	ok := httptest.NewRequest(http.MethodGet, link, http.NoBody)
	fmt.Println(s.Verify(ok) == nil)

	tampered := httptest.NewRequest(http.MethodGet, strings.Replace(link, "/42", "/43", 1), http.NoBody)
	fmt.Println(s.Verify(tampered) == nil)
	// Output:
	// true
	// false
}
