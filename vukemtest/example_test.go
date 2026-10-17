package vukemtest_test

import (
	"fmt"
	"testing"
	"time"

	vukem "github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/vukemtest"
	"github.com/AlexGalhardo/vukem/web"
)

// newApp stands in for an application's bootstrap.New.
func newApp(opts ...vukem.Option) (*vukem.App, error) {
	return vukem.New(append(opts, vukem.WithRoutes(func(r *router.Router, _ *vukem.App) error {
		r.Get("/ping", func(c *web.Ctx) error { return c.JSON(200, map[string]string{"pong": "ok"}) })
		return nil
	}))...)
}

// A test boots the app in process and asserts on its responses.
func Example() {
	_ = func(t *testing.T) {
		t.Helper()

		app := vukemtest.New(t, newApp)

		app.Client(t).
			GetJSON("/ping").
			AssertOK().
			AssertJSONPath("pong", "ok")
	}
}

func ExampleFakeClock() {
	clock := vukemtest.NewFakeClock(time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC))

	// Pass clock.Now as Options.Clock to drive the app's time.
	clock.Advance(90 * time.Minute)
	fmt.Println(clock.Now().Format(time.Kitchen))
	// Output: 10:30AM
}
