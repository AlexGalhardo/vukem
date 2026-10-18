package factories

import (
	"github.com/AlexGalhardo/vukem/factory"

	"example.com/blog/app/models"
)

// Post builds models.Post with fake data:
// factories.Post.Count(10).Create(ctx, db). Name the parameter f with
// the first fake field (f.Name(), f.Email(), f.Sentence()).
var Post = factory.New(func(_ *factory.Faker) models.Post {
	return models.Post{}
})
