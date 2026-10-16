// Package requests holds validated request inputs.
package requests

import "github.com/AlexGalhardo/vukem/z"

// Post is the input of the create and edit forms.
type Post struct {
	Title string `json:"title" form:"title"`
	Body  string `json:"body"  form:"body"`
}

// PostSchema validates Post.
var PostSchema = z.Struct[Post](z.Shape{
	"title": z.String().Trim().Min(3).Max(120),
	"body":  z.String().Trim().Max(10_000).Optional(),
})

// Comment is the comment form.
type Comment struct {
	Body string `json:"body" form:"body"`
}

// CommentSchema validates Comment.
var CommentSchema = z.Struct[Comment](z.Shape{"body": z.String().Trim().Min(2).Max(2000)})
