package controllers

import (
	"net/http"

	"github.com/AlexGalhardo/vukem/web"
)

// HomeController handles home requests.
type HomeController struct{}

// NewHomeController returns a HomeController.
func NewHomeController() *HomeController { return &HomeController{} }

// Index handles the main home page.
func (*HomeController) Index(c *web.Ctx) error {
	return c.String(http.StatusOK, "Home")
}
