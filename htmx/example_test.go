package htmx_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/AlexGalhardo/vukem/htmx"
)

func Example() {
	h := func(w http.ResponseWriter, r *http.Request) {
		if !htmx.Is(r) {
			_, _ = fmt.Fprint(w, "<html>full page</html>")
			return
		}
		htmx.NewResponse(w).Trigger("post-created", nil).PushURL("/posts/1")
		_, _ = fmt.Fprint(w, "<li>Hello</li>")
	}

	req := httptest.NewRequest(http.MethodPost, "/posts", http.NoBody)
	req.Header.Set(htmx.HeaderRequest, "true")
	rec := httptest.NewRecorder()
	h(rec, req)

	fmt.Println(rec.Header().Get("HX-Trigger"))
	fmt.Println(rec.Header().Get("HX-Push-Url"))
	fmt.Println(rec.Body.String())
	// Output:
	// {"post-created":null}
	// /posts/1
	// <li>Hello</li>
}

func ExampleOOB() {
	fmt.Println(htmx.OOB("count", "innerHTML", "3 posts"))
	// Output: <div id="count" hx-swap-oob="innerHTML">3 posts</div>
}
