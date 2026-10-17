package i18n_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing/fstest"

	"github.com/AlexGalhardo/vukem/i18n"
	"github.com/AlexGalhardo/vukem/z"
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

	mw, err := i18n.Middleware(b, i18n.MiddlewareConfig{})
	if err != nil {
		panic(err)
	}

	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
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

// A Refine callback returns z.FieldErrorKey so its message is translated
// like the built-in ones, from "validation.<key>".
func ExampleBundle_LocalizeIssues_refine() {
	lang := fstest.MapFS{
		"en.json": {Data: []byte(`{"validation": {
			"confirmed": "The {field} does not match the {other}."
		}}`)},
		"pt-BR.json": {Data: []byte(`{"validation": {
			"confirmed": "A {field} não confere.",
			"attributes": {"password_confirmation": "confirmação de senha"}
		}}`)},
	}

	b, err := i18n.Load(lang, i18n.Options{Default: "en"})
	if err != nil {
		panic(err)
	}

	type register struct {
		Password             string `json:"password"`
		PasswordConfirmation string `json:"password_confirmation"`
	}

	schema := z.Struct[register](z.Shape{
		"password":              z.String(),
		"password_confirmation": z.String(),
	}).Refine(func(r register) error {
		if r.Password != r.PasswordConfirmation {
			return z.FieldErrorKey("password_confirmation", "confirmed",
				"The {field} field does not match.", map[string]any{"other": "password"})
		}

		return nil
	})

	_, err = schema.Parse(map[string]any{"password": "a", "password_confirmation": "b"})
	is, _ := z.AsIssues(err)

	fmt.Println(is.First("password_confirmation"))
	fmt.Println(b.LocalizeIssues("en", is).First("password_confirmation"))
	fmt.Println(b.LocalizeIssues("pt-BR", is).First("password_confirmation"))

	// Output:
	// The password confirmation field does not match.
	// The password confirmation does not match the password.
	// A confirmação de senha não confere.
}
