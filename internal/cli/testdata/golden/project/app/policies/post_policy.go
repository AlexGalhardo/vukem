package policies

import (
	"context"

	"github.com/AlexGalhardo/vukem/authz"

	"example.com/blog/app/models"
)

// PostPolicy decides what users may do with models.Post. Ability
// names map to methods: c.Authorize("update", post) calls Update.
type PostPolicy struct{}

// View allows reading a post.
func (PostPolicy) View(ctx context.Context, u *models.User, m *models.Post) bool {
	return true
}

// Create allows creating posts: c.Authorize("create", (*models.Post)(nil)).
func (PostPolicy) Create(ctx context.Context, u *models.User) bool {
	return true
}

// Update allows changing a post.
func (PostPolicy) Update(ctx context.Context, u *models.User, m *models.Post) authz.Result {
	return authz.Deny()
}

// Delete allows deleting a post.
func (PostPolicy) Delete(ctx context.Context, u *models.User, m *models.Post) authz.Result {
	return authz.Deny()
}
