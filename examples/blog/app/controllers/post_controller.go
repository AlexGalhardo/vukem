package controllers

import (
	"net/http"
	"strconv"

	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/examples/blog/app/models"
	"github.com/AlexGalhardo/vukem/examples/blog/app/requests"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/web"
)

// PostController is the blog: everyone reads, signed-in users write, and
// the PostPolicy guards changes. JSON clients get the same actions.
type PostController struct {
	db *database.DB
}

// NewPostController returns a PostController using db.
func NewPostController(db *database.DB) *PostController { return &PostController{db: db} }

// Index lists posts with their authors (one query per relation).
func (pc *PostController) Index(c *web.Ctx) error {
	q := orm.QueryOf[models.Post](pc.db).OrderBy("id", orm.Desc).With("User")
	if search := c.Query("q"); search != "" {
		q = q.Where("title", "LIKE", "%"+search+"%")
	}

	posts, err := q.Get(c.Context())
	if err != nil {
		return err
	}

	if c.WantsJSON() {
		return c.JSON(http.StatusOK, map[string]any{"data": posts})
	}

	return c.Render("posts/index", map[string]any{"Posts": posts, "Search": c.Query("q")}, web.Block("post-list"))
}

// Create shows the form for a new post.
func (*PostController) Create(c *web.Ctx) error {
	if err := c.Authorize("create", (*models.Post)(nil)); err != nil {
		return err
	}

	return c.Render("posts/create", nil)
}

// Store saves a new post written by the current user.
func (pc *PostController) Store(c *web.Ctx) error {
	u, err := auth.User[models.User](c, "web")
	if err != nil {
		return err
	}

	in, err := web.Parse(c, requests.PostSchema)
	if err != nil {
		return err
	}

	post := models.Post{UserID: u.ID, Title: in.Title, Body: in.Body}
	if err := orm.Create(c.Context(), pc.db, &post); err != nil {
		return err
	}

	if c.WantsJSON() {
		return c.Created("/posts/"+strconv.FormatInt(post.ID, 10), post)
	}

	_ = c.Flash("success", "Post created.")

	return c.RedirectRoute("posts.show", "post", post.ID)
}

// Show displays a post with its author and comments.
func (pc *PostController) Show(c *web.Ctx) error {
	post, err := pc.find(c, "User", "Comments")
	if err != nil {
		return err
	}

	if c.WantsJSON() {
		return c.JSON(http.StatusOK, post)
	}

	return c.Render("posts/show", &post)
}

// Edit shows the edit form (authors only).
func (pc *PostController) Edit(c *web.Ctx) error {
	post, err := pc.find(c)
	if err != nil {
		return err
	}

	if err := c.Authorize("update", &post); err != nil {
		return err
	}

	return c.Render("posts/edit", post)
}

// Update saves changes (authors only).
func (pc *PostController) Update(c *web.Ctx) error {
	post, err := pc.find(c)
	if err != nil {
		return err
	}

	if err := c.Authorize("update", &post); err != nil {
		return err
	}

	in, err := web.Parse(c, requests.PostSchema)
	if err != nil {
		return err
	}

	post.Title, post.Body = in.Title, in.Body
	if err := orm.Update(c.Context(), pc.db, &post); err != nil {
		return err
	}

	if c.WantsJSON() {
		return c.JSON(http.StatusOK, post)
	}

	_ = c.Flash("success", "Post updated.")

	return c.RedirectRoute("posts.show", "post", post.ID)
}

// Destroy deletes a post (authors only).
func (pc *PostController) Destroy(c *web.Ctx) error {
	post, err := pc.find(c)
	if err != nil {
		return err
	}

	if err := c.Authorize("delete", &post); err != nil {
		return err
	}

	if err := orm.Delete(c.Context(), pc.db, &post); err != nil {
		return err
	}

	switch {
	case c.WantsJSON():
		return c.NoContent()
	case c.IsHTMX():
		return c.String(http.StatusOK, "")
	}

	_ = c.Flash("success", "Post deleted.")

	return c.RedirectRoute("posts.index")
}

// Comment adds a comment by the current user.
func (pc *PostController) Comment(c *web.Ctx) error {
	u, err := auth.User[models.User](c, "web")
	if err != nil {
		return err
	}

	post, err := pc.find(c)
	if err != nil {
		return err
	}

	in, err := web.Parse(c, requests.CommentSchema)
	if err != nil {
		return err
	}

	if err := orm.Create(
		c.Context(),
		pc.db,
		&models.Comment{PostID: post.ID, UserID: u.ID, Body: in.Body},
	); err != nil {
		return err
	}

	return c.RedirectRoute("posts.show", "post", post.ID)
}

// find loads the post in the URL (with relations) or fails with 404.
func (pc *PostController) find(c *web.Ctx, with ...string) (models.Post, error) {
	id, err := c.ParamInt("post")
	if err != nil {
		return models.Post{}, err
	}

	q := orm.QueryOf[models.Post](pc.db)
	if len(with) > 0 {
		q = q.With(with...)
	}

	return q.Find(c.Context(), id)
}
