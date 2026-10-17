package social_test

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/social"
	"github.com/AlexGalhardo/vukem/web"
)

func Example() {
	// Apps derive the key with app.Key.Derive("social.state", 32) and pass
	// real providers: social.GitHub(cfg), social.Google(cfg)...
	s := social.New(bytes.Repeat([]byte{9}, 32),
		social.Fake("github", "/auth/github/callback", social.User{ID: "7", Email: "ana@example.com"}),
	)

	r := router.New(nil)
	r.Get("/auth/{provider}/redirect", func(c *web.Ctx) error { return s.Redirect(c, c.Param("provider")) })
	r.Get("/auth/{provider}/callback", func(c *web.Ctx) error {
		u, err := s.User(c, c.Param("provider"))
		if err != nil {
			return err
		}
		// Find or create the local user by (u.Provider, u.ID), then log in.
		return c.String(http.StatusOK, u.Provider+":"+u.ID)
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/auth/github/redirect", http.NoBody))

	// The provider sends the browser back with a code and the signed state.
	cb := httptest.NewRequest(http.MethodGet, rec.Header().Get("Location"), http.NoBody)
	for _, ck := range rec.Result().Cookies() {
		cb.AddCookie(ck)
	}
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, cb)
	fmt.Println(rec.Code, rec.Body.String())
	// Output: 200 github:7
}
