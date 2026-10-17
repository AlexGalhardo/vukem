// Package factories builds models with fake data for tests and seeders.
package factories

import (
	"github.com/AlexGalhardo/vukem/examples/saas/app/models"
	"github.com/AlexGalhardo/vukem/factory"
)

// Project builds models.Project with fake data; give it an owner with State:
// factories.Project.State(func(p *models.Project) { p.UserID = u.ID }).Count(3).Create(ctx, db).
var Project = factory.New(func(f *factory.Faker) models.Project {
	return models.Project{Name: f.Words(2), Description: f.Sentence()}
})
