package controllers

import (
	"net/http"

	"github.com/AlexGalhardo/golang-framework/web"
)

// PostController handles post requests.
type PostController struct{}

// NewPostController returns a PostController.
func NewPostController() *PostController { return &PostController{} }

// Index lists posts: GET /posts.
func (*PostController) Index(c *web.Ctx) error {
	return c.String(http.StatusOK, "posts index")
}

// Create shows the form for a new post: GET /posts/create.
func (*PostController) Create(c *web.Ctx) error {
	return c.String(http.StatusOK, "new post form")
}

// Store saves a new post: POST /posts.
func (*PostController) Store(c *web.Ctx) error {
	return c.Redirect("/posts")
}

// Show displays one post: GET /posts/{post}.
func (*PostController) Show(c *web.Ctx) error {
	return c.String(http.StatusOK, "post "+c.Param("post"))
}

// Edit shows the edit form: GET /posts/{post}/edit.
func (*PostController) Edit(c *web.Ctx) error {
	return c.String(http.StatusOK, "edit post "+c.Param("post"))
}

// Update saves changes: PUT/PATCH /posts/{post}.
func (*PostController) Update(c *web.Ctx) error {
	return c.Redirect("/posts/" + c.Param("post"))
}

// Destroy deletes a post: DELETE /posts/{post}.
func (*PostController) Destroy(c *web.Ctx) error {
	return c.Redirect("/posts")
}
