// Package controllers holds the HTTP handlers.
package controllers

import (
	"github.com/AlexGalhardo/vukem/examples/saas/app/pages"
	"github.com/AlexGalhardo/vukem/web"
)

// Home renders the Home page component (resources/js/pages/Home).
func Home(c *web.Ctx) error { return pages.Home.Render(c, struct{}{}) }
