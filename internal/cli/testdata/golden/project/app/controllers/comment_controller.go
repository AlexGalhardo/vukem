package controllers

import (
	"net/http"

	"github.com/AlexGalhardo/vukem/web"
)

// CommentController handles comment requests.
type CommentController struct{}

// NewCommentController returns a CommentController.
func NewCommentController() *CommentController { return &CommentController{} }

// Index lists comments: GET /comments.
func (*CommentController) Index(c *web.Ctx) error {
	return c.String(http.StatusOK, "comments index")
}

// Create shows the form for a new comment: GET /comments/create.
func (*CommentController) Create(c *web.Ctx) error {
	return c.String(http.StatusOK, "new comment form")
}

// Store saves a new comment: POST /comments.
func (*CommentController) Store(c *web.Ctx) error {
	return c.Redirect("/comments")
}

// Show displays one comment: GET /comments/{comment}.
func (*CommentController) Show(c *web.Ctx) error {
	return c.String(http.StatusOK, "comment "+c.Param("comment"))
}

// Edit shows the edit form: GET /comments/{comment}/edit.
func (*CommentController) Edit(c *web.Ctx) error {
	return c.String(http.StatusOK, "edit comment "+c.Param("comment"))
}

// Update saves changes: PUT/PATCH /comments/{comment}.
func (*CommentController) Update(c *web.Ctx) error {
	return c.Redirect("/comments/" + c.Param("comment"))
}

// Destroy deletes a comment: DELETE /comments/{comment}.
func (*CommentController) Destroy(c *web.Ctx) error {
	return c.Redirect("/comments")
}
