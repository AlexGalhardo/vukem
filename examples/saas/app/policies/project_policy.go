// Package policies decides what users may do with models.
package policies

import (
	"context"

	"github.com/AlexGalhardo/vukem/authz"
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/examples/saas/app/billing"
	"github.com/AlexGalhardo/vukem/examples/saas/app/models"
	"github.com/AlexGalhardo/vukem/orm"
)

// FreeProjects is how many projects a user without a subscription may own.
const FreeProjects = 3

// ProjectPolicy keeps projects private to their owner and limits free
// accounts to FreeProjects projects.
type ProjectPolicy struct {
	DB      *database.DB
	Billing *billing.Billing
}

// Create allows a new project while the user is under the free limit or
// has a valid subscription. A database error denies (fail closed).
func (p ProjectPolicy) Create(ctx context.Context, u *models.User) authz.Result {
	n, err := orm.QueryOf[models.Project](p.DB).Where("user_id", u.ID).Count(ctx)
	if err != nil {
		return authz.Deny()
	}

	if n < FreeProjects {
		return authz.Allow()
	}

	sub, ok, err := p.Billing.Current(ctx, u.ID)
	if err == nil && ok && sub.Valid() {
		return authz.Allow()
	}

	return authz.Deny("Free accounts have up to 3 projects. Choose a plan to add more.")
}

// View allows the owner only; others get 404, so ids reveal nothing.
func (ProjectPolicy) View(_ context.Context, u *models.User, p *models.Project) authz.Result {
	if p.UserID == u.ID {
		return authz.Allow()
	}

	return authz.DenyAsNotFound()
}

// Update allows the owner only.
func (pp ProjectPolicy) Update(ctx context.Context, u *models.User, p *models.Project) authz.Result {
	return pp.View(ctx, u, p)
}

// Delete allows the owner only.
func (pp ProjectPolicy) Delete(ctx context.Context, u *models.User, p *models.Project) authz.Result {
	return pp.View(ctx, u, p)
}

// All lists the policies registered on the gate.
func All(db *database.DB, b *billing.Billing) []authz.Policy {
	return []authz.Policy{authz.For[*models.Project](ProjectPolicy{DB: db, Billing: b})}
}
