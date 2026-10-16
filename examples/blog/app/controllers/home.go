// Package controllers holds the HTTP handlers.
package controllers

import "github.com/AlexGalhardo/vukem/web"

// Home renders the welcome page.
func Home(c *web.Ctx) error { return c.Render("home", nil) }
