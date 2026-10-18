package factories

import (
	"github.com/AlexGalhardo/vukem/factory"

	"example.com/blog/app/models"
)

// Comment builds models.Comment with fake data:
// factories.Comment.Count(10).Create(ctx, db). Name the parameter f with
// the first fake field (f.Name(), f.Email(), f.Sentence()).
var Comment = factory.New(func(_ *factory.Faker) models.Comment {
	return models.Comment{}
})
