package policies

import (
	"context"

	"github.com/AlexGalhardo/vukem/authz"

	"example.com/blog/app/models"
)

// PostPolicy decides what users may do with models.Post. Ability
// names map to methods: c.Authorize("update", post) calls Update. Name
// the parameters (ctx, u, m) with the first decision that reads them.
type PostPolicy struct{}

// View allows reading a post.
func (PostPolicy) View(_ context.Context, _ *models.User, _ *models.Post) bool {
	return true
}

// Create allows creating posts: c.Authorize("create", (*models.Post)(nil)).
func (PostPolicy) Create(_ context.Context, _ *models.User) bool {
	return true
}

// Update allows changing a post.
func (PostPolicy) Update(_ context.Context, _ *models.User, _ *models.Post) authz.Result {
	return authz.Deny()
}

// Delete allows deleting a post.
func (PostPolicy) Delete(_ context.Context, _ *models.User, _ *models.Post) authz.Result {
	return authz.Deny()
}
