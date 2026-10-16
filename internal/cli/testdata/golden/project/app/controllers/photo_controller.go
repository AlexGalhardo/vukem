package controllers

import (
	"net/http"

	"github.com/AlexGalhardo/vukem/web"
)

// PhotoController handles photo requests.
type PhotoController struct{}

// NewPhotoController returns a PhotoController.
func NewPhotoController() *PhotoController { return &PhotoController{} }

// Index lists photos: GET /photos.
func (*PhotoController) Index(c *web.Ctx) error {
	return c.JSON(http.StatusOK, map[string]any{"data": []any{}})
}

// Store creates a photo: POST /photos.
func (*PhotoController) Store(c *web.Ctx) error {
	return c.JSON(http.StatusCreated, map[string]any{})
}

// Show returns one photo: GET /photos/{photo}.
func (*PhotoController) Show(c *web.Ctx) error {
	id, err := c.ParamInt("photo")
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, map[string]any{"id": id})
}

// Update changes a photo: PUT/PATCH /photos/{photo}.
func (*PhotoController) Update(c *web.Ctx) error {
	return c.JSON(http.StatusOK, map[string]any{})
}

// Destroy deletes a photo: DELETE /photos/{photo}.
func (*PhotoController) Destroy(c *web.Ctx) error {
	return c.NoContent()
}
