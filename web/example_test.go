package web_test

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/web"
	"github.com/AlexGalhardo/vukem/z"
)

type createPost struct {
	Title string `json:"title"`
}

var createPostSchema = z.Struct[createPost](z.Shape{"title": z.String().Trim().Min(3)})

// serve runs h the way the router does: errors go to the error handler.
func serve(h web.HandlerFunc, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	web.NewCtx(rec, req, &web.Services{Log: slog.New(slog.DiscardHandler)}).Serve(h)
	return rec
}

func Example() {
	show := func(c *web.Ctx) error {
		if c.Query("id") != "1" {
			return errs.ErrNotFound
		}
		return c.JSON(http.StatusOK, web.Map{"id": 1, "title": "Hello"})
	}

	ok := httptest.NewRequest(http.MethodGet, "/posts?id=1", http.NoBody)
	rec := serve(show, ok)
	fmt.Println(rec.Code, strings.TrimSpace(rec.Body.String()))

	missing := httptest.NewRequest(http.MethodGet, "/posts?id=2", http.NoBody)
	missing.Header.Set("Accept", "application/json")
	rec = serve(show, missing)
	fmt.Println(rec.Code, rec.Header().Get("Content-Type"))
	// Output:
	// 200 {"id":1,"title":"Hello"}
	// 404 application/problem+json
}

func ExampleParse() {
	store := func(c *web.Ctx) error {
		in, err := web.Parse(c, createPostSchema)
		if err != nil {
			return err // 422 problem+json, HTMX fragment or redirect back
		}
		return c.String(http.StatusCreated, "created "+in.Title)
	}

	req := httptest.NewRequest(http.MethodPost, "/posts", strings.NewReader(`{"title":"  Gophers  "}`))
	req.Header.Set("Content-Type", "application/json")
	rec := serve(store, req)
	fmt.Println(rec.Code, rec.Body.String())

	req = httptest.NewRequest(http.MethodPost, "/posts", strings.NewReader(`{"title":"Go"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	rec = serve(store, req)
	fmt.Println(rec.Code)
	// Output:
	// 201 created Gophers
	// 422
}
