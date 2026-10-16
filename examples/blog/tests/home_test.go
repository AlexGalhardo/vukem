package tests

import (
	"testing"
)

func TestHome(t *testing.T) {
	app := newApp(t)

	app.Client(t).Get("/").AssertOK().AssertSee("Your Vukem app is running")
	app.Client(t).Get("/up").AssertOK()
	app.Client(t).Get("/js/htmx.min.js").AssertOK()
}
