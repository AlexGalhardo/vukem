package auth_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

type exUser struct{ ID, Hash string }

func (u exUser) AuthID() string       { return u.ID }
func (u exUser) AuthPassword() string { return u.Hash }

// exHeaderGuard stands in for auth.NewSessionGuard or auth.NewTokenGuard,
// which need a database.
type exHeaderGuard struct{}

func (exHeaderGuard) Name() string { return "demo" }

func (exHeaderGuard) Check(c *web.Ctx) bool {
	id := c.Request().Header.Get("X-Demo-User")
	if id == "" {
		return false
	}
	auth.SetUser(c, "demo", exUser{ID: id})
	return true
}

func ExampleRequired() {
	guard := exHeaderGuard{}
	r := router.New(nil)
	r.Get("/me", func(c *web.Ctx) error {
		u, err := auth.User[exUser](c, guard.Name())
		if err != nil {
			return err
		}
		return c.String(http.StatusOK, "hello "+u.ID)
	}).Use(auth.Required(guard))

	anon := httptest.NewRequest(http.MethodGet, "/me", http.NoBody)
	anon.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, anon)
	fmt.Println(rec.Code)

	signedIn := httptest.NewRequest(http.MethodGet, "/me", http.NoBody)
	signedIn.Header.Set("X-Demo-User", "42")
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, signedIn)
	fmt.Println(rec.Code, rec.Body.String())
	// Output:
	// 401
	// 200 hello 42
}
