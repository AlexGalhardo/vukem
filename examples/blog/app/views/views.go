// Package views declares the data type of each typed html/template view, so
// `app view:check` can compare the templates with it.
package views

import (
	"github.com/AlexGalhardo/vukem/examples/blog/app/models"
	"github.com/AlexGalhardo/vukem/view"
)

// PostsIndexData is what posts/index receives.
type PostsIndexData struct {
	Posts  []models.Post
	Search string
}

// The typed views of the post pages. PostsIndex's block is the htmx fragment
// the search box swaps.
var (
	PostsIndex  = view.Of[PostsIndexData]("posts/index", "post-list")
	PostsShow   = view.Of[*models.Post]("posts/show")
	PostsCreate = view.Of[struct{}]("posts/create")
	PostsEdit   = view.Of[models.Post]("posts/edit")
)

// All lists the typed views for vukem.WithViewPages.
func All() []view.Decl {
	return []view.Decl{PostsIndex.Decl(), PostsShow.Decl(), PostsCreate.Decl(), PostsEdit.Decl()}
}
