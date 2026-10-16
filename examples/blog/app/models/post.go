// Package models holds the database models.
package models

import "github.com/AlexGalhardo/vukem/orm"

// Post is a row of the posts table, written by a user.
type Post struct {
	orm.Model

	UserID int64  `json:"user_id"`
	Title  string `json:"title"`
	Body   string `json:"body"`

	User     orm.BelongsTo[User]  `json:"author,omitzero"`
	Comments orm.HasMany[Comment] `json:"comments,omitzero"`
}

// Comment is a reply to a post.
type Comment struct {
	orm.Model

	PostID int64  `json:"post_id"`
	UserID int64  `json:"user_id"`
	Body   string `json:"body"`

	User orm.BelongsTo[User] `json:"author,omitzero"`
}
