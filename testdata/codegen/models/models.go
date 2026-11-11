// Package models holds the models the codegen:models tests generate columns for.
package models

import (
	"time"

	"github.com/AlexGalhardo/vukem/orm"
)

// Settings is the JSON document of User.Settings.
type Settings struct {
	Theme string
}

// User is the first model.
type User struct {
	orm.Model

	Name     string
	Email    *string
	Age      int
	Joined   time.Time
	Settings orm.JSON[Settings]

	Posts      orm.HasMany[Post] `orm:"fk:user_id"`
	PostsCount int64             `orm:"count:Posts"`
}

// Post belongs to a user.
type Post struct {
	orm.Model

	UserID    int64
	Title     string
	Published orm.NullTime
}

// Table implements orm.Tabler: a name that exercises PascalCase.
func (Post) Table() string { return "blog_posts" }
