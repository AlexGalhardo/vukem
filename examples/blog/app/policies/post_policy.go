// Package policies decides what users may do with models.
package policies

import (
	"context"

	"github.com/AlexGalhardo/vukem/authz"
	"github.com/AlexGalhardo/vukem/examples/blog/app/models"
)

// PostPolicy lets anyone signed in write posts and only authors change them.
type PostPolicy struct{}

// Create allows any signed-in user to write a post.
func (PostPolicy) Create(_ context.Context, u *models.User) bool { return u.ID > 0 }

// Update allows the author only.
func (PostPolicy) Update(_ context.Context, u *models.User, p *models.Post) authz.Result {
	if p.UserID == u.ID {
		return authz.Allow()
	}

	return authz.Deny("Only the author can change this post.")
}

// Delete allows the author only.
func (p PostPolicy) Delete(ctx context.Context, u *models.User, post *models.Post) authz.Result {
	return p.Update(ctx, u, post)
}

// All lists the policies registered on the gate.
func All() []authz.Policy {
	return []authz.Policy{authz.For[*models.Post](PostPolicy{})}
}
