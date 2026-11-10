// Package resources is the JSON wire format of the models. It names an
// author by id and name only: the model's email never leaves the server.
package resources

import (
	"time"

	"github.com/AlexGalhardo/vukem/examples/blog/app/models"
)

// AuthorJSON is the public part of a models.User.
type AuthorJSON struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// CommentJSON is the wire format of models.Comment.
type CommentJSON struct {
	ID        int64     `json:"id"`
	PostID    int64     `json:"post_id"`
	UserID    int64     `json:"user_id"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

// PostJSON is the wire format of models.Post. Author and Comments are set
// only when the query loaded them.
type PostJSON struct {
	ID        int64         `json:"id"`
	UserID    int64         `json:"user_id"`
	Title     string        `json:"title"`
	Body      string        `json:"body"`
	CreatedAt time.Time     `json:"created_at"`
	Author    *AuthorJSON   `json:"author,omitempty"`
	Comments  []CommentJSON `json:"comments,omitempty"`
}

// Post transforms one model.
func Post(p models.Post) PostJSON {
	out := PostJSON{ID: p.ID, UserID: p.UserID, Title: p.Title, Body: p.Body, CreatedAt: p.CreatedAt}

	if u, err := p.User.Get(); err == nil && u != nil {
		out.Author = &AuthorJSON{ID: u.ID, Name: u.Name}
	}

	if cs, err := p.Comments.Get(); err == nil {
		for _, c := range cs {
			out.Comments = append(out.Comments, CommentJSON{
				ID: c.ID, PostID: c.PostID, UserID: c.UserID, Body: c.Body, CreatedAt: c.CreatedAt,
			})
		}
	}

	return out
}

// Posts transforms a list.
func Posts(ps []models.Post) []PostJSON {
	out := make([]PostJSON, len(ps))
	for i, p := range ps {
		out[i] = Post(p)
	}

	return out
}
