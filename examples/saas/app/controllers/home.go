// Package controllers holds the HTTP handlers.
package controllers

import (
	"github.com/AlexGalhardo/vukem/inertia"
	"github.com/AlexGalhardo/vukem/web"
)

// Home renders the Home page component (resources/js/pages/Home).
func Home(c *web.Ctx) error { return inertia.Render(c, "Home", nil) }
