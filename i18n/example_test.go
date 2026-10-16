package i18n_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing/fstest"

	"github.com/AlexGalhardo/vukem/i18n"
)

func Example() {
	lang := fstest.MapFS{ // os.DirFS("resources/lang") in an app
		"en.json": {
			Data: []byte(`{"posts": {"count": {"zero": "No posts", "one": "{count} post", "other": "{count} posts"}}}`),
		},
		"pt.json": {
			Data: []byte(`{"posts": {"count": {"one": "{count} publicação", "other": "{count} publicações"}}}`),
		},
	}

	b, err := i18n.Load(lang, i18n.Options{Default: "en"})
	if err != nil {
		panic(err)
	}

	fmt.Println(b.T("en", "posts.count", "count", 0))
	fmt.Println(b.T("pt-BR", "posts.count", "count", 1))
	fmt.Println(b.T("pt-BR", "posts.count", "count", 3))

	h := i18n.Middleware(b, i18n.MiddlewareConfig{})(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		fmt.Println(i18n.Locale(r.Context()), i18n.T(r.Context(), "posts.count", "count", 2))
	}))

	r := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	r.Header.Set("Accept-Language", "de;q=0.9, pt-BR;q=0.8")
	h.ServeHTTP(httptest.NewRecorder(), r)

	// Output:
	// No posts
	// 1 publicação
	// 3 publicações
	// pt 2 publicações
}
