package factories

import (
	"github.com/AlexGalhardo/vukem/factory"

	"example.com/blog/app/models"
)

// Comment builds models.Comment with fake data:
// factories.Comment.Count(10).Create(ctx, db).
var Comment = factory.New(func(f *factory.Faker) models.Comment {
	return models.Comment{}
})
